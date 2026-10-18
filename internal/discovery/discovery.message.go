package discovery

import (
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// maxPacket bounds a datagram, received or sent (mDNS allows 9000 bytes;
// an advertisement takes a few hundred).
const maxPacket = 9000

// Class bits of mDNS (RFC 6762 §5.4, §10.2): a question's top bit asks for
// a unicast answer; a record's says it replaces cached ones.
const (
	unicastResponse = 1 << 15
	cacheFlush      = 1 << 15
)

// message is a parsed mDNS packet: its questions, and every record in its
// answer and additional sections.
type message struct {
	id        uint16
	response  bool
	questions []dnsmessage.Question
	records   []dnsmessage.Resource
}

var errNotDNS = errors.New("not a DNS message")

func parseMessage(b []byte) (message, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		return message{}, errNotDNS
	}
	m := message{id: h.ID, response: h.Response}
	if m.questions, err = p.AllQuestions(); err != nil {
		return message{}, err
	}
	answers, err := p.AllAnswers()
	if err != nil {
		return message{}, err
	}
	if err := p.SkipAllAuthorities(); err != nil {
		return message{}, err
	}
	extra, err := p.AllAdditionals()
	if err != nil {
		return message{}, err
	}
	m.records = slices.Concat(answers, extra)
	return m, nil
}

func sameName(a dnsmessage.Name, b string) bool { return strings.EqualFold(a.String(), b) }

// query is a question for every Hive runtime.
func query(id uint16) ([]byte, error) {
	b := dnsmessage.NewBuilder(make([]byte, 0, 64), dnsmessage.Header{ID: id})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{
		Name: dnsmessage.MustNewName(Service), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET,
	}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// probe asks whether anyone holds the advertisement's name (RFC 6762 §8.1).
func probe(a Advertisement) ([]byte, error) {
	name, err := dnsmessage.NewName(a.instanceFQDN())
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(make([]byte, 0, 128), dnsmessage.Header{})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeALL, Class: dnsmessage.ClassINET | unicastResponse}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// answer is what a response carries.
type answer struct {
	// ptr answers the service question; services the enumeration one.
	ptr, services bool
	// instance adds SRV and TXT; host the addresses.
	instance, host bool
}

func (w answer) empty() bool { return !w.ptr && !w.services && !w.instance && !w.host }

// response builds an answer about a: the records w asks for, the rest of
// the advertisement as additional records. legacy answers a one-shot
// query: it echoes its ID and questions, with short TTLs and no
// cache-flush bits. ttl 0 says goodbye.
func response(a Advertisement, addrs []netip.Addr, w answer, ttl time.Duration, legacy *message) ([]byte, error) {
	service := dnsmessage.MustNewName(Service)
	instance, err := dnsmessage.NewName(a.instanceFQDN())
	if err != nil {
		return nil, err
	}
	host := dnsmessage.MustNewName(a.hostFQDN())
	h := dnsmessage.Header{Response: true, Authoritative: true}
	flush := dnsmessage.Class(cacheFlush)
	if legacy != nil {
		h.ID, flush = legacy.id, 0
		ttl = min(ttl, legacyTTL)
	}
	b := dnsmessage.NewBuilder(make([]byte, 0, 512), h)
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if legacy != nil {
		for _, q := range legacy.questions {
			q.Class &^= unicastResponse
			if err := b.Question(q); err != nil {
				return nil, err
			}
		}
	}
	secs := uint32(ttl / time.Second)
	hdr := func(name dnsmessage.Name, unique bool) dnsmessage.ResourceHeader {
		c := dnsmessage.ClassINET
		if unique {
			c |= flush
		}
		return dnsmessage.ResourceHeader{Name: name, Class: c, TTL: secs}
	}
	srv := func() error {
		return b.SRVResource(hdr(instance, true), dnsmessage.SRVResource{Port: uint16(a.Port), Target: host})
	}
	txt := func() error { return b.TXTResource(hdr(instance, true), dnsmessage.TXTResource{TXT: a.txt()}) }
	ips := func() error {
		for _, ip := range addrs {
			var err error
			if ip.Is4() {
				err = b.AResource(hdr(host, true), dnsmessage.AResource{A: ip.As4()})
			} else {
				err = b.AAAAResource(hdr(host, true), dnsmessage.AAAAResource{AAAA: ip.As16()})
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	if w.services {
		if err := b.PTRResource(hdr(dnsmessage.MustNewName(servicesName), false), dnsmessage.PTRResource{PTR: service}); err != nil {
			return nil, err
		}
	}
	if w.ptr {
		if err := b.PTRResource(hdr(service, false), dnsmessage.PTRResource{PTR: instance}); err != nil {
			return nil, err
		}
	}
	if w.instance {
		if err := srv(); err != nil {
			return nil, err
		}
		if err := txt(); err != nil {
			return nil, err
		}
	}
	if w.host {
		if err := ips(); err != nil {
			return nil, err
		}
	}
	// What the asker will want next comes along (RFC 6763 §12).
	if err := b.StartAdditionals(); err != nil {
		return nil, err
	}
	if w.ptr && !w.instance {
		if err := srv(); err != nil {
			return nil, err
		}
		if err := txt(); err != nil {
			return nil, err
		}
	}
	if (w.ptr || w.instance) && !w.host {
		if err := ips(); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

// records assembles the runtimes a message describes. Instances without a
// valid TXT (v= and id=) are not Hive runtimes and are counted in invalid.
// goodbyes are the instances it says are gone (TTL 0).
func (m message) runtimes(now time.Time) (found []Record, goodbyes []string, invalid int) {
	var instances []string
	srvs := map[string]dnsmessage.SRVResource{}
	txts := map[string][]string{}
	ttls := map[string]uint32{}
	addrs := map[string][]netip.Addr{}
	for _, r := range m.records {
		name := strings.ToLower(r.Header.Name.String())
		switch body := r.Body.(type) {
		case *dnsmessage.PTRResource:
			if !sameName(r.Header.Name, Service) {
				continue
			}
			target := body.PTR.String()
			if r.Header.TTL == 0 {
				goodbyes = append(goodbyes, instanceLabel(target))
				continue
			}
			instances = append(instances, target)
		case *dnsmessage.SRVResource:
			srvs[name], ttls[name] = *body, r.Header.TTL
			if !strings.HasSuffix(name, "."+Service) {
				continue
			}
			if !containsFold(instances, name) {
				instances = append(instances, r.Header.Name.String()) // answered without a PTR
			}
		case *dnsmessage.TXTResource:
			txts[name] = body.TXT
		case *dnsmessage.AResource:
			addrs[name] = append(addrs[name], netip.AddrFrom4(body.A))
		case *dnsmessage.AAAAResource:
			addrs[name] = append(addrs[name], netip.AddrFrom16(body.AAAA).Unmap())
		}
	}
	for _, inst := range instances {
		key := strings.ToLower(inst)
		srv, ok := srvs[key]
		if !ok {
			continue // only the name: a later answer has the rest
		}
		if ttls[key] == 0 {
			goodbyes = append(goodbyes, instanceLabel(inst))
			continue
		}
		rec, ok := parseTXT(txts[key])
		if !ok {
			invalid++
			continue
		}
		rec.Name = instanceLabel(inst)
		rec.Host = strings.ToLower(srv.Target.String())
		rec.Port = int(srv.Port)
		rec.Addrs = append([]netip.Addr(nil), addrs[rec.Host]...)
		rec.Seen = now
		rec.ttl = time.Duration(ttls[key]) * time.Second
		found = append(found, rec)
	}
	return found, goodbyes, invalid
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// instanceLabel is an instance's name without the service.
func instanceLabel(fqdn string) string {
	if len(fqdn) > len(Service) && strings.EqualFold(fqdn[len(fqdn)-len(Service):], Service) {
		return fqdn[:len(fqdn)-len(Service)-1]
	}
	return strings.TrimSuffix(fqdn, ".")
}

// parseTXT reads an advertisement's TXT strings.
func parseTXT(txt []string) (Record, bool) {
	var r Record
	for _, kv := range txt {
		k, v, _ := strings.Cut(kv, "=")
		switch strings.ToLower(k) {
		case "v":
			r.Version, _ = strconv.Atoi(v)
		case "id":
			r.ID = strings.ToLower(v)
		case "api":
			r.APILevel, _ = strconv.Atoi(v)
		case "caps":
			for c := range strings.SplitSeq(v, ",") {
				if c = strings.TrimSpace(c); c != "" {
					r.Caps = append(r.Caps, c)
				}
			}
		}
	}
	return r, r.Version >= 1 && ValidID(r.ID) == nil
}
