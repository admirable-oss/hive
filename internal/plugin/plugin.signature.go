package plugin

// Publisher signatures on plugin releases (registry schema 2): SSH
// signatures in OpenSSH's SSHSIG format (PROTOCOL.sshsig), made with
// `ssh-keygen -Y sign -n hive-plugin-release` over a release's statement.
// Publishers sign with ed25519 keys, the default of ssh-keygen and the kind
// Hive's own releases are signed with.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SignatureNamespace is the SSHSIG namespace of plugin release signatures:
// a signature made for anything else (a commit, a file, a Hive release)
// never verifies as one.
const SignatureNamespace = "hive-plugin-release"

const (
	sshsigMagic   = "SSHSIG"
	sshsigArmorIn = "-----BEGIN SSH SIGNATURE-----"
	sshsigArmorEn = "-----END SSH SIGNATURE-----"
	sshEd25519    = "ssh-ed25519"
)

// Errors of signature checks.
var (
	ErrUnsigned     = errors.New("the release is not signed")
	ErrBadSignature = errors.New("the release signature does not verify")
)

// ReleaseStatement is what a publisher signs for a release: the canonical
// JSON (keys sorted, no spaces) of its identity, source and checksum. Every
// field that decides what is installed is in it, so a registry cannot move
// a signed release to other code.
func ReleaseStatement(id string, r Release) []byte {
	// Fields in key order: encoding/json writes them as declared.
	statement := struct {
		Checksum    string `json:"checksum"`
		Commit      string `json:"commit"`
		ID          string `json:"id"`
		PluginAPI   int    `json:"plugin_api"`
		PublishedAt string `json:"published_at"`
		Ref         string `json:"ref"`
		Version     string `json:"version"`
	}{
		Checksum:    strings.ToLower(r.Checksum),
		Commit:      strings.ToLower(r.Commit),
		ID:          id,
		PluginAPI:   r.PluginAPI,
		PublishedAt: r.PublishedAt.UTC().Format(time.RFC3339),
		Ref:         r.Ref,
		Version:     r.Version,
	}
	b, _ := json.Marshal(statement)
	return b
}

// PublicKey is a publisher's ed25519 key, from an authorized_keys line
// ("ssh-ed25519 AAAA… [comment]").
type PublicKey struct {
	key ed25519.PublicKey
}

// ParsePublicKey reads an authorized_keys line holding an ed25519 key.
func ParsePublicKey(line string) (PublicKey, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return PublicKey{}, errors.New("not an SSH public key")
	}
	if fields[0] != sshEd25519 {
		return PublicKey{}, fmt.Errorf("publisher keys must be ed25519, not %s", fields[0])
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return PublicKey{}, fmt.Errorf("SSH public key: %w", err)
	}
	return parseWireKey(blob)
}

func parseWireKey(blob []byte) (PublicKey, error) {
	r := sshReader{b: blob}
	kind, key := r.string(), r.string()
	if r.err != nil || len(r.b) != 0 || string(kind) != sshEd25519 || len(key) != ed25519.PublicKeySize {
		return PublicKey{}, errors.New("not an ed25519 SSH public key")
	}
	return PublicKey{key: ed25519.PublicKey(key)}, nil
}

// wire is the key in the SSH wire format.
func (k PublicKey) wire() []byte {
	var b bytes.Buffer
	sshString(&b, []byte(sshEd25519))
	sshString(&b, k.key)
	return b.Bytes()
}

// Fingerprint is the key's SHA-256 fingerprint as ssh-keygen -l prints it
// ("SHA256:…", unpadded base64).
func (k PublicKey) Fingerprint() string {
	sum := sha256.Sum256(k.wire())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// String is the key as an authorized_keys line.
func (k PublicKey) String() string {
	return sshEd25519 + " " + base64.StdEncoding.EncodeToString(k.wire())
}

// VerifySignature checks an armored SSH signature of message in the plugin
// release namespace, made by one of keys, and returns that key.
func VerifySignature(armored string, message []byte, keys []PublicKey) (PublicKey, error) {
	sig, err := parseSSHSig(armored)
	if err != nil {
		return PublicKey{}, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	if sig.namespace != SignatureNamespace {
		return PublicKey{}, fmt.Errorf("%w: signed for %q, not %q", ErrBadSignature, sig.namespace, SignatureNamespace)
	}
	var signer PublicKey
	for _, k := range keys {
		if bytes.Equal(k.key, sig.key.key) {
			signer = k
			break
		}
	}
	if signer.key == nil {
		return PublicKey{}, fmt.Errorf("%w: signed by %s, which is not one of the publisher's keys", ErrBadSignature, sig.key.Fingerprint())
	}
	var digest []byte
	switch sig.hash {
	case "sha512":
		d := sha512.Sum512(message)
		digest = d[:]
	case "sha256":
		d := sha256.Sum256(message)
		digest = d[:]
	default:
		return PublicKey{}, fmt.Errorf("%w: unsupported hash %q", ErrBadSignature, sig.hash)
	}
	var signed bytes.Buffer
	signed.WriteString(sshsigMagic)
	sshString(&signed, []byte(sig.namespace))
	sshString(&signed, sig.reserved)
	sshString(&signed, []byte(sig.hash))
	sshString(&signed, digest)
	if !ed25519.Verify(sig.key.key, signed.Bytes(), sig.signature) {
		return PublicKey{}, ErrBadSignature
	}
	return signer, nil
}

type sshSig struct {
	key       PublicKey
	namespace string
	reserved  []byte
	hash      string
	signature []byte
}

func parseSSHSig(armored string) (sshSig, error) {
	armored = strings.TrimSpace(armored)
	if !strings.HasPrefix(armored, sshsigArmorIn) || !strings.HasSuffix(armored, sshsigArmorEn) {
		return sshSig{}, errors.New("not an armored SSH signature")
	}
	body := strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimPrefix(armored, sshsigArmorIn), sshsigArmorEn)), "")
	blob, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return sshSig{}, err
	}
	if !bytes.HasPrefix(blob, []byte(sshsigMagic)) {
		return sshSig{}, errors.New("not an SSHSIG signature")
	}
	r := sshReader{b: blob[len(sshsigMagic):]}
	version := r.uint32()
	keyBlob, namespace, reserved, hash, sigBlob := r.string(), r.string(), r.string(), r.string(), r.string()
	if r.err != nil || len(r.b) != 0 {
		return sshSig{}, errors.New("malformed SSHSIG signature")
	}
	if version != 1 {
		return sshSig{}, fmt.Errorf("SSHSIG version %d", version)
	}
	key, err := parseWireKey(keyBlob)
	if err != nil {
		return sshSig{}, err
	}
	sr := sshReader{b: sigBlob}
	kind, raw := sr.string(), sr.string()
	if sr.err != nil || len(sr.b) != 0 || string(kind) != sshEd25519 || len(raw) != ed25519.SignatureSize {
		return sshSig{}, errors.New("not an ed25519 signature")
	}
	return sshSig{key: key, namespace: string(namespace), reserved: reserved, hash: string(hash), signature: raw}, nil
}

// sshString appends an SSH wire string: a big-endian uint32 length, then b.
func sshString(buf *bytes.Buffer, b []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint32(len(b)))
	buf.Write(b)
}

// sshReader reads SSH wire values, remembering the first error.
type sshReader struct {
	b   []byte
	err error
}

func (r *sshReader) uint32() uint32 {
	if r.err != nil || len(r.b) < 4 {
		r.err = errors.New("truncated")
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *sshReader) string() []byte {
	n := r.uint32()
	if r.err != nil || uint64(len(r.b)) < uint64(n) {
		r.err = errors.New("truncated")
		return nil
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s
}
