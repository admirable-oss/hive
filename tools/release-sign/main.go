// Command release-sign makes Hive's release signing key and signs release
// checksums with it. `hive update` installs only releases whose
// checksums.txt carries a valid checksums.txt.sig for the public key built
// into it (buildinfo.releaseKey).
//
//	go run ./tools/release-sign keygen hive-release.key   # once; prints the public key
//	HIVE_RELEASE_SIGNING_KEY=$(cat hive-release.key) go run ./tools/release-sign sign dist/checksums.txt
//	go run ./tools/release-sign verify <public-key> dist/checksums.txt
//
// Keep the private key out of the repository: store it as the
// HIVE_RELEASE_SIGNING_KEY secret, and the public key as the
// HIVE_RELEASE_PUBLIC_KEY variable, of the release workflow.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release-sign keygen <private-key-file> | sign <file> | verify <public-key> <file>")
	}
	switch args[0] {
	case "keygen":
		if len(args) != 2 {
			return errors.New("usage: release-sign keygen <private-key-file>")
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(args[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv)); err != nil {
			return errors.Join(err, f.Close())
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		return nil
	case "sign":
		if len(args) != 2 {
			return errors.New("usage: release-sign sign <file>")
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("HIVE_RELEASE_SIGNING_KEY")))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return errors.New("HIVE_RELEASE_SIGNING_KEY is not set to a base64 ed25519 private key")
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		sig := ed25519.Sign(ed25519.PrivateKey(raw), data)
		return os.WriteFile(args[1]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
	case "verify":
		if len(args) != 3 {
			return errors.New("usage: release-sign verify <public-key> <file>")
		}
		pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(args[1]))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("not a base64 ed25519 public key")
		}
		data, err := os.ReadFile(args[2])
		if err != nil {
			return err
		}
		sigText, err := os.ReadFile(args[2] + ".sig")
		if err != nil {
			return err
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
		if err != nil || !ed25519.Verify(pub, data, sig) {
			return fmt.Errorf("%s.sig does not verify", args[2])
		}
		fmt.Println("ok")
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}
