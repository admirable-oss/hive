package insights

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// dayLayout names a day file: its local date.
const dayLayout = "2006-01-02"

// State is what the service keeps across restarts besides the records.
type State struct {
	Schema  int       `json:"schema"`
	SavedAt time.Time `json:"saved_at"`
	// Open are the spans open when it was saved.
	Open []Span `json:"open,omitempty"`
	// Totals are the highest session totals seen, by agent, session and
	// field: what usage grew from.
	Totals map[string]float64 `json:"totals,omitempty"`
	// Plans are each plan's usage and working time over its life, by
	// environment and kind, for plan budgets.
	Plans map[string]map[string]Amounts `json:"plans,omitempty"`
	// Raised is the highest level raised per budget, window and key;
	// Held and Resumed the holds in force and lifted.
	Raised  map[string]string `json:"raised,omitempty"`
	Held    map[string]bool   `json:"held,omitempty"`
	Resumed map[string]bool   `json:"resumed,omitempty"`
}

func (s *State) init() {
	if s.Totals == nil {
		s.Totals = map[string]float64{}
	}
	if s.Plans == nil {
		s.Plans = map[string]map[string]Amounts{}
	}
	if s.Raised == nil {
		s.Raised = map[string]string{}
	}
	if s.Held == nil {
		s.Held = map[string]bool{}
	}
	if s.Resumed == nil {
		s.Resumed = map[string]bool{}
	}
}

// Store keeps the records, one file of JSON lines per local day, and the
// state:
//
//	<dir>/<YYYY-MM-DD>.jsonl
//	<dir>/state.json
//
// A state file from a newer hive is left alone and never written over; an
// unreadable one is moved aside. Lines of a newer record format, or
// broken ones, are skipped.
type Store struct {
	dir string

	mu       sync.Mutex
	readOnly bool
}

// NewStore keeps insights in dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is where the store keeps its files.
func (s *Store) Dir() string { return s.dir }

func (s *Store) dayFile(day string) string { return filepath.Join(s.dir, day+".jsonl") }

// Append adds records to their days' files.
func (s *Store) Append(recs []Record, loc *time.Location) error {
	if len(recs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	byDay := map[string][]byte{}
	var days []string
	for _, r := range recs {
		r.V = lineVersion
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		day := recordTime(r).In(loc).Format(dayLayout)
		if _, ok := byDay[day]; !ok {
			days = append(days, day)
		}
		byDay[day] = append(append(byDay[day], line...), '\n')
	}
	var errs []error
	for _, day := range days {
		f, err := os.OpenFile(s.dayFile(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := f.Write(byDay[day]); err != nil {
			errs = append(errs, err)
		}
		errs = append(errs, f.Close())
	}
	return errors.Join(errs...)
}

// recordTime is when a record happened: a span's start, usage's and an
// alert's time.
func recordTime(r Record) time.Time {
	switch {
	case r.Span != nil:
		return r.Span.Start
	case r.Usage != nil:
		return r.Usage.Time
	case r.Alert != nil:
		return r.Alert.Time
	}
	return time.Time{}
}

// Read returns the records of the local days from from to to, oldest day
// first, and how many lines it skipped.
func (s *Store) Read(from, to time.Time, loc *time.Location) ([]Record, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		out     []Record
		skipped int
		errs    []error
	)
	first := startOfDay(from.In(loc))
	for d := first; !d.After(to.In(loc)); d = d.AddDate(0, 0, 1) {
		recs, n, err := s.readDay(d.Format(dayLayout))
		skipped += n
		if err != nil {
			errs = append(errs, err)
		}
		out = append(out, recs...)
		if len(out) > MaxRecords {
			return out[:MaxRecords], skipped, errors.Join(append(errs, fmt.Errorf("more than %d records: narrow the range", MaxRecords))...)
		}
	}
	return out, skipped, errors.Join(errs...)
}

func (s *Store) readDay(day string) ([]Record, int, error) {
	f, err := os.Open(s.dayFile(day))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var (
		out     []Record
		skipped int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.V > lineVersion || r.V == 0 || !r.valid() {
			skipped++
			continue
		}
		out = append(out, r)
	}
	return out, skipped, sc.Err()
}

func (r Record) valid() bool {
	switch r.Type {
	case RecordSpan:
		return r.Span != nil
	case RecordUsage:
		return r.Usage != nil
	case RecordAlert:
		return r.Alert != nil
	}
	return false
}

// Days lists the days with records, oldest first.
func (s *Store) Days() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var days []string
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() {
			continue
		}
		if _, err := time.Parse(dayLayout, day); err == nil {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	return days
}

// Prune deletes the day files older than keep days before today, and
// returns them.
func (s *Store) Prune(keep int, today time.Time) []string {
	if keep <= 0 {
		return nil
	}
	cutoff := startOfDay(today).AddDate(0, 0, -keep).Format(dayLayout)
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []string
	for _, day := range s.Days() {
		if day < cutoff && os.Remove(s.dayFile(day)) == nil {
			gone = append(gone, day)
		}
	}
	return gone
}

func (s *Store) statePath() string { return filepath.Join(s.dir, "state.json") }

// LoadState reads the state; none is empty.
func (s *Store) LoadState() (State, error) {
	var st State
	err := jsonfile.Read(s.statePath(), &st)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		st = State{}
		err = nil
	case err != nil:
		backup := s.statePath() + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(s.statePath(), backup); rerr != nil {
			err = fmt.Errorf("%s: %w (and it could not be moved aside: %w)", s.statePath(), err, rerr)
		} else {
			err = fmt.Errorf("%s: %w; moved to %s", s.statePath(), err, backup)
		}
		st = State{}
	default:
		if cerr := jsonfile.CheckSchema(s.statePath(), st.Schema, schema); cerr != nil {
			s.mu.Lock()
			s.readOnly = true
			s.mu.Unlock()
			st, err = State{}, cerr
		}
	}
	st.init()
	return st, err
}

// SaveState writes the state, unless a newer hive's is there.
func (s *Store) SaveState(st State) error {
	s.mu.Lock()
	ro := s.readOnly
	s.mu.Unlock()
	if ro {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	st.Schema = schema
	return jsonfile.Write(s.statePath(), st)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
