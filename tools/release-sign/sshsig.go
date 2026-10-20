package main

// SSH signatures (the SSHSIG format of OpenSSH's PROTOCOL.sshsig), made with
// the release key so the installers can check a release with
// `ssh-keygen -Y verify`, which ships with OpenSSH on macOS, Linux and
// Windows. The key is the same ed25519 key that signs checksums.txt.sig.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// Namespace is the SSHSIG namespace of hive release signatures: a signature
// made for anything else never verifies as one.
const Namespace = "hive-release"

const sshsigMagic = "SSHSIG"

// sshString appends an SSH wire string: a big-endian uint32 length, then b.
func sshString(buf *bytes.Buffer, b []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint32(len(b)))
	buf.Write(b)
}

// sshPublicKey is pub in the SSH wire format (ssh-ed25519).
func sshPublicKey(pub ed25519.PublicKey) []byte {
	var b bytes.Buffer
	sshString(&b, []byte("ssh-ed25519"))
	sshString(&b, pub)
	return b.Bytes()
}

// AuthorizedKey is pub as a line of authorized_keys or allowed_signers.
func AuthorizedKey(pub ed25519.PublicKey) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshPublicKey(pub))
}

// AllowedSigners is the allowed_signers line `ssh-keygen -Y verify -I
// hive-release -n hive-release` needs to accept release signatures.
func AllowedSigners(pub ed25519.PublicKey) string {
	return Namespace + ` namespaces="` + Namespace + `" ` + AuthorizedKey(pub)
}

// SSHSign returns an armored SSH signature of data in the hive-release
// namespace.
func SSHSign(priv ed25519.PrivateKey, data []byte) []byte {
	digest := sha512.Sum512(data)
	var signed bytes.Buffer
	signed.WriteString(sshsigMagic)
	sshString(&signed, []byte(Namespace))
	sshString(&signed, nil) // reserved
	sshString(&signed, []byte("sha512"))
	sshString(&signed, digest[:])

	var sig bytes.Buffer
	sshString(&sig, []byte("ssh-ed25519"))
	sshString(&sig, ed25519.Sign(priv, signed.Bytes()))

	var blob bytes.Buffer
	blob.WriteString(sshsigMagic)
	_ = binary.Write(&blob, binary.BigEndian, uint32(1)) // version
	sshString(&blob, sshPublicKey(priv.Public().(ed25519.PublicKey)))
	sshString(&blob, []byte(Namespace))
	sshString(&blob, nil)
	sshString(&blob, []byte("sha512"))
	sshString(&blob, sig.Bytes())

	enc := base64.StdEncoding.EncodeToString(blob.Bytes())
	var out strings.Builder
	out.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(enc) > 70 {
		out.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	out.WriteString(enc + "\n-----END SSH SIGNATURE-----\n")
	return []byte(out.String())
}
