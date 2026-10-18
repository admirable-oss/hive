package discovery

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// BrowseConfig says where and how long to look.
type BrowseConfig struct {
	// Timeout is how long to listen for answers (default 3 s).
	Timeout time.Duration
	// Interfaces are the only interfaces multicast on, when not empty.
	Interfaces []string
	// Addrs are asked directly, host[:port] (port 5353 by default): the
	// way to find runtimes where multicast does not reach.
	Addrs []string
	// NoMulticast asks only Addrs.
	NoMulticast bool
}

// DefaultTimeout is how long a browse listens unless told.
const DefaultTimeout = 3 * time.Second

// maxQueries bounds the queries one browse sends to each destination.
const maxQueries = 3

// Result is what a browse found, and what it tried.
type Result struct {
	Runtimes []Record `json:"runtimes"`
	// Interfaces are those it asked on; Skipped those it did not, with why.
	Interfaces []Interface `json:"interfaces"`
	Skipped    []Interface `json:"skipped_interfaces,omitempty"`
	// Asked are the addresses asked directly.
	Asked []string `json:"asked,omitempty"`
	// Errors are failures to send or listen.
	Errors []string `json:"errors,omitempty"`
	// Answers counts the answers received; Invalid those that were not a
	// Hive runtime's.
	Answers int `json:"answers"`
	Invalid int `json:"invalid,omitempty"`
}

// Browse asks for Hive runtimes and collects the answers until the timeout
// (or ctx) ends. It fails only when it could ask nobody; what it could not
// do is in Result.Errors.
func Browse(ctx context.Context, cfg BrowseConfig) (Result, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	res := Result{Interfaces: []Interface{}, Runtimes: []Record{}}
	var direct []netip.AddrPort
	for _, a := range cfg.Addrs {
		ap, err := splitHostPort(ctx, a)
		if err != nil {
			return res, err
		}
		direct = append(direct, ap)
		res.Asked = append(res.Asked, ap.String())
	}
	var ifaces []Interface
	if !cfg.NoMulticast {
		use, skipped, err := Interfaces(cfg.Interfaces)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
		ifaces, res.Skipped = use, skipped
	}
	c, err := openBrowser(ctx, ifaces)
	if err != nil {
		return res, err
	}
	defer c.close()
	res.Interfaces = append(res.Interfaces, c.ifaces...)
	res.Skipped = append(res.Skipped, c.skipped...)
	if len(c.ifaces) == 0 && len(direct) == 0 {
		return res, errNoInterfaces
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, c.close)
	defer stop()

	// Queries go out at once and then every second, a few times: one lost
	// datagram does not hide a runtime.
	var wg sync.WaitGroup
	defer wg.Wait()
	errs := make(chan string, 64)
	wg.Go(func() {
		for n := 0; n < maxQueries; n++ {
			q, err := query(0) // answers to any query are wanted alike
			if err != nil {
				return
			}
			for _, e := range c.ask(q, direct) {
				select {
				case errs <- e:
				default:
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	})

	found := map[string]*Record{}
	for p := range c.packets(ctx) {
		m, err := parseMessage(p.data)
		if err != nil || !m.response {
			continue
		}
		recs, goodbyes, invalid := m.runtimes(time.Now())
		res.Invalid += invalid
		if len(recs) > 0 || invalid > 0 {
			res.Answers++
		}
		for _, name := range goodbyes {
			for id, r := range found {
				if strings.EqualFold(r.Name, name) {
					delete(found, id)
				}
			}
		}
		ifName := c.ifName(p.ifIndex)
		for _, rec := range recs {
			rec.Interface = ifName
			for i, a := range rec.Addrs {
				rec.Addrs[i] = zoned(a, ifName)
			}
			if len(rec.Addrs) == 0 && p.src.Addr().IsValid() {
				rec.Addrs = []netip.Addr{zoned(p.src.Addr(), ifName)}
			}
			merge(found, rec)
		}
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if !slices.Contains(res.Errors, e) {
			res.Errors = append(res.Errors, e)
		}
	}
	for _, r := range found {
		sortAddrs(r.Addrs)
		res.Runtimes = append(res.Runtimes, *r)
	}
	markDuplicates(res.Runtimes)
	slices.SortFunc(res.Runtimes, func(a, b Record) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return res, nil
}

// merge adds rec to found: one entry per identity, with every address it
// was seen at, and its latest name.
func merge(found map[string]*Record, rec Record) {
	old, ok := found[rec.ID]
	if !ok {
		found[rec.ID] = &rec
		return
	}
	for _, a := range rec.Addrs {
		if !slices.Contains(old.Addrs, a) {
			old.Addrs = append(old.Addrs, a)
		}
	}
	old.Name, old.Host, old.Port, old.Seen = rec.Name, rec.Host, rec.Port, rec.Seen
	old.Version, old.APILevel, old.Caps = rec.Version, rec.APILevel, rec.Caps
	if old.Interface == "" {
		old.Interface = rec.Interface
	}
}

// markDuplicates flags runtimes sharing a name with another identity.
func markDuplicates(rs []Record) {
	count := map[string]int{}
	for _, r := range rs {
		count[strings.ToLower(r.Name)]++
	}
	for i := range rs {
		rs[i].DuplicateName = count[strings.ToLower(rs[i].Name)] > 1
	}
}

// browser is a browse's sockets: on ephemeral ports, so the answers come
// back to it alone (RFC 6762 §5.1).
type browser struct {
	ifaces, skipped []Interface
	pc4, pc6        net.PacketConn
	c4              *ipv4.PacketConn
	c6              *ipv6.PacketConn
	once            sync.Once
}

func openBrowser(ctx context.Context, ifaces []Interface) (*browser, error) {
	b := &browser{}
	var errs []error
	var lc net.ListenConfig
	if pc, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:0"); err == nil {
		b.pc4, b.c4 = pc, ipv4.NewPacketConn(pc)
		_ = b.c4.SetMulticastTTL(255)
		_ = b.c4.SetControlMessage(ipv4.FlagInterface, true)
	} else {
		errs = append(errs, err)
	}
	if pc, err := lc.ListenPacket(ctx, "udp6", "[::]:0"); err == nil {
		b.pc6, b.c6 = pc, ipv6.NewPacketConn(pc)
		_ = b.c6.SetMulticastHopLimit(255)
		_ = b.c6.SetControlMessage(ipv6.FlagInterface, true)
	} else {
		errs = append(errs, err)
	}
	if b.c4 == nil && b.c6 == nil {
		return nil, errors.Join(errs...)
	}
	for _, ifc := range ifaces {
		has4 := slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return p.Addr().Is4() })
		has6 := slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return p.Addr().Is6() })
		if (has4 && b.c4 != nil) || (has6 && b.c6 != nil) {
			b.ifaces = append(b.ifaces, ifc)
			continue
		}
		ifc.Skipped = "no socket for its address family"
		b.skipped = append(b.skipped, ifc)
	}
	return b, nil
}

// ask sends q to the group on each interface and to each direct address,
// and returns what failed.
func (b *browser) ask(q []byte, direct []netip.AddrPort) []string {
	var errs []string
	fail := func(where string, err error) {
		if err != nil {
			errs = append(errs, where+": "+err.Error())
		}
	}
	for _, ifc := range b.ifaces {
		ni := &net.Interface{Index: ifc.Index, Name: ifc.Name}
		has4 := slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return p.Addr().Is4() })
		has6 := slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return p.Addr().Is6() })
		if has4 && b.c4 != nil {
			err := b.c4.SetMulticastInterface(ni)
			if err == nil {
				_, err = b.c4.WriteTo(q, nil, net.UDPAddrFromAddrPort(groupV4))
			}
			fail(ifc.Name+" (IPv4)", err)
		}
		if has6 && b.c6 != nil {
			err := b.c6.SetMulticastInterface(ni)
			if err == nil {
				_, err = b.c6.WriteTo(q, nil, &net.UDPAddr{IP: groupV6.Addr().AsSlice(), Port: Port, Zone: ifc.Name})
			}
			fail(ifc.Name+" (IPv6)", err)
		}
	}
	for _, d := range direct {
		var err error
		switch {
		case d.Addr().Is4() && b.c4 != nil:
			_, err = b.c4.WriteTo(q, nil, net.UDPAddrFromAddrPort(d))
		case d.Addr().Is6() && b.c6 != nil:
			_, err = b.c6.WriteTo(q, nil, net.UDPAddrFromAddrPort(d))
		default:
			err = errors.New("no socket for its address family")
		}
		fail(d.String(), err)
	}
	return errs
}

// packets yields the datagrams received until ctx ends.
func (b *browser) packets(ctx context.Context) <-chan packet {
	out := make(chan packet, 16)
	var wg sync.WaitGroup
	read := func(readFrom func([]byte) (int, int, net.Addr, error)) {
		buf := make([]byte, maxPacket)
		for {
			n, ifIndex, src, err := readFrom(buf)
			if err != nil {
				return
			}
			u, ok := src.(*net.UDPAddr)
			if !ok {
				continue
			}
			ap := u.AddrPort()
			p := packet{data: append([]byte(nil), buf[:n]...), ifIndex: ifIndex, src: netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())}
			select {
			case out <- p:
			case <-ctx.Done():
				return
			}
		}
	}
	if b.c4 != nil {
		wg.Go(func() {
			read(func(buf []byte) (int, int, net.Addr, error) {
				n, cm, src, err := b.c4.ReadFrom(buf)
				if cm != nil {
					return n, cm.IfIndex, src, err
				}
				return n, 0, src, err
			})
		})
	}
	if b.c6 != nil {
		wg.Go(func() {
			read(func(buf []byte) (int, int, net.Addr, error) {
				n, cm, src, err := b.c6.ReadFrom(buf)
				if cm != nil {
					return n, cm.IfIndex, src, err
				}
				return n, 0, src, err
			})
		})
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func (b *browser) ifName(index int) string {
	for _, ifc := range b.ifaces {
		if ifc.Index == index {
			return ifc.Name
		}
	}
	return ""
}

func (b *browser) close() {
	b.once.Do(func() {
		if b.pc4 != nil {
			_ = b.pc4.Close()
		}
		if b.pc6 != nil {
			_ = b.pc6.Close()
		}
	})
}
