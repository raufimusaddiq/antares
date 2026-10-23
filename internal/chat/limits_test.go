package chat

import (
	"net/http/httptest"
	"testing"

	"github.com/raufimusaddiq/antares/internal/auth"
)

func requireLimit(t *testing.T, w *httptest.ResponseRecorder, known ...bool) {
	t.Helper()
	var body = decodeBody[struct {
		Error struct {
			Code              string
			RetryAfterSeconds *int
		}
	}](t, w)
	if len(known) > 0 && !known[0] {
		if w.Code != 429 || body.Error.Code != "RATE_LIMITED" || body.Error.RetryAfterSeconds != nil || w.Header().Get("Retry-After") != "" {
			t.Fatalf("unknown wait: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
		return
	}
	if w.Code != 429 || body.Error.Code != "RATE_LIMITED" || body.Error.RetryAfterSeconds == nil || *body.Error.RetryAfterSeconds < 1 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("limit response: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
}

func TestDefaultMessageBurstLimits(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	path := "/api/chats/" + alice.newChat() + "/messages"
	before := len(a.hx.requests())
	for range 30 {
		if w := alice.do("POST", path, nil, nil); w.Code != 400 {
			t.Fatalf("below limit: %d %s", w.Code, w.Body.String())
		}
	}
	requireLimit(t, alice.do("POST", path, nil, nil))
	if got := len(a.hx.requests()); got != before {
		t.Fatalf("invalid or refused requests reached Hermes: %d -> %d", before, got)
	}
	bob.send(bob.newChat(), "B is unaffected")
}

func TestConfiguredMessageAndRunLimits(t *testing.T) {
	for _, tc := range []struct {
		name           string
		runs, messages int
	}{
		{"runs", 1, 3}, {"messages", 3, 1}, {"changed", 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := auth.DefaultLimits()
			limits.RunsPerMinute, limits.MessagesPerMinute = tc.runs, tc.messages
			a := newApp(t, appOpts{limits: &limits})
			alice, bob := a.user("alice"), a.user("bobby")
			chat := alice.newChat()
			path, headers := "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}
			count := min(tc.runs, tc.messages)
			var runID string
			for range count {
				w := alice.do("POST", path, headers, map[string]string{"input": "hello"})
				if w.Code != 202 {
					t.Fatalf("below quota: %d %s", w.Code, w.Body.String())
				}
				id := decodeBody[struct{ RunID string }](t, w).RunID
				if runID != "" && id != runID {
					t.Fatal("idempotent request created a second run")
				}
				runID = id
			}
			requireLimit(t, alice.do("POST", path, headers, map[string]string{"input": "hello"}))
			bob.send(bob.newChat(), "B is unaffected")
		})
	}
}

func TestUploadQuotaOnUnsupportedRoute(t *testing.T) {
	limits := auth.DefaultLimits()
	limits.UploadsPerMinute = 1
	a := newApp(t, appOpts{limits: &limits})
	alice, bob := a.user("alice"), a.user("bobby")
	before := len(a.hx.requests())
	if w := alice.do("POST", "/api/assets", nil, nil); w.Code != 501 {
		t.Fatalf("upload below limit: %d", w.Code)
	}
	requireLimit(t, alice.do("POST", "/api/assets", nil, nil))
	if w := bob.do("POST", "/api/assets", nil, nil); w.Code != 501 {
		t.Fatalf("A consumed B's upload quota: %d", w.Code)
	}
	if len(a.hx.requests()) != before {
		t.Fatal("unsupported or refused uploads reached Hermes")
	}
}

func TestSSEQuotaThroughAuthenticatedRoute(t *testing.T) {
	limits := auth.DefaultLimits()
	limits.SSEConnections = 1
	a := newApp(t, appOpts{limits: &limits})
	alice, bob := a.user("alice"), a.user("bobby")
	runA := alice.send(alice.newChat(), "stream A")
	_, frA := a.hx.runWithInput(t, "stream A")
	streamA := alice.open(runA, "")
	recv(t, frA.opens, "first stream")
	streamA.waitFor(t, "run.started")
	requireLimit(t, alice.get("/api/runs/"+runA+"/events"), false)
	runB := bob.send(bob.newChat(), "stream B")
	_, frB := a.hx.runWithInput(t, "stream B")
	streamB := bob.open(runB, "")
	recv(t, frB.opens, "B's stream")
	streamB.waitFor(t, "run.started")
	streamA.cancel()
	<-streamA.done
	// A nonexistent run returns immediately, so it also proves failed opens release their slot.
	path := "/api/runs/00000000000000000000000000000000/events"
	for range 2 {
		if w := alice.get(path); w.Code != 404 {
			t.Fatalf("slot not released: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestUpstreamRateLimitUsesSharedEnvelope(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	a.hx.mu.Lock()
	a.hx.failNext["POST /api/sessions"] = 429
	a.hx.mu.Unlock()
	w := alice.do("POST", "/api/chats", nil, nil)
	requireLimit(t, w, false)
	body := decodeBody[map[string]map[string]any](t, w)
	if hint, exists := body["error"]["retryAfterSeconds"]; !exists || hint != nil || len(body["error"]) != 4 {
		t.Fatalf("upstream limit envelope differs: %s", w.Body.String())
	}
}
