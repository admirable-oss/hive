package web

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestEncryptRFC8291 checks the encryption against RFC 8291, Appendix A.
func TestEncryptRFC8291(t *testing.T) {
	as, err := ecdh.P256().NewPrivateKey(b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encrypt([]byte("When I grow up, I want to be a watermelon"),
		b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		b64(t, "BTBZMqHH6r4Tts7J_aSIgg"), as, b64(t, "DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if g := base64.RawURLEncoding.EncodeToString(got); g != want {
		t.Fatalf("encrypted:\n got %s\nwant %s", g, want)
	}
}

// decryptPush is the browser's side of RFC 8291, for tests.
func decryptPush(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt, idlen := body[:16], int(body[20])
	asPublic, ciphertext := body[21:21+idlen], body[21+idlen:]
	as, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ua.ECDH(as)
	prkKey, _ := hkdf.Extract(sha256.New, shared, auth)
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPublic...)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return bytes.TrimRight(plain[:len(plain)-1], "\x00")
}

func TestSealRoundTripAndVAPID(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(nil)
	auth := []byte("0123456789abcdef")
	body, err := seal([]byte(`{"title":"hi"}`), ua.PublicKey().Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if got := decryptPush(t, body, ua, auth); string(got) != `{"title":"hi"}` {
		t.Fatalf("round trip: %q", got)
	}
	if _, err := seal(make([]byte, maxPayload+1), ua.PublicKey().Bytes(), auth); err == nil {
		t.Fatal("an oversized payload was sealed")
	}

	key, err := loadVAPID(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	h, err := key.authorization("https://fcm.googleapis.com/fcm/send/abc", "mailto:me@example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	verifyVAPID(t, h, "https://fcm.googleapis.com", now)
}

// verifyVAPID checks an Authorization header as a push service would.
func verifyVAPID(t *testing.T, header, aud string, now time.Time) {
	t.Helper()
	rest, ok := strings.CutPrefix(header, "vapid t=")
	if !ok {
		t.Fatalf("header %q", header)
	}
	jwt, k, ok := strings.Cut(rest, ", k=")
	if !ok {
		t.Fatalf("header %q", header)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q", jwt)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != aud || claims.Exp <= now.Unix() || claims.Exp > now.Add(24*time.Hour).Unix() || claims.Sub == "" {
		t.Fatalf("claims %+v", claims)
	}
	pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), b64(t, k))
	if err != nil {
		t.Fatal(err)
	}
	sig := b64(t, parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pk, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the VAPID signature does not verify")
	}
}

func TestPushEndpoints(t *testing.T) {
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/x", "https://updates.push.services.mozilla.com/wpush/v2/x",
		"https://web.push.apple.com/QH", "https://wns2-par02p.notify.windows.com/w/?token=x",
	} {
		if err := checkEndpoint(ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://fcm.googleapis.com/x", "https://evil.example/x", "https://fcm.googleapis.com.evil.example/x",
		"https://169.254.169.254/latest", "file:///etc/passwd", "",
	} {
		if checkEndpoint(bad, false) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if checkEndpoint("http://127.0.0.1:9/x", true) != nil {
		t.Error("allow-any refused a test endpoint")
	}
}
