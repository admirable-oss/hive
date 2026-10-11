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
}

// Target is how to reach m; controlDir is as in Target.
func (m Machine) Target(controlDir string) Target {
	return Target{Host: m.Host, Session: m.Session, HivePath: m.HivePath, ControlDir: controlDir}
}

// Errors of the registry.
var (
	ErrMachineNotFound = errors.New("no such machine")
	ErrMachineExists   = errors.New("a machine with that name exists")
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
