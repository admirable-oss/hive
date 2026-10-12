// Command nixhash prints the vendorHash flake.nix needs: the Nix hash (NAR,
// sha256, SRI form) of the module's `go mod vendor` output, which is what
// buildGoModule fetches. Run it after changing go.mod:
//
//	go run ./tools/nixhash
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

func main() {
	sum, err := VendorHash(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "nixhash:", err)
		os.Exit(1)
	}
	fmt.Println(sum)
}

// VendorHash vendors the module in dir into a temporary directory and
// returns its NAR hash.
func VendorHash(dir string) (string, error) {
	tmp, err := os.MkdirTemp("", "nixhash")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "vendor")
	cmd := exec.Command("go", "mod", "vendor", "-o", out)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go mod vendor: %w\n%s", err, b)
	}
	return NARHash(out)
}

// NARHash is the sha256 of path's Nix archive serialization, in SRI form.
func NARHash(path string) (string, error) {
	h := sha256.New()
	w := &nar{h: h}
	w.str("nix-archive-1")
	if err := w.node(path); err != nil {
		return "", err
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

type nar struct{ h hash.Hash }

// str writes a NAR string: its length (uint64, little-endian), its bytes,
// and zero padding to a multiple of 8.
func (n *nar) str(s string) { n.bytes([]byte(s)) }

func (n *nar) bytes(b []byte) {
	var l [8]byte
	binary.LittleEndian.PutUint64(l[:], uint64(len(b)))
	n.h.Write(l[:])
	n.h.Write(b)
	if pad := (8 - len(b)%8) % 8; pad > 0 {
		n.h.Write(make([]byte, pad))
	}
}

func (n *nar) node(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	n.str("(")
	n.str("type")
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		n.str("symlink")
		n.str("target")
		n.str(target)
	case info.IsDir():
		n.str("directory")
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		sort.Strings(names) // byte order, as Nix sorts
		for _, name := range names {
			n.str("entry")
			n.str("(")
			n.str("name")
			n.str(name)
			n.str("node")
			if err := n.node(filepath.Join(path, name)); err != nil {
				return err
			}
			n.str(")")
		}
	default:
		n.str("regular")
		if info.Mode()&0o111 != 0 {
			n.str("executable")
			n.str("")
		}
		n.str("contents")
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		var l [8]byte
		binary.LittleEndian.PutUint64(l[:], uint64(info.Size()))
		n.h.Write(l[:])
		if _, err := io.Copy(n.h, f); err != nil {
			return err
		}
		if pad := (8 - info.Size()%8) % 8; pad > 0 {
			n.h.Write(make([]byte, pad))
		}
	}
	n.str(")")
	return nil
}
