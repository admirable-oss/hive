package discovery

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// ResponderConfig is what a daemon advertises, and where.
type ResponderConfig struct {
	Advertisement Advertisement
	// Interfaces are the only interfaces used, when not empty.
	Interfaces []string
	// Addr, when set, answers unicast queries on this address only (see
	// EnvAddr) instead of joining the multicast group.
	Addr   string
	Logger *slog.Logger
}

// Timing of probing and announcing (RFC 6762 §8). Variables for tests.
var (
	probeInterval    = 250 * time.Millisecond
	announceInterval = time.Second
	// minAnswerInterval bounds multicast answers to the same question on
	// one interface (RFC 6762 §6).
	minAnswerInterval = time.Second
)

const (
	// probes are sent before a name is used.
	probes = 3
	// maxRenames bounds renaming after conflicts.
	maxRenames = 20
	// legacyPerSecond bounds answers to one-shot queries, so a responder
	// cannot be made to flood.
	legacyPerSecond = 20
)

// Advertise answers queries for the advertisement until ctx ends, then
// says goodbye. A name another runtime already uses is changed ("laptop
// (2)"). It returns an error when it cannot listen at all.
func Advertise(ctx context.Context, cfg ResponderConfig) error {
	if err := cfg.Advertisement.validate(); err != nil {
		return err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var t transport
	if cfg.Addr != "" {
		u, err := listenUnicast(cfg.Addr)
		if err != nil {
			return err
		}
		t = u
	} else {
		use, skipped, err := Interfaces(cfg.Interfaces)
		if err != nil {
			return err
		}
		for _, s := range skipped {
			log.Debug("discovery skips an interface", "interface", s.Name, "reason", s.Skipped)
		}
		m, skipped, err := listenMulticast(use)
		for _, s := range skipped {
			log.Warn("discovery skips an interface", "interface", s.Name, "reason", s.Skipped)
		}
		if err != nil {
			return err
		}
		t = m
		names := make([]string, 0, len(m.ifaces))
		for _, i := range m.ifaces {
			names = append(names, i.Name)
		}
		log.Info("advertising on the local network", "name", cfg.Advertisement.Instance, "id", cfg.Advertisement.ID, "interfaces", strings.Join(names, ","))
	}
	r := newResponder(t, cfg.Advertisement, log)
	return r.run(ctx)
}

type responder struct {
	t   transport
	log *slog.Logger

	mu   sync.Mutex
	adv  Advertisement
	base string // the configured name

	renames  int
	live     atomic.Bool // probed and announced: it answers
	answered map[answerKey]time.Time
	legacyAt time.Time
	legacyN  int
}

type answerKey struct {
	ifIndex int
	w       answer
}

func newResponder(t transport, adv Advertisement, log *slog.Logger) *responder {
	return &responder{t: t, adv: adv, base: adv.Instance, log: log, answered: map[answerKey]time.Time{}}
}

// Name is the name it advertises now.
func (r *responder) Name() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adv.Instance
}

func (r *responder) multicasting() bool { return len(r.t.interfaces()) > 0 }

func (r *responder) run(ctx context.Context) error {
	packets := make(chan packet, 16)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(packets)
		for {
			p, err := r.t.read()
			if err != nil {
				return
			}
			select {
			case packets <- p:
			case <-ctx.Done():
				return
			}
		}
	})
	defer wg.Wait()
	defer func() { _ = r.t.close() }()

	sent := 0 // probes sent; then announcements
	timer := time.NewTimer(0)
	defer timer.Stop()
	if !r.multicasting() {
		r.live.Store(true) // nobody to probe or announce to
		timer.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			r.goodbye()
			return nil
		case p, ok := <-packets:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("discovery: the socket closed")
			}
			if r.handle(p) {
				// A conflict: probe the new name.
				sent = 0
				r.live.Store(false)
				timer.Reset(probeInterval)
			}
		case <-timer.C:
			switch {
			case sent < probes:
				r.send(func(a Advertisement, _ []netip.Addr) ([]byte, error) { return probe(a) })
				sent++
				timer.Reset(probeInterval)
			case sent < probes+2:
				r.live.Store(true)
				r.send(func(a Advertisement, addrs []netip.Addr) ([]byte, error) {
					return response(a, addrs, answer{ptr: true, instance: true, host: true}, TTL, nil)
				})
				sent++
				if sent < probes+2 {
					timer.Reset(announceInterval)
				}
			}
		}
	}
}

// send multicasts what build makes on each interface.
func (r *responder) send(build func(Advertisement, []netip.Addr) ([]byte, error)) {
	adv := r.advertisement()
	for _, ifc := range r.t.interfaces() {
		b, err := build(adv, ifc.addrs())
		if err != nil {
			r.log.Warn("discovery: cannot build a packet", "err", err)
			return
		}
		if err := r.t.multicast(b, ifc.Index); err != nil {
			r.log.Debug("discovery: send failed", "interface", ifc.Name, "err", err)
		}
	}
}

func (r *responder) advertisement() Advertisement {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adv
}

func (r *responder) goodbye() {
	if !r.live.Load() || !r.multicasting() {
		return
	}
	r.send(func(a Advertisement, addrs []netip.Addr) ([]byte, error) {
		return response(a, addrs, answer{ptr: true, instance: true, host: true}, 0, nil)
	})
}

// handle answers a query, or notices a conflict in a response; it reports
// a conflict that renamed the advertisement.
func (r *responder) handle(p packet) bool {
	m, err := parseMessage(p.data)
	if err != nil {
		return false
	}
	adv := r.advertisement()
	if m.response {
		return r.conflict(m, adv)
	}
	if !r.live.Load() {
		return false
	}
	var w answer
	unicast := p.src.Port() != Port
	legacy := unicast
	instance, host := adv.instanceFQDN(), adv.hostFQDN()
	for _, q := range m.questions {
		all := q.Type == dnsmessage.TypeALL
		switch {
		case sameName(q.Name, Service) && (q.Type == dnsmessage.TypePTR || all):
			w.ptr = true
		case sameName(q.Name, servicesName) && (q.Type == dnsmessage.TypePTR || all):
			w.services = true
		case sameName(q.Name, instance) && (q.Type == dnsmessage.TypeSRV || q.Type == dnsmessage.TypeTXT || all):
			w.instance = true
		case sameName(q.Name, host) && (q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeAAAA || all):
			w.host = true
		default:
			continue
		}
		if q.Class&unicastResponse != 0 {
			unicast = true
		}
	}
	if w.empty() {
		return false
	}
	addrs := r.t.addrs(p.ifIndex, p.src.Addr())
	var legacyMsg *message
	if legacy {
		if !r.allowLegacy() {
			return false
		}
		legacyMsg = &m
	}
	b, err := response(adv, addrs, w, TTL, legacyMsg)
	if err != nil {
		r.log.Warn("discovery: cannot build an answer", "err", err)
		return false
	}
	if unicast || !r.multicasting() {
		if err := r.t.unicast(b, p.src); err != nil {
			r.log.Debug("discovery: answer failed", "to", p.src.String(), "err", err)
		}
		return false
	}
	key := answerKey{p.ifIndex, w}
	now := time.Now()
	if now.Sub(r.answered[key]) < minAnswerInterval {
		return false
	}
	r.answered[key] = now
	if err := r.t.multicast(b, p.ifIndex); err != nil {
		r.log.Debug("discovery: answer failed", "err", err)
	}
	return false
}

func (r *responder) allowLegacy() bool {
	now := time.Now()
	if now.Sub(r.legacyAt) >= time.Second {
		r.legacyAt, r.legacyN = now, 0
	}
	r.legacyN++
	return r.legacyN <= legacyPerSecond
}

// conflict renames the advertisement when another runtime answers for its
// name. While probing any answer is a conflict; once live, the runtime
// with the greater ID gives way (the other keeps the name), so two never
// both rename.
func (r *responder) conflict(m message, adv Advertisement) bool {
	found, _, _ := m.runtimes(time.Now())
	for _, rec := range found {
		if !strings.EqualFold(rec.Name, adv.Instance) || rec.ID == adv.ID {
			continue
		}
		if r.live.Load() && adv.ID < rec.ID {
			continue
		}
		r.mu.Lock()
		if r.renames >= maxRenames {
			r.mu.Unlock()
			return false
		}
		r.renames++
		r.adv.Instance = renamed(r.base, r.renames+1)
		name := r.adv.Instance
		r.mu.Unlock()
		r.log.Warn("discovery: another runtime uses this name; renamed", "name", name, "other_id", rec.ID)
		return true
	}
	return false
}
