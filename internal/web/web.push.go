package web

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Web Push: the web observer notifies phones and browsers itself, with no
// relay run by anyone else. Each server has its own VAPID key pair (RFC
// 8292) and sends to the browser vendor's push service, which forwards to
// the device; payloads are encrypted for the device (RFC 8291), so the push
// service cannot read them. Everything here is the standard library.

// pushKeyFile holds the server's VAPID private key.
const pushKeyFile = "vapid.key"

// vapidKey is the server's VAPID identity.
type vapidKey struct {
	priv *ecdsa.PrivateKey
	pub  []byte // uncompressed P-256 point, as browsers want it
}

// loadVAPID reads the key in dir, making one on first use (0600).
func loadVAPID(dir string) (*vapidKey, error) {
	path := filepath.Join(dir, pushKeyFile)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		scalar, err := k.Bytes()
		if err != nil {
			return nil, err
		}
		raw = []byte(base64.RawURLEncoding.EncodeToString(scalar))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}
	scalar, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return vapidFromScalar(scalar)
}

func vapidFromScalar(scalar []byte) (*vapidKey, error) {
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), scalar)
	if err != nil {
		return nil, fmt.Errorf("web push: VAPID key: %w", err)
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	return &vapidKey{priv: priv, pub: pub}, nil
}

// PublicKey is what browsers subscribe with (applicationServerKey).
func (k *vapidKey) PublicKey() string { return base64.RawURLEncoding.EncodeToString(k.pub) }

// authorization is the VAPID Authorization header for a push endpoint:
// a JWT (ES256) for its origin, valid for 12 hours, naming contact.
func (k *vapidKey) authorization(endpoint, contact string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": contact})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + enc.EncodeToString(sig) + ", k=" + k.PublicKey(), nil
}

// recordSize is the RFC 8188 record size advertised; a push message is one
// record.
const recordSize = 4096

// maxPayload keeps an encrypted message well inside every push service's
// limit (4 KB).
const maxPayload = 3000

// seal encrypts plaintext for a subscription with a fresh key and salt.
func seal(plaintext, uaPublic, authSecret []byte) ([]byte, error) {
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(plaintext, uaPublic, authSecret, as, salt)
}

// encrypt seals plaintext for a subscription (RFC 8291, aes128gcm) with
// the ephemeral key as and salt given (tests pass the RFC's).
func encrypt(plaintext, uaPublic, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > maxPayload {
		return nil, fmt.Errorf("web push: payload of %d bytes is too large", len(plaintext))
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("web push: subscription key: %w", err)
	}
	if len(authSecret) != 16 || len(salt) != 16 {
		return nil, errors.New("web push: auth secret and salt are 16 bytes")
	}
	shared, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := as.PublicKey().Bytes()

	keyInfo := append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...)
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: the plaintext, then the last-record delimiter.
	padded := append(append([]byte(nil), plaintext...), 0x02)
	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(recordSize))
	out.WriteByte(byte(len(asPublic)))
	out.Write(asPublic)
	out.Write(gcm.Seal(nil, nonce, padded, nil))
	return out.Bytes(), nil
}

// pushEndpointHosts are the push services browsers use; a subscription
// elsewhere is refused unless configured otherwise, so the server cannot be
// made to post to arbitrary addresses.
var pushEndpointHosts = []string{
	"fcm.googleapis.com", "android.googleapis.com", // Chrome, Edge on Android, Samsung Internet, Opera
	"push.services.mozilla.com", // Firefox (updates.push.services.mozilla.com)
	"web.push.apple.com",        // Safari, iOS and iPadOS
	"notify.windows.com",        // Edge on Windows (*.notify.windows.com)
}

// checkEndpoint refuses endpoints that are not HTTPS push services.
func checkEndpoint(endpoint string, allowAny bool) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: push endpoint", ErrInvalid)
	}
	if allowAny {
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("%w: push endpoint scheme", ErrInvalid)
		}
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: push endpoints are HTTPS", ErrInvalid)
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushEndpointHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is not a known push service (set web.push_allow_any_endpoint to allow it)", ErrInvalid, host)
}

// pushMessage is one notification to send.
type pushMessage struct {
	Payload []byte
	TTL     time.Duration
	Urgent  bool
	Topic   string // replaces an undelivered message with the same topic
}

// errGone means the subscription no longer exists: forget it.
var errGone = errors.New("web push: subscription gone")

// pusher sends Web Push messages.
type pusher struct {
	key     *vapidKey
	contact string
	client  *http.Client
	now     func() time.Time
}

// send delivers m to a subscription.
func (p *pusher) send(ctx context.Context, sub Subscription, m pushMessage) error {
	uaPublic, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return fmt.Errorf("web push: subscription key: %w", err)
	}
	auth, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil {
		return fmt.Errorf("web push: subscription secret: %w", err)
	}
	body, err := seal(m.Payload, uaPublic, auth)
	if err != nil {
		return err
	}
	authz, err := p.key.authorization(sub.Endpoint, p.contact, p.now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authz)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(m.TTL/time.Second)))
	urgency := "normal"
	if m.Urgent {
		urgency = "high"
	}
	req.Header.Set("Urgency", urgency)
	if m.Topic != "" {
		req.Header.Set("Topic", m.Topic)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return errGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("web push: %s answered %s", hostOf(sub.Endpoint), resp.Status)
	}
	return nil
}

func hostOf(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return "the push service"
}
