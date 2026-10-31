package access

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Audit actions. Access changes are the user's; the others are a
// member's.
const (
	AuditInvited      = "invited"      // a member was let in
	AuditGranted      = "granted"      // a member's role changed somewhere
	AuditRenewed      = "renewed"      // a member's expiry moved
	AuditRevoked      = "revoked"      // a member's access ended
	AuditKeys         = "keys"         // Hive's block in authorized_keys was rewritten, or could not be
	AuditConnected    = "connected"    // a member's connection was accepted
	AuditRefused      = "refused"      // a connection for an unknown, expired or revoked member
	AuditDenied       = "denied"       // a member asked for something it may not do, or cannot see
	AuditAction       = "action"       // a member changed something, or tried to
	AuditDisconnected = "disconnected" // a member's connection ended (or was ended)
)

// Audit log size: each file rotates past auditMaxBytes, keeping
// auditBackups older ones.
const (
	auditMaxBytes = 4 << 20
	auditBackups  = 4
)

// AuditEntry is one line of the audit log. It never holds what a member
// typed.
type AuditEntry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	Member string    `json:"member,omitempty"`
	Name   string    `json:"name,omitempty"`
	// From is where the member's ssh connection came from.
	From string `json:"from,omitempty"`
	// Client is the program the member's side says it is (its hello); it
	// is not verified.
	Client string `json:"client,omitempty"`
	// Method is the call an action or a denial was for.
	Method string `json:"method,omitempty"`
	// Environment is where the call acted, when it named environments.
	Environment string `json:"environment,omitempty"`
	// Outcome is how an action ended: ok, or the error's code.
	Outcome string `json:"outcome,omitempty"`
	// Detail says what changed, what was refused, or why a connection
	// ended.
	Detail string `json:"detail,omitempty"`
}

// Audit appends who did what to a private, rotated JSON-lines file.
type Audit struct {
	path string
	now  func() time.Time

	mu     sync.Mutex
	f      *logging.RotatingFile
	failed error // the last write's, until one succeeds
}

// OpenAudit opens the audit log in dir.
func OpenAudit(dir string) (*Audit, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "audit.log")
	f, err := logging.OpenRotating(path, auditMaxBytes, auditBackups)
	if err != nil {
		return nil, err
	}
	return &Audit{path: path, now: time.Now, f: f}, nil
}

// Record appends e, stamped now. Failing to write is not an error for the
// caller: auditing must not take the daemon down. Err says it happened.
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
	_, a.failed = a.f.Write(append(line, '\n'))
}

// Err is why the last entry could not be written, nil once one is.
func (a *Audit) Err() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failed
}

// Read returns up to limit of the newest entries, oldest first; with
// member set (an ID or a name), only that member's.
func (a *Audit) Read(limit int, member string) ([]AuditEntry, error) {
	if a == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AuditEntry
	for i := auditBackups; i >= 0; i-- {
		p := a.path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", a.path, i)
		}
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
			if json.Unmarshal(sc.Bytes(), &e) != nil || (member != "" && e.Member != member && e.Name != member) {
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
