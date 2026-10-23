// Package push sends the generic "your run finished" Web Push (PRD §21.2, UXP-002).
// It implements just the sender half of RFC 8030 / RFC 8291 (aes128gcm) / RFC 8292
// (VAPID) with the standard library: no dependency, no gateway. It carries no model
// logic and the only thing it can be asked to send is a Payload, whose closed shape
// holds no conversation content.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Payload is everything a notification can carry, and the only thing the sender
// accepts. Both fields are fixed vocabulary or an opaque id: no title, prompt,
// message body or model output exists in this type to be put there.
type Payload struct {
	V      int    `json:"v"`
	Kind   string `json:"kind"`   // "completed" | "failed"
	ChatID string `json:"chatId"` // opaque app chat id, for the deep link only
}

// Notification kinds.
const (
	KindCompleted = "completed"
	KindFailed    = "failed"
)

// VAPID is the application server key pair (RFC 8292).
type VAPID struct {
	key     *ecdsa.PrivateKey
	Subject string // mailto: or https: contact
}

// NewVAPID builds the key pair from the raw 32-byte P-256 private scalar.
func NewVAPID(private []byte, subject string) (*VAPID, error) {
	if !strings.HasPrefix(subject, "mailto:") && !strings.HasPrefix(subject, "https://") {
		return nil, errors.New("push: VAPID subject must be a mailto: or https: URL")
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), private)
	if err != nil {
		return nil, errors.New("push: VAPID private key is not a valid P-256 scalar")
	}
	return &VAPID{key: k, Subject: subject}, nil
}

// PublicKey is the uncompressed point, base64url, as PushManager.subscribe wants it.
func (v *VAPID) PublicKey() string {
	b, _ := v.key.PublicKey.Bytes()
	return base64.RawURLEncoding.EncodeToString(b)
}

func (v *VAPID) authorization(aud string, now time.Time) (string, error) {
	hdr, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	claims, _ := json.Marshal(map[string]any{"aud": aud, "exp": now.Add(12 * time.Hour).Unix(), "sub": v.Subject})
	signing := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	der, err := ecdsa.SignASN1(rand.Reader, v.key, sum[:])
	if err != nil {
		return "", err
	}
	var sig struct{ R, S asn1.RawValue }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return "", err
	}
	raw := make([]byte, 64)
	copy(raw[32-len(trimZero(sig.R.Bytes)):32], trimZero(sig.R.Bytes))
	copy(raw[64-len(trimZero(sig.S.Bytes)):], trimZero(sig.S.Bytes))
	return "vapid t=" + signing + "." + base64.RawURLEncoding.EncodeToString(raw) + ", k=" + v.PublicKey(), nil
}

func trimZero(b []byte) []byte {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

// ---- RFC 8291 payload encryption --------------------------------------------------

func hkdfExtract(salt, ikm []byte) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write(ikm)
	return m.Sum(nil)
}

// hkdfExpand returns the first n (<= 32) bytes of HKDF-Expand.
func hkdfExpand(prk, info []byte, n int) []byte {
	m := hmac.New(sha256.New, prk)
	m.Write(info)
	m.Write([]byte{1})
	return m.Sum(nil)[:n]
}

const recordSize = 4096

// encrypt produces the aes128gcm body for one subscription (a single record).
func encrypt(plain, uaPublic, authSecret []byte) ([]byte, error) {
	if len(authSecret) != 16 {
		return nil, errors.New("push: auth secret must be 16 bytes")
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, errors.New("push: subscription key is not a P-256 point")
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := as.PublicKey().Bytes()
	ikm := hkdfExpand(hkdfExtract(authSecret, secret), append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...), 32)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	prk := hkdfExtract(salt, ikm)
	blk, err := aes.NewCipher(hkdfExpand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	if len(plain)+1+gcm.Overhead() > recordSize {
		return nil, errors.New("push: payload too large")
	}
	record := gcm.Seal(nil, hkdfExpand(prk, []byte("Content-Encoding: nonce\x00"), 12), append(append([]byte{}, plain...), 0x02), nil)
	var hdr bytes.Buffer
	hdr.Write(salt)
	_ = binary.Write(&hdr, binary.BigEndian, uint32(recordSize))
	hdr.WriteByte(byte(len(asPublic)))
	hdr.Write(asPublic)
	return append(hdr.Bytes(), record...), nil
}

// ---- delivery ---------------------------------------------------------------------

// Target is one decrypted subscription.
type Target struct {
	Endpoint string
	P256dh   []byte
	Auth     []byte
}

// Result classes of one send.
var (
	// ErrGone: the push service says the subscription no longer exists (404/410).
	ErrGone = errors.New("push: subscription is gone")
	// ErrTransient: worth another bounded attempt (network, 429, 5xx).
	ErrTransient = errors.New("push: transient failure")
	// ErrRejected: the service refused it for a reason a retry will not change.
	ErrRejected = errors.New("push: rejected")
)

// Sender delivers a Payload to a Target. Errors never contain the endpoint (it is
// a capability URL).
type Sender struct {
	vapid  *VAPID
	client *http.Client
	now    func() time.Time
}

// NewSender uses a client that refuses redirects and connections to anything but a
// public address, so a stored endpoint cannot be turned into a request to an
// internal service (PRD §21.2). A nil client selects that default.
func NewSender(v *VAPID, c *http.Client) *Sender {
	if c == nil {
		c = SafeClient()
	}
	return &Sender{vapid: v, client: c, now: time.Now}
}

// SafeClient is the default egress client for push services.
func SafeClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !publicIP(ip) {
			return errors.New("push: refusing to connect to a non-public address")
		}
		return nil
	}}
	return &http.Client{
		Timeout:       15 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

// Send encrypts p for t and posts it.
func (s *Sender) Send(ctx context.Context, t Target, p Payload) error {
	u, err := url.Parse(t.Endpoint)
	if err != nil {
		return ErrRejected
	}
	body, err := encrypt(mustJSON(p), t.P256dh, t.Auth)
	if err != nil {
		return ErrRejected
	}
	authz, err := s.vapid.authorization(u.Scheme+"://"+u.Host, s.now())
	if err != nil {
		return ErrRejected
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrRejected
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", authz)
	req.Header.Set("TTL", "3600")
	req.Header.Set("Urgency", "normal")
	resp, err := s.client.Do(req)
	if err != nil {
		return ErrTransient // the error text carries the endpoint URL; drop it
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return ErrTransient
	}
	return fmt.Errorf("%w: status %d", ErrRejected, resp.StatusCode)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// ---- endpoint validation -----------------------------------------------------------

// pushHosts are the push services browsers use. A subscription for any other host
// is refused at registration: Antares is not a generic HTTP callback service.
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome, Edge, Opera, Samsung (FCM)
	"updates.push.services.mozilla.com", // Firefox
	"push.services.mozilla.com",
	"web.push.apple.com", // Safari / iOS web apps
	"push.apple.com",
	"notify.windows.com", // legacy Edge / WNS
}

// ValidEndpoint accepts an https URL on the default port, without credentials or
// fragment, whose host is a known push service (or a subdomain of one).
func ValidEndpoint(raw string) error {
	if len(raw) > 2048 {
		return errors.New("endpoint too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return errors.New("endpoint must be an https URL")
	}
	if p := u.Port(); p != "" && p != "443" {
		return errors.New("endpoint port is not allowed")
	}
	h := strings.ToLower(u.Hostname())
	for _, ok := range pushHosts {
		if h == ok || strings.HasSuffix(h, "."+ok) {
			return nil
		}
	}
	return errors.New("endpoint is not a known push service")
}
