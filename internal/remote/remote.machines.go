package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Machine is a saved remote machine.
type Machine struct {
	Name string `json:"name"`
	// Host, Session and HivePath are as in Target.
	Host     string `json:"host"`
	Session  string `json:"session,omitempty"`
	HivePath string `json:"hive_path,omitempty"`
	// Disabled machines are kept but not connected to (the UI leaves
	// them out).
	Disabled bool      `json:"disabled,omitempty"`
	AddedAt  time.Time `json:"added_at"`

	// ID and PublicKey are the runtime identity pinned when the machine
	// was paired or trusted (see package discovery); empty for a machine
	// saved without one.
	ID        string    `json:"id,omitempty"`
	PublicKey string    `json:"public_key,omitempty"`
	Platform  string    `json:"platform,omitempty"`
	TrustedAt time.Time `json:"trusted_at,omitzero"`
	// Discovered says its address came from local discovery, which keeps
	// it current when the machine moves. ssh then files its host key under
	// HostKeyAlias rather than that address.
	Discovered   bool   `json:"discovered,omitempty"`
	HostKeyAlias string `json:"host_key_alias,omitempty"`
	// IdentityChanged says it proved an identity other than the pinned
	// one. It is not connected to until `hive machine trust` accepts the
	// new identity.
	IdentityChanged bool `json:"identity_changed,omitempty"`
}

// Machine states, from most to least trusted.
const (
	// StateTrusted machines have a pinned identity.
	StateTrusted = "trusted"
	// StateConfigured machines were saved without an identity: ssh alone
	// says who they are.
	StateConfigured = "configured"
	// StateChanged machines proved another identity than the pinned one.
	StateChanged = "changed"
	// StateDisabled machines are kept but not connected to.
	StateDisabled = "disabled"
	// StateDiscovered runtimes were found nearby but are not saved.
	StateDiscovered = "discovered"
)

// State is how far m is trusted.
func (m Machine) State() string {
	switch {
	case m.Disabled:
		return StateDisabled
	case m.IdentityChanged:
		return StateChanged
	case m.ID != "":
		return StateTrusted
	}
	return StateConfigured
}

// Target is how to reach m; controlDir is as in Target.
func (m Machine) Target(controlDir string) Target {
	return Target{Host: m.Host, Session: m.Session, HivePath: m.HivePath, ControlDir: controlDir, HostKeyAlias: m.HostKeyAlias}
}

// HostKeyAlias is the name ssh files a discovered machine's host key
// under: the identity it was paired with, which stays when its address
// changes.
func HostKeyAlias(id string) string { return "hive-" + id }

// Trust pins a proven identity on m.
func (m *Machine) Trust(id, publicKey, platform string) {
	m.ID, m.PublicKey, m.Platform = id, publicKey, platform
	m.TrustedAt = time.Now().UTC().Truncate(time.Second)
	m.IdentityChanged = false
}

// Errors of the registry.
var (
	ErrMachineNotFound = errors.New("no such machine")
	ErrMachineExists   = errors.New("a machine with that name exists")
	ErrAlreadyPaired   = errors.New("that machine is already paired")
	ErrInvalidName     = errors.New("invalid machine name (letters, digits, '.', '_' and '-', up to 64 characters)")
)

var machineName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName checks a machine name.
func ValidName(name string) error {
	if !machineName.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// Registry keeps saved machines in one JSON file (<HIVE_HOME>/machines.json).
type Registry struct {
	path string
	mu   sync.Mutex
}

// NewRegistry returns the registry stored at path.
func NewRegistry(path string) *Registry { return &Registry{path: path} }

type registryFile struct {
	Schema   int       `json:"schema"`
	Machines []Machine `json:"machines"`
}

func (r *Registry) load() ([]Machine, error) {
	var f registryFile
	err := jsonfile.Read(r.path, &f)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.path, err)
	}
	if err := jsonfile.CheckSchema(r.path, f.Schema, 1); err != nil {
		return nil, err
	}
	return f.Machines, nil
}

func (r *Registry) save(ms []Machine) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	slices.SortFunc(ms, func(a, b Machine) int { return strings.Compare(a.Name, b.Name) })
	return jsonfile.Write(r.path, registryFile{Schema: 1, Machines: ms})
}

// List returns every saved machine, by name.
func (r *Registry) List() ([]Machine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ms, err := r.load()
	if ms == nil {
		ms = []Machine{}
	}
	return ms, err
}

// Get returns the machine called name.
func (r *Registry) Get(name string) (Machine, error) {
	ms, err := r.List()
	if err != nil {
		return Machine{}, err
	}
	if i := slices.IndexFunc(ms, func(m Machine) bool { return m.Name == name }); i >= 0 {
		return ms[i], nil
	}
	return Machine{}, fmt.Errorf("%w %q (see `hive machine list`)", ErrMachineNotFound, name)
}

// FindID returns the machine whose pinned identity is id.
func (r *Registry) FindID(id string) (Machine, bool, error) {
	ms, err := r.List()
	if err != nil || id == "" {
		return Machine{}, false, err
	}
	if i := slices.IndexFunc(ms, func(m Machine) bool { return m.ID == id }); i >= 0 {
		return ms[i], true, nil
	}
	return Machine{}, false, nil
}

// Add saves a new machine.
func (r *Registry) Add(m Machine) (Machine, error) {
	if err := ValidName(m.Name); err != nil {
		return Machine{}, err
	}
	if err := (Target{Host: m.Host}).validate(); err != nil {
		return Machine{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ms, err := r.load()
	if err != nil {
		return Machine{}, err
	}
	if slices.ContainsFunc(ms, func(x Machine) bool { return x.Name == m.Name }) {
		return Machine{}, fmt.Errorf("%w: %q", ErrMachineExists, m.Name)
	}
	if i := slices.IndexFunc(ms, func(x Machine) bool { return m.ID != "" && x.ID == m.ID }); i >= 0 {
		return Machine{}, fmt.Errorf("%w: it is saved as %q", ErrAlreadyPaired, ms[i].Name)
	}
	if m.AddedAt.IsZero() {
		m.AddedAt = time.Now()
	}
	return m, r.save(append(ms, m))
}

// Update changes the machine called name.
func (r *Registry) Update(name string, change func(*Machine) error) (Machine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ms, err := r.load()
	if err != nil {
		return Machine{}, err
	}
	i := slices.IndexFunc(ms, func(m Machine) bool { return m.Name == name })
	if i < 0 {
		return Machine{}, fmt.Errorf("%w %q", ErrMachineNotFound, name)
	}
	m := ms[i]
	if err := change(&m); err != nil {
		return Machine{}, err
	}
	if m.Name != name {
		if err := ValidName(m.Name); err != nil {
			return Machine{}, err
		}
		if slices.ContainsFunc(ms, func(x Machine) bool { return x.Name == m.Name }) {
			return Machine{}, fmt.Errorf("%w: %q", ErrMachineExists, m.Name)
		}
	}
	ms[i] = m
	return m, r.save(ms)
}

// Remove forgets a machine.
func (r *Registry) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ms, err := r.load()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ms, func(m Machine) bool { return m.Name == name })
	if i < 0 {
		return fmt.Errorf("%w %q", ErrMachineNotFound, name)
	}
	return r.save(slices.Delete(ms, i, i+1))
}
