package chat

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/store"
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
		{"runs", 1, 3}, {"messages", 3, 1}, {"changed", 2, 3}, {"changed messages", 3, 2},
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

func TestUploadRateLimit(t *testing.T) {
	limits := auth.DefaultLimits()
	limits.UploadsPerMinute = 1
	a := newApp(t, appOpts{limits: &limits})
	alice, bob := a.user("alice"), a.user("bobby")
	before := len(a.hx.requests())
	if w := alice.do("POST", "/api/assets", audioHdr, webmBody); w.Code != 201 {
		t.Fatalf("upload below limit: %d", w.Code)
	}
	requireLimit(t, alice.do("POST", "/api/assets", audioHdr, webmBody))
	if w := bob.do("POST", "/api/assets", audioHdr, webmBody); w.Code != 201 {
		t.Fatalf("A consumed B's upload quota: %d", w.Code)
	}
	if len(a.hx.requests()) != before {
		t.Fatal("uploads reached Hermes")
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

// An export reads one history page per 100 turns and every page rebuilds the whole
// transcript, so history has its own per-account budget: one export fits, a repeat does not,
// and an ordinary read of the newest page is nowhere near it.
func TestRepeatedExportIsRefused(t *testing.T) {
	limits := auth.DefaultLimits()
	limits.HistoryPerMinute = 12
	a := newApp(t, appOpts{limits: &limits})
	alice, bob := a.user("alice"), a.user("bobby")
	chat, bobChat := alice.newChat(), bob.newChat()
	seedTurns(t, a, alice, chat, 500) // 1000 messages: 10 pages of 100
	seedTurns(t, a, bob, bobChat, 500)

	export := func(p *person, id string) (pages int, last int) {
		cursor := ""
		for {
			w := p.get("/api/chats/" + id + "/messages?limit=100" + cursor)
			if w.Code != 200 {
				return pages, w.Code
			}
			pages++
			page := decodeBody[historyPage](t, w)
			if page.NextCursor == "" {
				return pages, 200
			}
			cursor = "&cursor=" + url.QueryEscape(page.NextCursor)
		}
	}
	if pages, code := export(alice, chat); pages != 10 || code != 200 {
		t.Fatalf("first export: %d pages, last status %d", pages, code)
	}
	pages, code := export(alice, chat)
	if code != 429 || pages >= 10 {
		t.Fatalf("repeat export: %d pages, last status %d, want it refused with 429 before finishing", pages, code)
	}
	requireLimit(t, alice.get("/api/chats/"+chat+"/messages"))
	if pages, code := export(bob, bobChat); pages != 10 || code != 200 {
		t.Fatalf("A's exports consumed B's budget: %d pages, %d", pages, code)
	}
}

func TestOrdinaryHistoryReadingIsNotLimitedByDefault(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	seedTurns(t, a, alice, chat, 200)
	for i := range 60 { // an hour of scrolling back and forth, in a minute
		if w := alice.get("/api/chats/" + chat + "/messages"); w.Code != 200 {
			t.Fatalf("read %d: %d %s", i, w.Code, w.Body.String())
		}
	}
}

func TestAssetReadRateLimit(t *testing.T) {
	limits := auth.DefaultLimits()
	limits.ArtifactsPerMinute = 1
	a := newApp(t, appOpts{limits: &limits})
	alice, bob := a.user("alice"), a.user("bobby")
	id := decodeBody[struct{ ID string }](t, alice.do("POST", "/api/assets", audioHdr, webmBody)).ID
	if w := alice.get("/api/assets/" + id); w.Code != 200 {
		t.Fatalf("read below limit: %d", w.Code)
	}
	requireLimit(t, alice.get("/api/assets/"+id))
	if w := bob.get("/api/assets/" + store.NewID()); w.Code != 404 {
		t.Fatalf("A consumed B's read quota: %d", w.Code)
	}
}
