package discovery

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/net/dns/dnsmessage"
)

func TestMain(m *testing.M) {
	probeInterval, announceInterval = 10*time.Millisecond, 20*time.Millisecond
	goleak.VerifyTestMain(m)
}

func testIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIdentityIsStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidID(a.ID); err != nil {
		t.Fatalf("ID %q: %v", a.ID, err)
	}
	b, err := LoadIdentity(dir)
	if err != nil || b.ID != a.ID || !b.PublicKey.Equal(a.PublicKey) {
		t.Fatalf("reloaded identity differs: %v %v", b, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, IdentityFile))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("identity file mode %v (%v), want 0600", fi.Mode(), err)
		}
	}
	if !strings.HasPrefix(Fingerprint(a.PublicKey), "SHA256:") {
		t.Fatal(Fingerprint(a.PublicKey))
	}
	if err := os.WriteFile(filepath.Join(dir, IdentityFile), []byte(`{"private_key":"short"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir); !errors.Is(err, ErrBadIdentity) {
		t.Fatalf("a damaged identity loads: %v", err)
	}
}

func TestIdentityMadeOnceWhenRacing(t *testing.T) {
	dir := t.TempDir()
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			id, err := LoadIdentity(dir)
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = id.ID
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("racing loads made different identities: %q", ids)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestProof(t *testing.T) {
	id := testIdentity(t)
	c := NewChallenge()
	p, err := id.Prove(c, "laptop", "laptop.example", "darwin/arm64", "1.0.8")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(c); err != nil {
		t.Fatalf("a good proof fails: %v", err)
	}
	if !p.Key().Equal(id.PublicKey) {
		t.Fatal("Key is not the identity's")
	}
	if err := p.Verify(NewChallenge()); !errors.Is(err, ErrBadProof) {
		t.Fatalf("a replayed proof verifies: %v", err)
	}
	tampered := p
	tampered.Platform = "linux/amd64"
	if err := tampered.Verify(c); !errors.Is(err, ErrBadProof) {
		t.Fatalf("a tampered proof verifies: %v", err)
	}
	// Another key cannot claim this ID.
	other := testIdentity(t)
	forged, _ := other.Prove(c, "laptop", "", "darwin/arm64", "1.0.8")
	forged.ID = id.ID
	if err := forged.Verify(c); !errors.Is(err, ErrBadProof) {
		t.Fatalf("a proof by another key verifies: %v", err)
	}
	forged.PublicKey = p.PublicKey
	if err := forged.Verify(c); !errors.Is(err, ErrBadProof) {
		t.Fatalf("a borrowed public key verifies: %v", err)
	}
	if _, err := id.Prove("sign this text, please", "", "", "", ""); !errors.Is(err, ErrBadProof) {
		t.Fatalf("signs arbitrary text: %v", err)
	}
}

func TestInstanceName(t *testing.T) {
	for in, want := range map[string]string{
		"laptop":                 "laptop",
		"my.laptop.local":        "my-laptop-local",
		"  Studio (2) ":          "Studio (2)",
		"ünïcode":                "n-code",
		strings.Repeat("a", 80):  strings.Repeat("a", 63),
		"semi;colon\nnewline":    "semi-colon-newline",
		"...":                    "",
		"GPU box":                "GPU box",
		"under_score-and-dash01": "under_score-and-dash01",
	} {
		if got := InstanceName(in); got != want {
			t.Errorf("InstanceName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := renamed("laptop (2)", 3); got != "laptop (3)" {
		t.Fatal(got)
	}
	if got := renamed(strings.Repeat("a", 63), 2); len(got) != 63 || !strings.HasSuffix(got, " (2)") {
		t.Fatal(got)
	}
}

func testAdvertisement(t *testing.T, name string) Advertisement {
	t.Helper()
	return Advertisement{Instance: name, ID: testIdentity(t).ID, Port: 22, APILevel: 7, Caps: []string{CapSSH}}
}

func TestResponseRoundTrip(t *testing.T) {
	adv := testAdvertisement(t, "GPU box")
	addrs := []netip.Addr{netip.MustParseAddr("fe80::1"), netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("2001:db8::7")}
	b, err := response(adv, addrs, answer{ptr: true}, TTL, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseMessage(b)
	if err != nil || !m.response {
		t.Fatalf("parse: %v", err)
	}
	recs, goodbyes, invalid := m.runtimes(time.Now())
	if len(recs) != 1 || len(goodbyes) != 0 || invalid != 0 {
		t.Fatalf("runtimes %v, goodbyes %v, invalid %d", recs, goodbyes, invalid)
	}
	r := recs[0]
	if r.Name != "GPU box" || r.ID != adv.ID || r.Port != 22 || r.APILevel != 7 || r.Version != Version || !slices.Equal(r.Caps, []string{CapSSH}) {
		t.Fatalf("record %+v", r)
	}
	sortAddrs(r.Addrs)
	if r.Addrs[0].String() != "192.168.1.20" || r.Addrs[2].String() != "fe80::1" {
		t.Fatalf("addresses not best first: %v", r.Addrs)
	}
	if d, _ := r.SSHDestination("me"); d != "me@192.168.1.20" {
		t.Fatal(d)
	}
	r.Port, r.Addrs = 2222, []netip.Addr{netip.MustParseAddr("2001:db8::7")}
	if d, _ := r.SSHDestination(""); d != "ssh://[2001:db8::7]:2222" {
		t.Fatal(d)
	}
	for _, rr := range m.records {
		if _, ok := rr.Body.(*dnsmessage.SRVResource); ok && rr.Header.Class&cacheFlush == 0 {
			t.Error("unique records lack the cache-flush bit")
		}
	}

	// Saying goodbye.
	b, _ = response(adv, addrs, answer{ptr: true, instance: true, host: true}, 0, nil)
	m, _ = parseMessage(b)
	if recs, goodbyes, _ := m.runtimes(time.Now()); len(recs) != 0 || !slices.Contains(goodbyes, "GPU box") {
		t.Fatalf("goodbye: %v %v", recs, goodbyes)
	}
}

func TestLegacyResponse(t *testing.T) {
	adv := testAdvertisement(t, "laptop")
	q, _ := query(4242)
	m, _ := parseMessage(q)
	b, err := response(adv, []netip.Addr{netip.MustParseAddr("10.0.0.2")}, answer{ptr: true}, TTL, &m)
	if err != nil {
		t.Fatal(err)
	}
	var p dnsmessage.Parser
	h, _ := p.Start(b)
	qs, _ := p.AllQuestions()
	if h.ID != 4242 || len(qs) != 1 {
		t.Fatalf("legacy answer: id %d, %d questions", h.ID, len(qs))
	}
	rs, _ := p.AllAnswers()
	for _, r := range rs {
		if r.Header.TTL > uint32(legacyTTL/time.Second) || r.Header.Class&cacheFlush != 0 {
			t.Fatalf("legacy record %v", r.Header)
		}
	}
}

func TestNotAHiveRuntime(t *testing.T) {
	adv := testAdvertisement(t, "laptop")
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	_ = b.StartAnswers()
	inst := dnsmessage.MustNewName(adv.instanceFQDN())
	_ = b.PTRResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(Service), Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
	_ = b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.SRVResource{Port: 22, Target: dnsmessage.MustNewName("x.local.")})
	_ = b.TXTResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.TXTResource{TXT: []string{"v=1", "id=not-an-id"}})
	msg, _ := b.Finish()
	m, _ := parseMessage(msg)
	if recs, _, invalid := m.runtimes(time.Now()); len(recs) != 0 || invalid != 1 {
		t.Fatalf("a bad advertisement was accepted: %v (invalid %d)", recs, invalid)
	}
	if _, err := parseMessage([]byte("hello")); err == nil {
		t.Fatal("garbage parses")
	}
}

// bus is a network in memory: what a member multicasts reaches every
// member (itself too, as multicast loopback does).
type bus struct {
	mu      sync.Mutex
	members []*memConn
}

type memConn struct {
	b    *bus
	addr netip.AddrPort
	ch   chan packet
	done chan struct{}
	once sync.Once
}

func (b *bus) join(addr string) *memConn {
	c := &memConn{b: b, addr: netip.MustParseAddrPort(addr), ch: make(chan packet, 64), done: make(chan struct{})}
	b.mu.Lock()
	b.members = append(b.members, c)
	b.mu.Unlock()
	return c
}

func (c *memConn) read() (packet, error) {
	select {
	case p := <-c.ch:
		return p, nil
	case <-c.done:
		return packet{}, net.ErrClosed
	}
}

func (c *memConn) deliver(p packet) {
	select {
	case c.ch <- p:
	case <-c.done:
	default:
	}
}

func (c *memConn) multicast(data []byte, _ int) error {
	c.b.mu.Lock()
	defer c.b.mu.Unlock()
	for _, m := range c.b.members {
		m.deliver(packet{data: data, ifIndex: 1, src: c.addr})
	}
	return nil
}

func (c *memConn) unicast(data []byte, dst netip.AddrPort) error {
	c.b.mu.Lock()
	defer c.b.mu.Unlock()
	for _, m := range c.b.members {
		if m.addr == dst {
			m.deliver(packet{data: data, ifIndex: 1, src: c.addr})
		}
	}
	return nil
}

func (c *memConn) addrs(int, netip.Addr) []netip.Addr { return []netip.Addr{c.addr.Addr()} }

func (c *memConn) interfaces() []Interface {
	return []Interface{{Name: "mem0", Index: 1, Prefixes: []netip.Prefix{netip.PrefixFrom(c.addr.Addr(), 24)}}}
}

func (c *memConn) close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// collect reads what reaches c for d, as runtimes and goodbyes.
func collect(c *memConn, d time.Duration) (found []Record, goodbyes []string, packets int) {
	deadline := time.After(d)
	for {
		select {
		case p := <-c.ch:
			m, err := parseMessage(p.data)
			if err != nil || !m.response {
				continue
			}
			packets++
			r, g, _ := m.runtimes(time.Now())
			found, goodbyes = append(found, r...), append(goodbyes, g...)
		case <-deadline:
			return found, goodbyes, packets
		}
	}
}

func start(ctx context.Context, t *testing.T, c *memConn, adv Advertisement) (*responder, <-chan error) {
	t.Helper()
	r := newResponder(c, adv, slog.New(slog.DiscardHandler))
	done := make(chan error, 1)
	go func() { done <- r.run(ctx) }()
	return r, done
}

func waitLive(t *testing.T, r *responder) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.live.Load() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the responder never went live")
}

func TestResponderAnnouncesAnswersAndSaysGoodbye(t *testing.T) {
	var lan bus
	client := lan.join("10.0.0.9:5353")
	ctx, cancel := context.WithCancel(context.Background())
	adv := testAdvertisement(t, "laptop")
	r, done := start(ctx, t, lan.join("10.0.0.2:5353"), adv)
	waitLive(t, r)
	found, _, _ := collect(client, 100*time.Millisecond)
	if len(found) == 0 || found[0].ID != adv.ID {
		t.Fatalf("no announcement: %v", found)
	}

	// A multicast question is answered on the group, at most once a second.
	q, _ := query(0)
	_ = client.multicast(q, 1)
	_ = client.multicast(q, 1)
	found, _, packets := collect(client, 100*time.Millisecond)
	if packets != 1 || len(found) != 1 || !slices.Contains(found[0].Addrs, netip.MustParseAddr("10.0.0.2")) {
		t.Fatalf("answers: %d packets, %v", packets, found)
	}

	// A one-shot query from another port gets a unicast answer.
	legacy := lan.join("10.0.0.9:40000")
	_ = legacy.multicast(q, 1)
	if found, _, _ := collect(legacy, 100*time.Millisecond); len(found) != 1 {
		t.Fatalf("no answer to a one-shot query: %v", found)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, goodbyes, _ := collect(client, 100*time.Millisecond); !slices.Contains(goodbyes, "laptop") {
		t.Fatalf("no goodbye: %v", goodbyes)
	}
}

func TestResponderRenamesOnConflict(t *testing.T) {
	var lan bus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, done1 := start(ctx, t, lan.join("10.0.0.2:5353"), testAdvertisement(t, "laptop"))
	waitLive(t, first)
	second, done2 := start(ctx, t, lan.join("10.0.0.3:5353"), testAdvertisement(t, "laptop"))
	waitLive(t, second)
	if first.Name() != "laptop" || second.Name() != "laptop (2)" {
		t.Fatalf("names %q and %q, want laptop and laptop (2)", first.Name(), second.Name())
	}
	cancel()
	<-done1
	<-done2
}

// TestResponderLiveConflict: two runtimes that went live with one name at
// once (a network that just merged) settle it: one keeps it.
func TestResponderLiveConflict(t *testing.T) {
	a, b := testAdvertisement(t, "studio"), testAdvertisement(t, "studio")
	r1 := newResponder(nil, a, slog.New(slog.DiscardHandler))
	r2 := newResponder(nil, b, slog.New(slog.DiscardHandler))
	r1.live.Store(true)
	r2.live.Store(true)
	msg := func(adv Advertisement) message {
		raw, _ := response(adv, nil, answer{ptr: true}, TTL, nil)
		m, _ := parseMessage(raw)
		return m
	}
	c1, c2 := r1.conflict(msg(b), a), r2.conflict(msg(a), b)
	if c1 == c2 {
		t.Fatalf("both or neither renamed: %v %v", c1, c2)
	}
	if r1.conflict(msg(a), a) {
		t.Fatal("its own announcement is a conflict")
	}
}

func TestLegacyAnswersAreBounded(t *testing.T) {
	r := newResponder(nil, Advertisement{}, slog.New(slog.DiscardHandler))
	n := 0
	for range 100 {
		if r.allowLegacy() {
			n++
		}
	}
	if n != legacyPerSecond {
		t.Fatalf("%d answers in a burst, want %d", n, legacyPerSecond)
	}
}

// unicastResponder runs a responder on a loopback port, as EnvAddr makes a
// daemon do.
func unicastResponder(t *testing.T, adv Advertisement) (addr string, stop func()) {
	t.Helper()
	u, err := listenUnicast("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- newResponder(u, adv, slog.New(slog.DiscardHandler)).run(ctx) }()
	return u.LocalAddr().String(), func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestBrowseUnicast(t *testing.T) {
	adv := testAdvertisement(t, "studio")
	addr, stop := unicastResponder(t, adv)
	defer stop()
	other := testAdvertisement(t, "studio")
	addr2, stop2 := unicastResponder(t, other)
	defer stop2()

	res, err := Browse(context.Background(), BrowseConfig{Timeout: 500 * time.Millisecond, Addrs: []string{addr, addr2, addr}, NoMulticast: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Runtimes) != 2 {
		t.Fatalf("found %d runtimes, want 2 (one per identity): %+v", len(res.Runtimes), res)
	}
	for _, r := range res.Runtimes {
		if !r.DuplicateName || r.Name != "studio" {
			t.Errorf("a duplicate name is not marked: %+v", r)
		}
		if a, _ := r.Address(); a.String() != "127.0.0.1" {
			t.Errorf("address %v", r.Addrs)
		}
		if r.ID != adv.ID && r.ID != other.ID {
			t.Errorf("unknown ID %q", r.ID)
		}
	}
}

// TestBrowseWithoutMulticast: nobody answering is an empty result, not an
// error, and says what it tried.
func TestBrowseNobodyAnswers(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	silent := pc.LocalAddr().String()
	defer pc.Close()
	res, err := Browse(context.Background(), BrowseConfig{Timeout: 200 * time.Millisecond, Addrs: []string{silent}, NoMulticast: true})
	if err != nil || len(res.Runtimes) != 0 || res.Answers != 0 || !slices.Contains(res.Asked, silent) {
		t.Fatalf("browse: %+v, %v", res, err)
	}
	if _, err := Browse(context.Background(), BrowseConfig{NoMulticast: true}); !errors.Is(err, errNoInterfaces) {
		t.Fatalf("asking nobody: %v", err)
	}
	if _, err := Browse(context.Background(), BrowseConfig{Addrs: []string{"not an address!"}, NoMulticast: true}); err == nil {
		t.Fatal("a bad address is accepted")
	}
}

func TestInterfacesNamed(t *testing.T) {
	_, skipped, err := Interfaces([]string{"no-such-interface0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0].Skipped != "no such interface" {
		t.Fatalf("skipped %+v", skipped)
	}
	use, _, err := Interfaces(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range use {
		if i.Skipped != "" || len(i.Prefixes) == 0 {
			t.Errorf("unusable interface used: %+v", i)
		}
	}
}

func TestAdvertiseValidates(t *testing.T) {
	err := Advertise(context.Background(), ResponderConfig{Advertisement: Advertisement{Instance: "x", ID: "nope", Port: 22}})
	if err == nil {
		t.Fatal("a bad advertisement is accepted")
	}
	err = Advertise(context.Background(), ResponderConfig{Advertisement: Advertisement{Instance: "a.b", ID: testIdentity(t).ID, Port: 22}})
	if err == nil {
		t.Fatal("a name with a dot is accepted")
	}
}

// TestMulticast runs a responder and a browse on the real network. It
// needs a network with multicast, so it runs only when asked
// (HIVE_MULTICAST_TESTS=1).
func TestMulticast(t *testing.T) {
	if os.Getenv("HIVE_MULTICAST_TESTS") != "1" {
		t.Skip("set HIVE_MULTICAST_TESTS=1 to test on the network")
	}
	adv := testAdvertisement(t, "hive-test-"+ShortID(testIdentity(t).ID))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Advertise(ctx, ResponderConfig{Advertisement: adv}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	res, err := Browse(context.Background(), BrowseConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Runtimes, func(r Record) bool { return r.ID == adv.ID }) {
		t.Fatalf("not found on the network: %+v", res)
	}
}

func FuzzParseMessage(f *testing.F) {
	adv := Advertisement{Instance: "laptop", ID: strings.Repeat("a", 26), Port: 22, APILevel: 7, Caps: []string{CapSSH}}
	for _, w := range []answer{{ptr: true}, {instance: true}, {host: true, services: true}} {
		b, _ := response(adv, []netip.Addr{netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("fe80::2")}, w, TTL, nil)
		f.Add(b)
	}
	q, _ := query(1)
	f.Add(q)
	p, _ := probe(adv)
	f.Add(p)
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := parseMessage(b)
		if err != nil {
			return
		}
		recs, _, _ := m.runtimes(time.Now())
		for _, r := range recs {
			if ValidID(r.ID) != nil {
				t.Fatalf("an invalid ID got through: %q", r.ID)
			}
		}
	})
}
