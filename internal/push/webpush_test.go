package push

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/push/pushtest"
)

func newVAPID(t *testing.T) *VAPID {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := k.Bytes()
	v, err := NewVAPID(raw, "mailto:ops@example.test")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func browserKeys(t *testing.T) (*ecdh.PrivateKey, []byte) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return k, auth
}

func TestEncryptRoundTripsThroughTheBrowserSide(t *testing.T) {
	ua, auth := browserKeys(t)
	msg := []byte(`{"v":1,"kind":"completed","chatId":"` + strings.Repeat("a", 32) + `"}`)
	body, err := encrypt(msg, ua.PublicKey().Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "completed") {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := pushtest.Decrypt(body, ua, auth)
	if err != nil || string(got) != string(msg) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	other, _ := browserKeys(t)
	if _, err := pushtest.Decrypt(body, other, auth); err == nil {
		t.Fatal("another browser's key opened the message")
	}
	if _, err := pushtest.Decrypt(body, ua, make([]byte, 16)); err == nil {
		t.Fatal("a wrong auth secret opened the message")
	}
}

// The payload type is the whole of what can be sent. A new field has to be added here on
// purpose, which is the moment to ask whether it may carry conversation content.
func TestPayloadIsAClosedShape(t *testing.T) {
	typ := reflect.TypeOf(Payload{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String && f.Type.Kind() != reflect.Int {
			t.Errorf("field %s is %s", f.Name, f.Type)
		}
		got = append(got, f.Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, []string{"v", "kind", "chatId"}) {
		t.Fatalf("Payload fields = %v", got)
	}
}

func TestVAPIDAuthorizationVerifies(t *testing.T) {
	v := newVAPID(t)
	now := time.Unix(1_800_000_000, 0)
	h, err := v.authorization("https://push.example.test", now)
	if err != nil {
		t.Fatal(err)
	}
	tok, key, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	if !ok || key != v.PublicKey() {
		t.Fatalf("header = %q", h)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %q", tok)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&v.key.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Aud, Sub string
		Exp      int64
	}
	json.Unmarshal(raw, &c)
	if c.Aud != "https://push.example.test" || c.Sub != v.Subject || c.Exp-now.Unix() > 24*3600 || c.Exp <= now.Unix() {
		t.Fatalf("claims = %+v", c)
	}
}

func TestNewVAPIDRefusesBadInput(t *testing.T) {
	if _, err := NewVAPID(make([]byte, 32), "mailto:a@example.test"); err == nil {
		t.Error("the zero scalar was accepted")
	}
	if _, err := NewVAPID([]byte("short"), "mailto:a@example.test"); err == nil {
		t.Error("a short key was accepted")
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw, _ := k.Bytes()
	if _, err := NewVAPID(raw, "ops@example.test"); err == nil {
		t.Error("a subject that is not a URL was accepted")
	}
}

func TestValidEndpoint(t *testing.T) {
	good := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/abc",
		"https://wns2-par02p.notify.windows.com/w/?token=x",
		"https://fcm.googleapis.com:443/x",
	}
	for _, u := range good {
		if err := ValidEndpoint(u); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
	bad := []string{
		"http://fcm.googleapis.com/x", "https://evil.example.test/x", "https://fcm.googleapis.com.evil.test/x",
		"https://evilfcm.googleapis.com@evil.test/x", "https://user:pw@fcm.googleapis.com/x", "https://fcm.googleapis.com:8443/x",
		"https://127.0.0.1/x", "https://localhost/x", "https://169.254.169.254/latest", "https://[::1]/x", "ftp://fcm.googleapis.com/x",
		"https://fcm.googleapis.com/x#frag", "", "https://" + strings.Repeat("a", 3000) + ".push.apple.com/",
		"https://notfcm.googleapis.com.evil.test/", "https://googleapis.com/x",
	}
	for _, u := range bad {
		if err := ValidEndpoint(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestSafeClientNeverConnectsToANonPublicAddress(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	ua, auth := browserKeys(t)
	s := NewSender(newVAPID(t), nil)
	err := s.Send(context.Background(), Target{Endpoint: srv.URL, P256dh: ua.PublicKey().Bytes(), Auth: auth}, Payload{V: 1, Kind: KindCompleted})
	if hit || err == nil {
		t.Fatalf("loopback was reached (hit=%v, err=%v)", hit, err)
	}
	if strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("the error carries the endpoint: %v", err)
	}
}

func TestSendClassifiesPushServiceAnswers(t *testing.T) {
	ua, auth := browserKeys(t)
	status := 201
	var got *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	s := NewSender(newVAPID(t), srv.Client())
	tg := Target{Endpoint: srv.URL + "/send/secret-capability", P256dh: ua.PublicKey().Bytes(), Auth: auth}
	if err := s.Send(context.Background(), tg, Payload{V: 1, Kind: KindFailed, ChatID: strings.Repeat("b", 32)}); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(got.Header.Get("Authorization"), "vapid t=") || got.Header.Get("TTL") == "" {
		t.Fatalf("headers = %v", got.Header)
	}
	plain, err := pushtest.Decrypt(body, ua, auth)
	if err != nil || !strings.Contains(string(plain), `"kind":"failed"`) {
		t.Fatalf("plain = %q %v", plain, err)
	}
	for code, want := range map[int]error{404: ErrGone, 410: ErrGone, 429: ErrTransient, 503: ErrTransient, 400: ErrRejected, 403: ErrRejected} {
		status = code
		if err := s.Send(context.Background(), tg, Payload{V: 1, Kind: KindCompleted}); !errors.Is(err, want) {
			t.Errorf("status %d -> %v, want %v", code, err, want)
		}
	}
}

// The standard predicates pass these; each is address space a push service is not in, and
// 100.100.100.200 is a cloud metadata endpoint.
func TestPublicIPRefusesSpecialPurposeSpace(t *testing.T) {
	for _, s := range []string{
		"100.100.100.200", "100.64.0.1", "100.127.255.254", "192.0.0.1", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.255",
		"0.0.0.0", "10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.169.254", "192.0.2.1", "198.51.100.1", "203.0.113.1",
		"::1", "::", "fe80::1", "fc00::1", "fd00::1", "2001:db8::1", "64:ff9b::a00:1", "2002:a00:1::1", "2001::1",
		"::ffff:10.0.0.1", "::ffff:100.100.100.200", "::ffff:127.0.0.1",
	} {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("%s was treated as public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "142.250.80.46", "34.107.243.93", "17.253.144.10", "2606:4700:4700::1111", "2a00:1450:4001:81b::200e"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("%s was refused", s)
		}
	}
	if publicIP(nil) || publicIP(net.IP{1, 2, 3}) {
		t.Error("a malformed address was accepted")
	}
}

func TestAudienceHasNoDefaultPort(t *testing.T) {
	for in, want := range map[string]string{
		"https://fcm.googleapis.com:443/fcm/send/x": "https://fcm.googleapis.com",
		"https://fcm.googleapis.com/fcm/send/x":     "https://fcm.googleapis.com",
		"https://FCM.GoogleAPIs.com/x":              "https://fcm.googleapis.com",
		"https://127.0.0.1:8443/x":                  "https://127.0.0.1:8443",
	} {
		u, _ := url.Parse(in)
		if got := audience(u); got != want {
			t.Errorf("audience(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestRetryDoesNotWaitAfterTheFinalAttempt(t *testing.T) {
	ua, auth := browserKeys(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(503) }))
	defer srv.Close()
	var waits []int
	s := &Service{sender: NewSender(newVAPID(t), srv.Client()), backoff: func(n int) time.Duration { waits = append(waits, n); return time.Millisecond }}
	err := s.sendWithRetry(context.Background(), Target{Endpoint: srv.URL, P256dh: ua.PublicKey().Bytes(), Auth: auth}, Payload{V: 1, Kind: KindCompleted})
	if !errors.Is(err, ErrTransient) || hits != sendAttempts {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
	if len(waits) != sendAttempts-1 {
		t.Fatalf("waited %v between %d attempts, want %d waits", waits, sendAttempts, sendAttempts-1)
	}
}
