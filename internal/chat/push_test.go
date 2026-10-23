package chat

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/push"
	"github.com/raufimusaddiq/antares/internal/push/pushtest"
	"github.com/raufimusaddiq/antares/internal/store"
)

// A fake push service: it records what a browser's push service would receive and can
// open each body with the device's private key, the way the browser does.
type pushService struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []pushReq
	code int
}

type pushReq struct {
	Path   string
	Header http.Header
	Body   []byte
}

func newPushService(t *testing.T) *pushService {
	p := &pushService{code: 201}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.reqs = append(p.reqs, pushReq{r.URL.Path, r.Header.Clone(), b})
		code := p.code
		p.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pushService) requests() []pushReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pushReq(nil), p.reqs...)
}

func (p *pushService) setCode(c int) { p.mu.Lock(); p.code = c; p.mu.Unlock() }

// device is one browser: a push subscription with its private half.
type device struct {
	key  *ecdh.PrivateKey
	auth []byte
	path string
}

func newDevice(t *testing.T, path string) *device {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	rand.Read(a)
	return &device{key: k, auth: a, path: path}
}

func (d *device) open(t *testing.T, r pushReq) map[string]any {
	t.Helper()
	plain, err := pushtest.Decrypt(r.Body, d.key, d.auth)
	if err != nil {
		t.Fatalf("device cannot open the push: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(plain, &m); err != nil {
		t.Fatalf("payload is not JSON: %q", plain)
	}
	return m
}

// withPush gives the app a configured push service whose egress goes to ps. validate
// nil selects the production endpoint rules.
func withPush(t *testing.T, a *app, ps *pushService, validate func(string) error) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw, _ := k.Bytes()
	v, err := push.NewVAPID(raw, "mailto:ops@example.test")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := push.New(push.Options{DB: a.db, Box: a.box, VAPID: v, Client: ps.srv.Client(), ValidateEndpoint: validate,
		Backoff: func(int) time.Duration { return time.Millisecond }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	a.push = svc
	a.svc.notifier = svc
	a.h = rebuild(t, a, a.svc)
}

func allowAny(string) error { return nil }

func (p *person) subscribe(t *testing.T, ps *pushService, d *device) string {
	t.Helper()
	w := p.do("POST", "/api/push/subscriptions", nil, map[string]any{
		"endpoint": ps.srv.URL + d.path,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(d.key.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(d.auth),
		},
	})
	if w.Code != 201 {
		t.Fatalf("subscribe: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ ID string }](t, w).ID
}

// finishRun drives one run to the given terminal frame and waits for the notifier.
func finishRun(t *testing.T, a *app, p *person, chatID, input, frame, data string) string {
	t.Helper()
	runID := p.send(chatID, input)
	_, fr := a.hx.runWithInput(t, input)
	s := p.open(runID, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, frame, data)
	s.waitFor(t, map[string]string{"run.completed": "run.completed", "run.failed": "run.failed"}[frame])
	a.push.Wait()
	return runID
}

func TestPushRoutesNeedAnEligibleAccount(t *testing.T) {
	a := newApp(t)
	withPush(t, a, newPushService(t), allowAny)
	pending := a.account("pendingp", "USER", "PENDING", "UNPROVISIONED")
	for _, r := range auth.PushRoutes {
		rt := struct{ m, path string }{r.Method, strings.ReplaceAll(r.Path, "{subId}", store.NewID())}
		if w := pending.do(rt.m, rt.path, nil, map[string]string{"mode": "all"}); w.Code != 403 {
			t.Errorf("pending %s %s = %d", rt.m, rt.path, w.Code)
		}
		anon := httptest.NewRequest(rt.m, testOrigin+rt.path, nil)
		w := httptest.NewRecorder()
		a.h.ServeHTTP(w, anon)
		if w.Code != 401 {
			t.Errorf("anonymous %s %s = %d", rt.m, rt.path, w.Code)
		}
	}
	// A mutation from another origin is refused before it reaches the handler.
	alice := a.user("alice")
	w := alice.do("PUT", "/api/me/notifications", map[string]string{"Origin": "https://evil.example.test"}, map[string]string{"mode": "all"})
	if w.Code != 403 {
		t.Errorf("cross-origin PUT = %d", w.Code)
	}
}

func TestUnconfiguredPushIsReportedNotFaked(t *testing.T) {
	a := newApp(t)
	svc, err := push.New(push.Options{DB: a.db, Box: a.box})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	a.push, a.svc.notifier = svc, svc
	a.h = rebuild(t, a, a.svc)
	alice := a.user("alice")
	if w := alice.get("/api/push/public-key"); w.Code != 501 || errCode(t, w) != "FEATURE_NOT_CONFIGURED" {
		t.Errorf("public key: %d %s", w.Code, w.Body.String())
	}
	if w := alice.do("POST", "/api/push/subscriptions", nil, map[string]any{"endpoint": "https://fcm.googleapis.com/x"}); w.Code != 501 {
		t.Errorf("subscribe: %d", w.Code)
	}
	if w := alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "all"}); w.Code != 501 {
		t.Errorf("turning on: %d", w.Code)
	}
	v := decodeBody[struct {
		Configured bool
		Mode       string
	}](t, alice.get("/api/me/notifications"))
	if v.Configured || v.Mode != "off" {
		t.Errorf("view = %+v", v)
	}
}

func TestSubscribeRefusesWhatItShould(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, nil) // production endpoint rules
	alice := a.user("alice")
	d := newDevice(t, "/x")
	keys := map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(d.key.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(d.auth)}
	bad := map[string]any{
		"plain http":        map[string]any{"endpoint": "http://fcm.googleapis.com/x", "keys": keys},
		"internal host":     map[string]any{"endpoint": "https://127.0.0.1/x", "keys": keys},
		"unknown push host": map[string]any{"endpoint": "https://hooks.example.test/x", "keys": keys},
		"bad key":           map[string]any{"endpoint": "https://fcm.googleapis.com/x", "keys": map[string]string{"p256dh": "AAAA", "auth": keys["auth"]}},
		"short auth":        map[string]any{"endpoint": "https://fcm.googleapis.com/x", "keys": map[string]string{"p256dh": keys["p256dh"], "auth": "AAAA"}},
		"profile smuggled":  map[string]any{"endpoint": "https://fcm.googleapis.com/x", "keys": keys, "profile": "u_other"},
		"user id smuggled":  map[string]any{"endpoint": "https://fcm.googleapis.com/x", "keys": keys, "userId": "someone"},
	}
	for name, body := range bad {
		if w := alice.do("POST", "/api/push/subscriptions", nil, body); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if n := decodeBody[struct{ Devices []any }](t, alice.get("/api/me/notifications")).Devices; len(n) != 0 {
		t.Errorf("a refused subscription was stored: %v", n)
	}
	if w := alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "loud"}); w.Code != 400 {
		t.Errorf("bad mode: %d", w.Code)
	}
}

func TestStoredSubscriptionIsSealed(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	d := newDevice(t, "/capability-token-xyz")
	alice.subscribe(t, ps, d)
	var ep, keys []byte
	if err := a.db.QueryRow(`SELECT sealed_endpoint, sealed_keys FROM push_subscriptions`).Scan(&ep, &keys); err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{ep, keys} {
		if strings.Contains(string(b), "capability-token-xyz") || strings.Contains(string(b), ps.srv.URL) {
			t.Fatal("the endpoint is stored in clear")
		}
	}
}

func TestOptOutSendsNothing(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice, bob := a.user("alice"), a.user("bobby")

	// No device at all, and a device that was never opted in for this account: nothing.
	chatA := alice.newChat()
	finishRun(t, a, alice, chatA, "no device yet", "run.completed", `{}`)

	d := newDevice(t, "/alice-phone")
	subID := alice.subscribe(t, ps, d)
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("subscribing alone sent %d push(es)", n)
	}

	// Muted: a device exists but the mode is off.
	if w := alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "off"}); w.Code != 200 {
		t.Fatalf("mute: %d %s", w.Code, w.Body.String())
	}
	finishRun(t, a, alice, chatA, "while muted", "run.completed", `{}`)
	finishRun(t, a, alice, chatA, "while muted and failing", "run.failed", `{}`)
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("a muted account got %d push(es)", n)
	}

	// Back on, then revoked: delivery stops at once.
	alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "all"})
	if w := alice.do("DELETE", "/api/push/subscriptions/"+subID, nil, nil); w.Code != 204 {
		t.Fatalf("revoke: %d", w.Code)
	}
	finishRun(t, a, alice, chatA, "after revoke", "run.completed", `{}`)
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("a revoked device got %d push(es)", n)
	}

	// Bob never opted in and shares nothing with Alice's device.
	chatB := bob.newChat()
	finishRun(t, a, bob, chatB, "bob without opt-in", "run.completed", `{}`)
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("an account that never opted in got %d push(es)", n)
	}
}

func TestSubscribingTurnsOnAndFailuresOnlyHonoursItself(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	if v := decodeBody[struct{ Mode string }](t, alice.get("/api/me/notifications")); v.Mode != "off" {
		t.Fatalf("default mode = %q", v.Mode)
	}
	d := newDevice(t, "/phone")
	alice.subscribe(t, ps, d)
	if v := decodeBody[struct{ Mode string }](t, alice.get("/api/me/notifications")); v.Mode != "all" {
		t.Fatalf("mode after opting in = %q", v.Mode)
	}
	alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "failures"})
	chat := alice.newChat()
	finishRun(t, a, alice, chat, "good one", "run.completed", `{}`)
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("failures-only sent %d push(es) for a completion", n)
	}
	finishRun(t, a, alice, chat, "bad one", "run.failed", `{}`)
	if reqs := ps.requests(); len(reqs) != 1 || d.open(t, reqs[0])["kind"] != "failed" {
		t.Fatalf("failures-only on a failure: %v", reqs)
	}
}

func TestPayloadCarriesNoConversationContent(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	d := newDevice(t, "/alice-phone")
	alice.subscribe(t, ps, d)

	chat := alice.newChat()
	if _, err := a.db.Exec(`UPDATE chats SET title = 'TITLE-SECRET-77' WHERE id = ?`, chat); err != nil {
		t.Fatal(err)
	}
	secrets := []string{"PROMPT-SECRET-11", "ANSWER-SECRET-22", "TITLE-SECRET-77", "TOOL-SECRET-33"}

	runOK := alice.send(chat, "PROMPT-SECRET-11 please")
	_, fr := a.hx.runWithInput(t, "PROMPT-SECRET-11 please")
	s := alice.open(runOK, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "tool.started", `{"tool_call_id":"c1","name":"TOOL-SECRET-33"}`)
	a.hx.push(fr, "assistant.delta", `{"delta":"ANSWER-SECRET-22"}`)
	a.hx.setStatus(fr, "completed", "ANSWER-SECRET-22")
	a.hx.push(fr, "run.completed", `{"output":"ANSWER-SECRET-22"}`)
	s.waitFor(t, "run.completed")
	a.push.Wait()

	finishRun(t, a, alice, chat, "PROMPT-SECRET-11 again", "run.failed", `{"error":"ANSWER-SECRET-22"}`)

	reqs := ps.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d push(es), want one for the completion and one for the failure", len(reqs))
	}
	for i, kind := range []string{"completed", "failed"} {
		m := d.open(t, reqs[i])
		// Exactly these keys, with exactly these values: this is the whole payload.
		want := map[string]any{"v": float64(1), "kind": kind, "chatId": chat}
		if len(m) != len(want) {
			t.Errorf("payload %d has keys %v", i, m)
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("payload %d %s = %v, want %v", i, k, m[k], v)
			}
		}
		// And nothing else on the wire carries content either.
		wire := reqs[i].Path + " " + string(reqs[i].Body)
		for k, vs := range reqs[i].Header {
			wire += " " + k + "=" + strings.Join(vs, ",")
		}
		for _, sec := range secrets {
			if strings.Contains(wire, sec) {
				t.Errorf("request %d carries %q", i, sec)
			}
		}
		if reqs[i].Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("request %d is not encrypted: %v", i, reqs[i].Header)
		}
	}
}

func TestOneNotificationPerRunAndCancelNeverNotifies(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	d := newDevice(t, "/phone")
	alice.subscribe(t, ps, d)
	chat := alice.newChat()

	runID := finishRun(t, a, alice, chat, "once only", "run.completed", `{}`)
	if n := len(ps.requests()); n != 1 {
		t.Fatalf("got %d push(es), want 1", n)
	}
	// The same terminal status reported again (a re-attached run, a retry) is not a second alert.
	a.push.RunFinished(alice.id, runID, chat, store.RunCompleted)
	a.push.Wait()
	if n := len(ps.requests()); n != 1 {
		t.Fatalf("a repeated terminal status sent %d push(es)", n)
	}

	// A run the user cancels is not reported as a result, and neither is a disconnect.
	cancelled := alice.send(chat, "to be cancelled")
	_, fr := a.hx.runWithInput(t, "to be cancelled")
	recv(t, fr.opens, "stream")
	if w := alice.do("POST", "/api/runs/"+cancelled+"/cancel", nil, nil); w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	a.push.RunFinished(alice.id, cancelled, chat, store.RunCancelled)
	a.push.RunFinished(alice.id, cancelled, chat, store.RunDisconnected)
	a.push.Wait()
	if n := len(ps.requests()); n != 1 {
		t.Fatalf("cancel/disconnect sent %d extra push(es)", n-1)
	}
}

func TestOnlyTheOwnersDevicesAreNotified(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice, bob := a.user("alice"), a.user("bobby")
	da, db := newDevice(t, "/alice-phone"), newDevice(t, "/bob-phone")
	aliceSub := alice.subscribe(t, ps, da)
	bob.subscribe(t, ps, db)

	// Bob cannot see or revoke Alice's device.
	if w := bob.do("DELETE", "/api/push/subscriptions/"+aliceSub, nil, nil); w.Code != 404 {
		t.Errorf("bob revoking alice's device = %d", w.Code)
	}
	if v := decodeBody[struct{ Devices []struct{ ID string } }](t, bob.get("/api/me/notifications")); len(v.Devices) != 1 || v.Devices[0].ID == aliceSub {
		t.Errorf("bob's device list = %+v", v)
	}

	chatA := alice.newChat()
	finishRun(t, a, alice, chatA, "alice runs", "run.completed", `{}`)
	reqs := ps.requests()
	if len(reqs) != 1 || reqs[0].Path != da.path {
		t.Fatalf("alice's run produced %v", reqs)
	}
	if d := da.open(t, reqs[0]); d["chatId"] != chatA {
		t.Errorf("payload = %v", d)
	}
	if _, err := pushtest.Decrypt(reqs[0].Body, db.key, db.auth); err == nil {
		t.Error("bob's device can read alice's notification")
	}
}

// The notification's link is /chat/<id>. Whoever follows it, the page asks the API for
// that chat, and the API answers only its owner.
func TestDeepLinkTargetOpensForTheOwnerOnly(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice, bob := a.user("alice"), a.user("bobby")
	alice.subscribe(t, ps, newDevice(t, "/phone"))
	chatA := alice.newChat()
	finishRun(t, a, alice, chatA, "alice runs", "run.completed", `{}`)

	if w := alice.get("/api/chats/" + chatA); w.Code != 200 {
		t.Fatalf("owner opening her chat = %d", w.Code)
	}
	before := len(a.hx.requests())
	for _, p := range []string{"/api/chats/" + chatA, "/api/chats/" + chatA + "/messages"} {
		w := bob.get(p)
		if w.Code != 404 || errCode(t, w) != "NOT_FOUND" || strings.Contains(w.Body.String(), "alice") {
			t.Errorf("bob following alice's link: GET %s = %d %s", p, w.Code, w.Body.String())
		}
	}
	if n := len(a.hx.requests()); n != before {
		t.Errorf("a foreign link made %d Hermes request(s)", n-before)
	}
	// The same shell is served to everyone; it carries no chat content.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", testOrigin+"/chat/"+chatA, nil)
	a.h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "alice") {
		t.Errorf("deep link shell = %d", w.Code)
	}
}

func TestSuspendedOrDemotedAccountGetsNothing(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	alice.subscribe(t, ps, newDevice(t, "/phone"))
	chat := alice.newChat()
	runID := alice.send(chat, "finishing while suspended")
	_, fr := a.hx.runWithInput(t, "finishing while suspended")
	s := alice.open(runID, "")
	recv(t, fr.opens, "stream")
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, alice.id); err != nil {
		t.Fatal(err)
	}
	a.push.RunFinished(alice.id, runID, chat, store.RunCompleted)
	a.push.Wait()
	s.close()
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("a suspended account got %d push(es)", n)
	}
}

func TestGoneSubscriptionIsDroppedAndTransientFailureIsBounded(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	alice.subscribe(t, ps, newDevice(t, "/phone"))
	chat := alice.newChat()

	ps.setCode(503)
	finishRun(t, a, alice, chat, "service down", "run.completed", `{}`)
	if n := len(ps.requests()); n != 3 {
		t.Fatalf("a transient failure was tried %d times, want 3", n)
	}
	if v := decodeBody[struct{ Devices []any }](t, alice.get("/api/me/notifications")); len(v.Devices) != 1 {
		t.Fatalf("a transient failure dropped the device")
	}
	// The failed attempt keeps its claim, so a re-reported terminal status does not send again:
	// delivery is at most once per {device, run, kind}, and delivered_at tells the two states apart.
	var runID string
	if err := a.db.QueryRow(`SELECT id FROM runs WHERE user_id = ?`, alice.id).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	a.push.RunFinished(alice.id, runID, chat, store.RunCompleted)
	a.push.Wait()
	if n := len(ps.requests()); n != 3 {
		t.Fatalf("a re-reported status after a failed send sent again (%d requests)", n)
	}
	var claims, delivered int
	a.db.QueryRow(`SELECT COUNT(*), COUNT(delivered_at) FROM push_delivery_events WHERE run_id = ?`, runID).Scan(&claims, &delivered)
	if claims != 1 || delivered != 0 {
		t.Fatalf("claims=%d delivered=%d, want 1 and 0", claims, delivered)
	}
	ps.setCode(410)
	finishRun(t, a, alice, chat, "device gone", "run.completed", `{}`)
	if v := decodeBody[struct{ Devices []any }](t, alice.get("/api/me/notifications")); len(v.Devices) != 0 {
		t.Fatalf("a 410 left the device registered: %v", v.Devices)
	}
}

// An endpoint is a capability, but presenting one must not let a request revoke someone else's
// device: a foreign endpoint is a conflict the client resolves explicitly, and the holder keeps it.
func TestForeignEndpointRegistrationDoesNotSilentlyRevokeTheOtherAccount(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice, bob := a.user("alice"), a.user("bobby")
	d := newDevice(t, "/shared-browser")
	aliceSub := alice.subscribe(t, ps, d)

	w := bob.do("POST", "/api/push/subscriptions", nil, map[string]any{
		"endpoint": ps.srv.URL + d.path,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(d.key.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(d.auth),
		},
	})
	if w.Code != 409 || errCode(t, w) != "ENDPOINT_IN_USE" {
		t.Fatalf("bob presenting alice's endpoint = %d %s", w.Code, w.Body.String())
	}
	if v := decodeBody[struct{ Devices []struct{ ID string } }](t, alice.get("/api/me/notifications")); len(v.Devices) != 1 || v.Devices[0].ID != aliceSub {
		t.Fatalf("alice's device was revoked by bob's request: %+v", v)
	}
	if v := decodeBody[struct{ Devices []any }](t, bob.get("/api/me/notifications")); len(v.Devices) != 0 {
		t.Fatalf("bob holds a device on alice's endpoint: %v", v.Devices)
	}
	finishRun(t, a, alice, alice.newChat(), "alice still notified", "run.completed", `{}`)
	if n := len(ps.requests()); n != 1 {
		t.Fatalf("alice got %d push(es) after the refused registration, want 1", n)
	}
	if strings.Contains(w.Body.String(), "alice") {
		t.Errorf("the conflict answer names the holder: %s", w.Body.String())
	}
}

// The same account registering the same endpoint again replaces its own row (fresh keys), once.
func TestSameAccountReRegistrationReplacesItsOwnDevice(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	d := newDevice(t, "/phone")
	first := alice.subscribe(t, ps, d)
	second := alice.subscribe(t, ps, d)
	v := decodeBody[struct{ Devices []struct{ ID string } }](t, alice.get("/api/me/notifications"))
	if first == second || len(v.Devices) != 1 || v.Devices[0].ID != second {
		t.Fatalf("devices after re-registering = %+v (first %s, second %s)", v, first, second)
	}
}

// Muting is the account's choice; adding a device (the per-device opt-in) must not undo it.
func TestAddingADeviceKeepsAMute(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	first, second := newDevice(t, "/first"), newDevice(t, "/second")
	alice.subscribe(t, ps, first)
	alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "off"})
	alice.subscribe(t, ps, second)
	if v := decodeBody[struct{ Mode string }](t, alice.get("/api/me/notifications")); v.Mode != "off" {
		t.Fatalf("mode after adding a device = %q, want off", v.Mode)
	}
	finishRun(t, a, alice, alice.newChat(), "while muted, two devices", "run.completed", `{}`)
	if n := len(ps.requests()); n != 0 {
		t.Fatalf("a muted account with two devices got %d push(es)", n)
	}
}

// Every PushRoutes entry has a handler and every handler has an entry, so neither a table row
// without a handler nor a handler outside the guarded table can exist unnoticed.
func TestPushRouteTableMatchesHandlers(t *testing.T) {
	a := newApp(t)
	withPush(t, a, newPushService(t), allowAny)
	want := map[string]bool{}
	for _, rt := range auth.PushRoutes {
		want[rt.Method+" "+rt.Path] = true
	}
	got := a.push.Handlers()
	for k := range want {
		if got[k] == nil {
			t.Errorf("PushRoutes lists %s but the service has no handler", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("handler %s is not in PushRoutes, so it would not be mounted", k)
		}
	}
}

func TestDeliveryRecordsAreBoundedAndUnreadableSubscriptionsAreLogged(t *testing.T) {
	a := newApp(t)
	ps := newPushService(t)
	withPush(t, a, ps, allowAny)
	alice := a.user("alice")
	subID := alice.subscribe(t, ps, newDevice(t, "/phone"))
	chat := alice.newChat()
	finishRun(t, a, alice, chat, "first", "run.completed", `{}`)
	// Age the record past retention; the next notification prunes it.
	if _, err := a.db.Exec(`UPDATE push_delivery_events SET created_at = '2020-01-01T00:00:00.000000000Z'`); err != nil {
		t.Fatal(err)
	}
	// A subscription that cannot be opened (a rotated secret key) says so, with its id.
	if _, err := a.db.Exec(`UPDATE push_subscriptions SET sealed_endpoint = x'00' WHERE id = ?`, subID); err != nil {
		t.Fatal(err)
	}
	finishRun(t, a, alice, chat, "second", "run.completed", `{}`)
	// Pruning does not wait for the account to be unmuted.
	alice.do("PUT", "/api/me/notifications", nil, map[string]string{"mode": "off"})
	if _, err := a.db.Exec(`INSERT INTO push_delivery_events (id, user_id, subscription_id, run_id, event_kind, created_at)
		SELECT 'old-muted', user_id, ?, id, 'failed', '2020-01-01T00:00:00.000000000Z' FROM runs LIMIT 1`, subID); err != nil {
		t.Fatal(err)
	}
	finishRun(t, a, alice, chat, "third, muted", "run.completed", `{}`)
	var old int
	a.db.QueryRow(`SELECT COUNT(*) FROM push_delivery_events WHERE created_at < '2021'`).Scan(&old)
	if old != 0 {
		t.Errorf("%d expired delivery record(s) survived", old)
	}
	if !strings.Contains(logBuf.String(), "cannot open subscription "+subID) {
		t.Errorf("no diagnosable log line for an unreadable subscription: %q", logBuf.String())
	}
	if strings.Contains(logBuf.String(), ps.srv.URL) {
		t.Error("the log carries the endpoint")
	}
}
