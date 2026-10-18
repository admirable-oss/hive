// Package discovery finds Hive runtimes on the local network, and lets
// them prove who they are.
//
// A daemon that opts in advertises itself with multicast DNS (DNS-SD,
// service _hive._tcp.local): its discovery name, its identity ID, the port
// ssh listens on, its API level and capabilities, and the addresses of the
// interface the question came in on. Nothing else: no user names, sessions,
// agents or environment. An advertisement opens no port and grants nothing;
// Hive is still reached over SSH, as before.
//
// An advertisement is a claim anyone on the network can make. Trust comes
// after it, from the runtime's identity (Identity): an ed25519 key pair whose
// ID the advertisement carries, and which only the runtime can prove, by
// signing a fresh challenge over an authenticated SSH connection (Proof).
// Pairing pins that identity; a later proof by another key is an identity
// change, which needs the user's confirmation again.
package discovery

import (
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Service is the DNS-SD service type Hive advertises.
const Service = "_hive._tcp.local."

// servicesName lists service types (DNS-SD service enumeration).
const servicesName = "_services._dns-sd._udp.local."

// Version is the advertisement's format (its TXT v= key).
const Version = 1

// Port is the mDNS port.
const Port = 5353

// The mDNS groups.
var (
	groupV4 = netip.MustParseAddrPort("224.0.0.251:5353")
	groupV6 = netip.MustParseAddrPort("[ff02::fb]:5353")
)

// TTL is how long an advertisement's records may be cached (RFC 6762
// recommends 120 s for records naming a host).
const TTL = 120 * time.Second

// legacyTTL bounds the TTL of answers to one-shot (legacy unicast) queries.
const legacyTTL = 10 * time.Second

// EnvAddr makes a daemon answer unicast queries on this address only
// (host:port) instead of multicast, for tests and networks without
// multicast.
const EnvAddr = "HIVE_DISCOVERY_ADDR"

// CapSSH says the runtime is reached over ssh on the advertised port.
const CapSSH = "ssh"

// Advertisement is what a runtime says about itself.
type Advertisement struct {
	// Instance is the discovery name ("laptop"); see InstanceName.
	Instance string
	// ID is the runtime's identity ID.
	ID string
	// Port is the port ssh listens on there.
	Port int
	// APILevel is the runtime's protocol API level.
	APILevel int
	// Caps are what it offers (CapSSH).
	Caps []string
}

func (a Advertisement) instanceFQDN() string { return a.Instance + "." + Service }

// hostFQDN is the host name the advertisement's addresses are served under:
// its own, so it never clashes with the system's mDNS host name.
func (a Advertisement) hostFQDN() string { return "hive-" + ShortID(a.ID) + ".local." }

func (a Advertisement) txt() []string {
	t := []string{"v=" + strconv.Itoa(Version), "id=" + a.ID}
	if a.APILevel > 0 {
		t = append(t, "api="+strconv.Itoa(a.APILevel))
	}
	if len(a.Caps) > 0 {
		t = append(t, "caps="+strings.Join(a.Caps, ","))
	}
	return t
}

func (a Advertisement) validate() error {
	if err := ValidID(a.ID); err != nil {
		return err
	}
	if a.Instance == "" || len(a.Instance) > 63 || a.Instance != InstanceName(a.Instance) {
		return fmt.Errorf("invalid discovery name %q", a.Instance)
	}
	if a.Port < 1 || a.Port > 65535 {
		return fmt.Errorf("invalid port %d", a.Port)
	}
	return nil
}

// Record is a runtime a browser found.
type Record struct {
	// Name is its discovery name.
	Name string `json:"name"`
	// ID is the identity ID it advertises (a claim until proven).
	ID string `json:"id"`
	// Host is the .local name it serves its addresses under.
	Host string `json:"host"`
	// Port is where ssh listens.
	Port int `json:"port"`
	// Addrs are its addresses, best first (see Address).
	Addrs []netip.Addr `json:"addresses"`
	// Version is the advertisement format; APILevel and Caps are as in
	// Advertisement.
	Version  int      `json:"version"`
	APILevel int      `json:"api_level,omitempty"`
	Caps     []string `json:"capabilities,omitempty"`
	// Interface is the local interface the answer came in on, when known.
	Interface string `json:"interface,omitempty"`
	// DuplicateName says another runtime advertises the same name.
	DuplicateName bool `json:"duplicate_name,omitempty"`
	// Seen is when it last answered.
	Seen time.Time `json:"seen"`

	ttl time.Duration
}

// Address is the address to connect to: IPv4 first (it needs no zone),
// then global IPv6, then link-local IPv6 with its zone.
func (r Record) Address() (netip.Addr, bool) {
	if len(r.Addrs) == 0 {
		return netip.Addr{}, false
	}
	return r.Addrs[0], true
}

// SSHDestination is how ssh reaches r as user ("" for ssh's default).
func (r Record) SSHDestination(user string) (string, bool) {
	addr, ok := r.Address()
	if !ok {
		return "", false
	}
	if user != "" {
		user += "@"
	}
	if r.Port == 0 || r.Port == 22 {
		return user + addr.String(), true
	}
	host := addr.String()
	if addr.Is6() {
		host = "[" + host + "]"
	}
	return "ssh://" + user + host + ":" + strconv.Itoa(r.Port), true
}

func sortAddrs(addrs []netip.Addr) {
	rank := func(a netip.Addr) int {
		switch {
		case a.Is4() && !a.IsLinkLocalUnicast():
			return 0
		case a.Is4():
			return 1
		case !a.IsLinkLocalUnicast():
			return 2
		}
		return 3
	}
	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return a.Compare(b)
	})
}

// InstanceName turns a name into a discovery name: letters, digits,
// spaces, '-', '_', '(' and ')', up to 63 bytes; other characters (dots
// among them, which DNS would read as labels) become '-'.
func InstanceName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)), strings.ContainsRune(" -_()", r):
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 63 {
			break
		}
	}
	s := strings.Trim(b.String(), " -")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// DefaultName is the discovery name unless configured: the host name's
// first label.
func DefaultName() string {
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	if n := InstanceName(h); n != "" {
		return n
	}
	return "hive"
}

// renamed is name with a number for a conflict: "laptop (2)".
func renamed(name string, n int) string {
	suffix := " (" + strconv.Itoa(n) + ")"
	if base, _, ok := strings.Cut(name, " ("); ok && strings.HasSuffix(name, ")") {
		name = base
	}
	if len(name)+len(suffix) > 63 {
		name = name[:63-len(suffix)]
	}
	return name + suffix
}
