package discovery

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IdentityFile is the runtime identity's file in <HIVE_HOME>. It holds a
// private key: it is written 0600 and never leaves the machine.
const IdentityFile = "identity.json"

// Identity is a Hive installation's stable identity: an ed25519 key pair
// made the first time it is needed. Its ID is derived from the public key,
// so it does not change with the machine's address or name, and only the
// holder of the private key can prove it (Prove).
type Identity struct {
	ID        string
	PublicKey ed25519.PublicKey
	private   ed25519.PrivateKey
	Created   time.Time
}

type identityFile struct {
	Schema     int       `json:"schema"`
	PrivateKey string    `json:"private_key"` // base64 ed25519 seed
	Created    time.Time `json:"created"`
}

// Errors of identities.
var (
	ErrBadProof      = errors.New("the identity proof does not verify")
	ErrBadIdentity   = errors.New("invalid identity")
	errBadIdentityID = errors.New("an identity ID is 26 base32 characters")
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// IdentityID is the ID of a public key: the first 128 bits of its SHA-256,
// in lowercase base32 (26 characters).
func IdentityID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(idEncoding.EncodeToString(sum[:16]))
}

// ValidID checks the form of an identity ID.
func ValidID(id string) error {
	if len(id) != 26 {
		return errBadIdentityID
	}
	if _, err := idEncoding.DecodeString(strings.ToUpper(id)); err != nil || strings.ToLower(id) != id {
		return errBadIdentityID
	}
	return nil
}

// Fingerprint is the public key's SHA-256 in the form ssh prints host keys
// (SHA256:…), for people to compare out of band.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// ShortID is the start of an identity ID, as tables show it.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// LoadIdentity reads the identity in dir, making it on first use.
func LoadIdentity(dir string) (*Identity, error) {
	path := filepath.Join(dir, IdentityFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return createIdentity(dir)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f identityFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", path, ErrBadIdentity, err)
	}
	seed, err := base64.StdEncoding.DecodeString(f.PrivateKey)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s: %w: bad private key", path, ErrBadIdentity)
	}
	return newIdentity(ed25519.NewKeyFromSeed(seed), f.Created), nil
}

func createIdentity(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id := newIdentity(priv, time.Now().UTC().Truncate(time.Second))
	data, err := json.MarshalIndent(identityFile{
		Schema: 1, PrivateKey: base64.StdEncoding.EncodeToString(priv.Seed()), Created: id.Created,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".identity-*") // 0600
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		return nil, errors.Join(err, tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return nil, errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	// A link fails when the file exists: when another hive made one at the
	// same moment, its identity wins and this one is dropped.
	path := filepath.Join(dir, IdentityFile)
	if err := os.Link(tmp.Name(), path); errors.Is(err, fs.ErrExist) {
		return LoadIdentity(dir)
	} else if err != nil {
		return nil, err
	}
	return id, nil
}

func newIdentity(priv ed25519.PrivateKey, created time.Time) *Identity {
	pub, _ := priv.Public().(ed25519.PublicKey)
	return &Identity{ID: IdentityID(pub), PublicKey: pub, private: priv, Created: created}
}

// Proof is what a runtime answers to a challenge: who it is, signed with
// its identity key. A proof is only as good as the challenge is fresh, so
// the verifier makes the challenge (NewChallenge).
type Proof struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"` // base64
	// Name, Hostname, Platform and Version describe the runtime; they are
	// signed too, so they cannot be swapped in transit.
	Name      string `json:"name"`
	Hostname  string `json:"hostname,omitempty"`
	Platform  string `json:"platform"`
	Version   string `json:"version"`
	Challenge string `json:"challenge"`
	Signature string `json:"signature"` // base64
}

// proofContext separates these signatures from any other use of the key.
const proofContext = "hive-identity-proof-v1"

func (p Proof) signed() []byte {
	return []byte(strings.Join([]string{proofContext, p.ID, p.PublicKey, p.Name, p.Hostname, p.Platform, p.Version, p.Challenge}, "\x00"))
}

// NewChallenge returns a fresh random challenge.
func NewChallenge() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// validChallenge accepts what NewChallenge makes (and other hex of similar
// size), so a prover never signs arbitrary text.
func validChallenge(c string) bool {
	if len(c) < 32 || len(c) > 128 {
		return false
	}
	_, err := hex.DecodeString(c)
	return err == nil
}

// Prove answers challenge.
func (id *Identity) Prove(challenge, name, hostname, platform, version string) (Proof, error) {
	if !validChallenge(challenge) {
		return Proof{}, fmt.Errorf("%w: the challenge must be 32 to 128 hex digits", ErrBadProof)
	}
	p := Proof{
		ID: id.ID, PublicKey: base64.StdEncoding.EncodeToString(id.PublicKey),
		Name: name, Hostname: hostname, Platform: platform, Version: version, Challenge: challenge,
	}
	p.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(id.private, p.signed()))
	return p, nil
}

// Verify checks that p answers challenge, is signed by its key, and that
// its ID is that key's.
func (p Proof) Verify(challenge string) error {
	pub, err := base64.StdEncoding.DecodeString(p.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: bad public key", ErrBadProof)
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		return fmt.Errorf("%w: bad signature", ErrBadProof)
	}
	switch {
	case p.Challenge != challenge:
		return fmt.Errorf("%w: it answers another challenge", ErrBadProof)
	case IdentityID(pub) != p.ID:
		return fmt.Errorf("%w: its ID is not its key's", ErrBadProof)
	case !ed25519.Verify(pub, p.signed(), sig):
		return fmt.Errorf("%w: bad signature", ErrBadProof)
	}
	return nil
}

// Key is the proof's public key (after Verify).
func (p Proof) Key() ed25519.PublicKey {
	pub, _ := base64.StdEncoding.DecodeString(p.PublicKey)
	return pub
}
