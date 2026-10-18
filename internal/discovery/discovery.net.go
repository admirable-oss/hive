package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Interface is a network interface discovery may use.
type Interface struct {
	Name     string         `json:"name"`
	Index    int            `json:"-"`
	Prefixes []netip.Prefix `json:"addresses"`
	// Skipped says why discovery does not use it.
	Skipped string `json:"skipped,omitempty"`
}

func (ifc Interface) addrs() []netip.Addr {
	out := make([]netip.Addr, 0, len(ifc.Prefixes))
	for _, p := range ifc.Prefixes {
		out = append(out, p.Addr())
	}
	return out
}

func (ifc Interface) contains(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	for _, p := range ifc.Prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Interfaces returns the interfaces discovery uses, and those it skips
// with why. named, when not empty, are the only ones used; otherwise every
// interface that is up, multicast and not loopback. Point-to-point links
// (VPN tunnels, mostly) and links with only link-local addresses (such as
// Apple's peer-to-peer Wi-Fi) are used only when named.
func Interfaces(named []string) (use, skipped []Interface, err error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("list network interfaces: %w", err)
	}
	for _, n := range named {
		if !slices.ContainsFunc(all, func(i net.Interface) bool { return i.Name == n }) {
			skipped = append(skipped, Interface{Name: n, Skipped: "no such interface"})
		}
	}
	for _, i := range all {
		ifc := Interface{Name: i.Name, Index: i.Index}
		if as, err := i.Addrs(); err == nil {
			for _, a := range as {
				if n, ok := a.(*net.IPNet); ok {
					if addr, ok := netip.AddrFromSlice(n.IP); ok {
						ones, _ := n.Mask.Size()
						ifc.Prefixes = append(ifc.Prefixes, netip.PrefixFrom(addr.Unmap(), ones))
					}
				}
			}
		}
		ifc.Prefixes = slices.DeleteFunc(ifc.Prefixes, func(p netip.Prefix) bool { return !p.IsValid() })
		wanted := slices.Contains(named, i.Name)
		switch {
		case len(named) > 0 && !wanted:
			continue
		case i.Flags&net.FlagUp == 0:
			ifc.Skipped = "down"
		case i.Flags&net.FlagLoopback != 0 && !wanted:
			continue // not worth reporting
		case i.Flags&net.FlagMulticast == 0:
			ifc.Skipped = "no multicast"
		case i.Flags&net.FlagPointToPoint != 0 && !wanted:
			ifc.Skipped = "point-to-point link (a VPN?); name it in discovery.interfaces to use it"
		case len(ifc.Prefixes) == 0:
			ifc.Skipped = "no addresses"
		case !wanted && !slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return !p.Addr().IsLinkLocalUnicast() }):
			ifc.Skipped = "only link-local addresses (a peer-to-peer link?); name it in discovery.interfaces to use it"
		}
		if ifc.Skipped != "" {
			if wanted || i.Flags&net.FlagUp != 0 {
				skipped = append(skipped, ifc)
			}
			continue
		}
		use = append(use, ifc)
	}
	return use, skipped, nil
}

// packet is a datagram received.
type packet struct {
	data []byte
	// ifIndex is the interface it came in on (0: unknown).
	ifIndex int
	src     netip.AddrPort
}

// transport carries mDNS packets.
type transport interface {
	read() (packet, error)
	// multicast sends to the mDNS group on the interface (0: on each).
	multicast(b []byte, ifIndex int) error
	unicast(b []byte, dst netip.AddrPort) error
	// addrs are the addresses to advertise in answer to a packet that came
	// in on ifIndex from src.
	addrs(ifIndex int, src netip.Addr) []netip.Addr
	// interfaces are those it multicasts on.
	interfaces() []Interface
	close() error
}

// mcast is the mDNS group on some interfaces: one socket per address
// family, bound to port 5353 alongside the system's own responder.
type mcast struct {
	ifaces []Interface
	c4     *ipv4.PacketConn
	c6     *ipv6.PacketConn
	pc4    net.PacketConn
	pc6    net.PacketConn
	ch     chan packet
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex // the outgoing interface is per socket: set, then write
	wg     sync.WaitGroup
}

// errNoInterfaces is returned when no interface can be used.
var errNoInterfaces = errors.New("no network interface to discover on")

// listenMulticast joins the mDNS groups on ifaces. Interfaces that cannot
// join are dropped (with why, in skipped); it fails when none can.
func listenMulticast(ifaces []Interface) (t *mcast, skipped []Interface, err error) {
	if len(ifaces) == 0 {
		return nil, nil, errNoInterfaces
	}
	t = &mcast{ch: make(chan packet, 64), done: make(chan struct{})}
	has := func(ifc Interface, v4 bool) bool {
		return slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return p.Addr().Is4() == v4 })
	}
	// Each socket joins the group on its first interface as it opens, then
	// on the others.
	first4 := slices.IndexFunc(ifaces, func(i Interface) bool { return has(i, true) })
	first6 := slices.IndexFunc(ifaces, func(i Interface) bool { return has(i, false) })
	var errs []error
	if first4 >= 0 {
		ni := &net.Interface{Index: ifaces[first4].Index, Name: ifaces[first4].Name}
		if pc, err := net.ListenMulticastUDP("udp4", ni, net.UDPAddrFromAddrPort(groupV4)); err == nil {
			t.pc4, t.c4 = pc, ipv4.NewPacketConn(pc)
			_ = t.c4.SetMulticastTTL(255)
			_ = t.c4.SetMulticastLoopback(true)
			_ = t.c4.SetControlMessage(ipv4.FlagInterface, true)
		} else {
			errs = append(errs, err)
		}
	}
	if first6 >= 0 {
		ni := &net.Interface{Index: ifaces[first6].Index, Name: ifaces[first6].Name}
		if pc, err := net.ListenMulticastUDP("udp6", ni, net.UDPAddrFromAddrPort(groupV6)); err == nil {
			t.pc6, t.c6 = pc, ipv6.NewPacketConn(pc)
			_ = t.c6.SetMulticastHopLimit(255)
			_ = t.c6.SetMulticastLoopback(true)
			_ = t.c6.SetControlMessage(ipv6.FlagInterface, true)
		} else {
			errs = append(errs, err)
		}
	}
	if t.c4 == nil && t.c6 == nil {
		return nil, nil, fmt.Errorf("listen for mDNS on port %d: %w", Port, cmpErr(errors.Join(errs...), errNoInterfaces))
	}
	for i, ifc := range ifaces {
		ni := &net.Interface{Index: ifc.Index, Name: ifc.Name}
		joined := false
		if t.c4 != nil && has(ifc, true) {
			joined = i == first4 || t.c4.JoinGroup(ni, &net.UDPAddr{IP: groupV4.Addr().AsSlice()}) == nil
		}
		if t.c6 != nil && has(ifc, false) {
			joined = i == first6 || t.c6.JoinGroup(ni, &net.UDPAddr{IP: groupV6.Addr().AsSlice()}) == nil || joined
		}
		if !joined {
			ifc.Skipped = "cannot join the mDNS group"
			skipped = append(skipped, ifc)
			continue
		}
		t.ifaces = append(t.ifaces, ifc)
	}
	if len(t.ifaces) == 0 {
		_ = t.close()
		return nil, skipped, errNoInterfaces
	}
	if t.c4 != nil {
		t.wg.Go(func() {
			buf := make([]byte, maxPacket)
			for {
				n, cm, src, err := t.c4.ReadFrom(buf)
				if err != nil {
					return
				}
				ifIndex := 0
				if cm != nil {
					ifIndex = cm.IfIndex
				}
				t.deliver(buf[:n], ifIndex, src)
			}
		})
	}
	if t.c6 != nil {
		t.wg.Go(func() {
			buf := make([]byte, maxPacket)
			for {
				n, cm, src, err := t.c6.ReadFrom(buf)
				if err != nil {
					return
				}
				ifIndex := 0
				if cm != nil {
					ifIndex = cm.IfIndex
				}
				t.deliver(buf[:n], ifIndex, src)
			}
		})
	}
	return t, skipped, nil
}

func (t *mcast) deliver(b []byte, ifIndex int, src net.Addr) {
	u, ok := src.(*net.UDPAddr)
	if !ok {
		return
	}
	ap := u.AddrPort()
	ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	if ifIndex == 0 {
		// No control messages (Windows): the interface whose subnet holds
		// the sender.
		for _, ifc := range t.ifaces {
			if ifc.contains(ap.Addr()) {
				ifIndex = ifc.Index
				break
			}
		}
	}
	if !slices.ContainsFunc(t.ifaces, func(i Interface) bool { return i.Index == ifIndex }) {
		return // an interface discovery does not use
	}
	select {
	case t.ch <- packet{data: append([]byte(nil), b...), ifIndex: ifIndex, src: ap}:
	case <-t.done:
	default: // a flood: drop it
	}
}

func (t *mcast) read() (packet, error) {
	select {
	case p := <-t.ch:
		return p, nil
	case <-t.done:
		return packet{}, net.ErrClosed
	}
}

func (t *mcast) multicast(b []byte, ifIndex int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []error
	for _, ifc := range t.ifaces {
		if ifIndex != 0 && ifc.Index != ifIndex {
			continue
		}
		ni := &net.Interface{Index: ifc.Index, Name: ifc.Name}
		for _, a := range ifc.Prefixes {
			if a.Addr().Is4() && t.c4 != nil {
				if err := t.c4.SetMulticastInterface(ni); err == nil {
					_, err = t.c4.WriteTo(b, nil, net.UDPAddrFromAddrPort(groupV4))
					errs = append(errs, err)
				}
				break
			}
		}
		if t.c6 != nil && slices.ContainsFunc(ifc.Prefixes, func(p netip.Prefix) bool { return p.Addr().Is6() }) {
			if err := t.c6.SetMulticastInterface(ni); err == nil {
				_, err = t.c6.WriteTo(b, nil, &net.UDPAddr{IP: groupV6.Addr().AsSlice(), Port: Port, Zone: ifc.Name})
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (t *mcast) unicast(b []byte, dst netip.AddrPort) error {
	var err error
	if dst.Addr().Is4() && t.c4 != nil {
		_, err = t.c4.WriteTo(b, nil, net.UDPAddrFromAddrPort(dst))
	} else if t.c6 != nil {
		_, err = t.c6.WriteTo(b, nil, net.UDPAddrFromAddrPort(dst))
	}
	return err
}

func (t *mcast) addrs(ifIndex int, _ netip.Addr) []netip.Addr {
	for _, ifc := range t.ifaces {
		if ifc.Index == ifIndex {
			return ifc.addrs()
		}
	}
	return nil
}

func (t *mcast) interfaces() []Interface { return t.ifaces }

func (t *mcast) close() error {
	t.once.Do(func() {
		close(t.done)
		if t.pc4 != nil {
			_ = t.pc4.Close()
		}
		if t.pc6 != nil {
			_ = t.pc6.Close()
		}
	})
	t.wg.Wait()
	return nil
}

// ucast is a plain UDP socket: a responder on EnvAddr, which answers only
// queries sent to it.
type ucast struct {
	pc   *net.UDPConn
	self []netip.Addr
}

func listenUnicast(addr string) (*ucast, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvAddr, err)
	}
	pc, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(ap))
	if err != nil {
		return nil, err
	}
	t := &ucast{pc: pc}
	if !ap.Addr().IsUnspecified() {
		t.self = []netip.Addr{ap.Addr()}
	}
	return t, nil
}

func (t *ucast) read() (packet, error) {
	buf := make([]byte, maxPacket)
	n, src, err := t.pc.ReadFromUDPAddrPort(buf)
	if err != nil {
		return packet{}, err
	}
	return packet{data: buf[:n], src: netip.AddrPortFrom(src.Addr().Unmap(), src.Port())}, nil
}

func (t *ucast) multicast([]byte, int) error { return nil }

func (t *ucast) unicast(b []byte, dst netip.AddrPort) error {
	_, err := t.pc.WriteToUDPAddrPort(b, dst)
	return err
}

func (t *ucast) addrs(_ int, src netip.Addr) []netip.Addr {
	if len(t.self) > 0 {
		return t.self
	}
	if use, _, err := Interfaces(nil); err == nil {
		for _, ifc := range use {
			if ifc.contains(src) {
				return ifc.addrs()
			}
		}
	}
	return []netip.Addr{src} // asked from this machine
}

func (t *ucast) interfaces() []Interface { return nil }

func (t *ucast) close() error { return t.pc.Close() }

// LocalAddr is where a unicast transport listens (tests use port 0).
func (t *ucast) LocalAddr() netip.AddrPort {
	if u, ok := t.pc.LocalAddr().(*net.UDPAddr); ok {
		return u.AddrPort()
	}
	return netip.AddrPort{}
}

// zoned gives a link-local IPv6 address the zone it was received on.
func zoned(a netip.Addr, ifName string) netip.Addr {
	if a.Is6() && a.IsLinkLocalUnicast() && a.Zone() == "" && ifName != "" {
		return a.WithZone(ifName)
	}
	return a
}

// splitHostPort reads host[:port] with Port as the default port.
func splitHostPort(ctx context.Context, s string) (netip.AddrPort, error) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap, nil
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(host)
	if err != nil {
		ips, rerr := net.DefaultResolver.LookupNetIP(ctx, "ip", s)
		if rerr != nil || len(ips) == 0 {
			return netip.AddrPort{}, fmt.Errorf("%q is not an address (host[:port]): %w", s, cmpErr(rerr, err))
		}
		a = ips[0]
	}
	return netip.AddrPortFrom(a.Unmap(), Port), nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
