package access

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// keyTypes are the ssh public key types a member's key may have.
var keyTypes = []string{
	"ssh-ed25519", "ssh-rsa",
	"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
	"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com",
}

// PublicKey is an ssh public key as authorized_keys holds it.
type PublicKey struct {
	Type    string
	Blob    string // base64, as written
	Comment string
}

// String is "<type> <base64>", without the comment.
func (k PublicKey) String() string { return k.Type + " " + k.Blob }

// Fingerprint is the key's SHA-256 fingerprint, as ssh-keygen -l prints it.
func (k PublicKey) Fingerprint() string {
	raw, _ := base64.StdEncoding.DecodeString(k.Blob)
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// ParseKey reads one public key: "<type> <base64> [comment]", as in a
// .pub file. Options (as in authorized_keys lines) are refused.
func ParseKey(s string) (PublicKey, error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) < 2 {
		return PublicKey{}, fmt.Errorf("%w: not an ssh public key (want \"<type> <base64> [comment]\", as in a .pub file)", ErrInvalid)
	}
	k := PublicKey{Type: fields[0], Blob: fields[1], Comment: strings.Join(fields[2:], " ")}
	if !slices.Contains(keyTypes, k.Type) {
		return PublicKey{}, fmt.Errorf("%w: key type %q (one of %s)", ErrInvalid, k.Type, strings.Join(keyTypes, ", "))
	}
	raw, err := base64.StdEncoding.DecodeString(k.Blob)
	if err != nil {
		return PublicKey{}, fmt.Errorf("%w: the key is not base64", ErrInvalid)
	}
	// The blob starts with its own type, as a length-prefixed string.
	if len(raw) < 4 {
		return PublicKey{}, fmt.Errorf("%w: the key is truncated", ErrInvalid)
	}
	n := binary.BigEndian.Uint32(raw)
	if uint64(n) > uint64(len(raw)-4) || string(raw[4:4+n]) != k.Type {
		return PublicKey{}, fmt.Errorf("%w: the key's contents are not a %s key", ErrInvalid, k.Type)
	}
	return k, nil
}

// Managed block markers in authorized_keys.
const (
	blockBegin = "# >>> hive access (managed by hive; do not edit) >>>"
	blockEnd   = "# <<< hive access <<<"
)

// AuthorizedLine is the authorized_keys line that lets m in: its key,
// forced to run proxy (a shell command ending in `--member`) with its ID,
// with every forwarding and the pty turned off.
func AuthorizedLine(proxy string, m Member) string {
	cmd := strings.TrimSpace(proxy) + " " + m.ID
	cmd = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cmd)
	// Escaped for authorized_keys by hand: %q would quote it as Go does.
	return `command="` + cmd + `",restrict ` + m.Key + " hive:" + m.Name
}

// KeysFile is an authorized_keys file and the block Hive manages in it.
type KeysFile struct {
	Path string
}

// Read returns the lines inside Hive's block, and the keys of every line
// outside it.
func (f KeysFile) Read() (managed []string, others []PublicKey, err error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	in := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == blockBegin:
			in = true
		case line == blockEnd:
			in = false
		case in && line != "" && !strings.HasPrefix(line, "#"):
			managed = append(managed, line)
		case !in && line != "" && !strings.HasPrefix(line, "#"):
			if k, ok := lineKey(line); ok {
				others = append(others, k)
			}
		}
	}
	return managed, others, sc.Err()
}

// lineKey finds the key in an authorized_keys line, after any options.
func lineKey(line string) (PublicKey, bool) {
	fields := strings.Fields(line)
	for i := range fields {
		if slices.Contains(keyTypes, fields[i]) && i+1 < len(fields) {
			if k, err := ParseKey(strings.Join(fields[i:], " ")); err == nil {
				return k, true
			}
		}
	}
	return PublicKey{}, false
}

// ErrBrokenBlock is a file whose Hive block was edited by hand: its
// markers are missing, repeated or out of order. Hive leaves such a file
// alone rather than guess which lines are its own.
var ErrBrokenBlock = errors.New("access: the hive access block in authorized_keys was edited by hand (fix or remove its markers)")

func checkMarkers(data string) error {
	depth, blocks := 0, 0
	for line := range strings.SplitSeq(data, "\n") {
		switch strings.TrimSpace(line) {
		case blockBegin:
			depth++
			blocks++
		case blockEnd:
			depth--
		}
		if depth < 0 || depth > 1 || blocks > 1 {
			return ErrBrokenBlock
		}
	}
	if depth != 0 {
		return ErrBrokenBlock
	}
	return nil
}

// Write replaces Hive's block with lines, keeping every other line as it
// is. With no lines the block is removed. The file and its directory are
// made private if they do not exist.
func (f KeysFile) Write(lines []string) error {
	data, err := os.ReadFile(f.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := checkMarkers(string(data)); err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	var out []string
	in, placed := false, false
	block := func() {
		if placed || len(lines) == 0 {
			placed = true
			return
		}
		out = append(out, blockBegin)
		out = append(out, lines...)
		out = append(out, blockEnd)
		placed = true
	}
	if len(data) > 0 {
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			switch strings.TrimSpace(line) {
			case blockBegin:
				in = true
				block()
				continue
			case blockEnd:
				in = false
				continue
			}
			if !in {
				out = append(out, line)
			}
		}
	}
	block()
	text := strings.Join(out, "\n")
	if text != "" {
		text += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(f.Path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".authorized_keys.hive-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}
