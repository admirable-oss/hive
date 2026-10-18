package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/admirable-oss/hive/internal/discovery"
)

// ErrNoIdentity means the host's hive cannot prove an identity: it is
// older than identities.
var ErrNoIdentity = errors.New("hive there is too old to prove its identity")

// Identify asks the host's hive to prove its runtime identity, over ssh
// (`hive machine identity --prove`), and checks the proof. ssh has
// authenticated the host and this user by then; the proof says which Hive
// installation answered.
func (t Target) Identify(ctx context.Context) (discovery.Proof, error) {
	if err := t.validate(); err != nil {
		return discovery.Proof{}, err
	}
	challenge := discovery.NewChallenge()
	script := t.hive() + `; exec "$h" machine identity --json --prove ` + Quote(challenge)
	out, err := t.Shell(ctx, script, nil)
	if err != nil {
		if s := err.Error(); strings.Contains(s, "unknown command") || strings.Contains(s, "unknown flag") {
			return discovery.Proof{}, fmt.Errorf("%w (%s): update it", ErrNoIdentity, t.Host)
		}
		return discovery.Proof{}, err
	}
	var p discovery.Proof
	if err := json.Unmarshal(out, &p); err != nil {
		return discovery.Proof{}, fmt.Errorf("%s: an unreadable identity proof: %w", t.Host, err)
	}
	if err := p.Verify(challenge); err != nil {
		return discovery.Proof{}, fmt.Errorf("%s: %w", t.Host, err)
	}
	return p, nil
}
