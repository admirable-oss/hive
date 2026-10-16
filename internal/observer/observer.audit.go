package observer

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Audit actions.
const (
	AuditCreated      = "created"      // a credential was made
	AuditRevoked      = "revoked"      // a credential was revoked
	AuditConnected    = "connected"    // an observer authenticated
	AuditRefused      = "refused"      // a token was refused: invalid, expired or revoked
	AuditDenied       = "denied"       // an authenticated observer asked for something out of scope
	AuditDisconnected = "disconnected" // an observer's connection ended (or was ended)
)

// Audit log size: each file rotates past auditMaxBytes, keeping
// auditBackups older ones.
const (
	auditMaxBytes = 1 << 20
	auditBackups  = 2
)

// AuditEntry is one line of the audit log. It never holds a token.
type AuditEntry struct {
	Time       time.Time `json:"time"`
	Action     string    `json:"action"`
	Credential string    `json:"credential,omitempty"`
	Name       string    `json:"name,omitempty"`
	// Detail says what was refused or denied, or why a connection ended.
	Detail string `json:"detail,omitempty"`
	// Client is the program the observer says it is (its hello), for
	// telling observers apart; it is not verified.
	Client string `json:"client,omitempty"`
}

// Audit appends observer access to a private, rotated JSON-lines file.
type Audit struct {
	path string
	now  func() time.Time

	mu sync.Mutex
	f  *logging.RotatingFile
}

// OpenAudit opens the audit log in dir.
func OpenAudit(dir string) (*Audit, error) {
	path := filepath.Join(dir, "audit.log")
	f, err := logging.OpenRotating(path, auditMaxBytes, auditBackups)
	if err != nil {
		return nil, err
	}
	return &Audit{path: path, now: time.Now, f: f}, nil
}

// Record appends e, stamped now. Failing to write is not an error for the
// caller: auditing must not take observation (or the daemon) down.
func (a *Audit) Record(e AuditEntry) {
	if a == nil {
		return
	}
	e.Time = a.now().UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.f.Write(append(line, '\n'))
}

// Read returns up to limit of the newest entries, oldest first; with
// credential set, only that credential's.
func (a *Audit) Read(limit int, credential string) ([]AuditEntry, error) {
	if a == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AuditEntry
	for _, p := range []string{a.path + ".2", a.path + ".1", a.path} {
		f, err := os.Open(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var e AuditEntry
			if json.Unmarshal(sc.Bytes(), &e) != nil || (credential != "" && e.Credential != credential) {
				continue
			}
			out = append(out, e)
			if len(out) > limit {
				out = out[1:]
			}
		}
		_ = f.Close()
	}
	return out, nil
}

// Close closes the log.
func (a *Audit) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Close()
}
