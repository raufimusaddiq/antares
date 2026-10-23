package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

func limitRequest(h http.Handler, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, store.User{ID: id}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertLimited(t *testing.T, w *httptest.ResponseRecorder, known ...bool) {
	t.Helper()
	var body struct {
		Error struct {
			Code              string
			RetryAfterSeconds *int
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 429 || body.Error.Code != "RATE_LIMITED" {
		t.Fatalf("rate limit response: %d %s", w.Code, w.Body.String())
	}
	if len(known) > 0 && !known[0] {
		var raw map[string]map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		hint, exists := raw["error"]["retryAfterSeconds"]
		if !exists || hint != nil || w.Header().Get("Retry-After") != "" {
			t.Fatalf("unknown wait: %v %s", w.Header(), w.Body.String())
		}
		return
	}
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retry < 1 || body.Error.RetryAfterSeconds == nil || *body.Error.RetryAfterSeconds != retry {
		t.Fatalf("rate limit response: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
}

func TestRequestLimitsPerAccountAndWindow(t *testing.T) {
	for _, tc := range []struct {
		name, route string
		limits      Limits
	}{
		{"runs", "POST /api/chats/{chatId}/messages", Limits{RunsPerMinute: 2, MessagesPerMinute: 9, UploadsPerMinute: 9, SSEConnections: 9}},
		{"messages", "POST /api/chats/{chatId}/messages", Limits{RunsPerMinute: 9, MessagesPerMinute: 2, UploadsPerMinute: 9, SSEConnections: 9}},
		{"uploads", "POST /api/assets", Limits{RunsPerMinute: 9, MessagesPerMinute: 9, UploadsPerMinute: 2, SSEConnections: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			a := &Auth{now: func() time.Time { return now }, limits: newRequestLimits(tc.limits)}
			calls := 0
			h := a.limit(tc.route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
			for range 2 {
				if w := limitRequest(h, "a"); w.Code != 204 {
					t.Fatalf("early limit: %d", w.Code)
				}
			}
			assertLimited(t, limitRequest(h, "a"))
			if w := limitRequest(h, "b"); w.Code != 204 {
				t.Fatalf("A consumed B's quota: %d", w.Code)
			}
			now = now.Add(time.Minute - time.Nanosecond)
			assertLimited(t, limitRequest(h, "a"))
			now = now.Add(time.Nanosecond)
			if w := limitRequest(h, "a"); w.Code != 204 {
				t.Fatalf("window did not reset: %d", w.Code)
			}
			if calls != 4 {
				t.Fatalf("refused request reached handler: %d calls", calls)
			}
		})
	}
}

func TestRequestLimitsConcurrentAdmission(t *testing.T) {
	a := &Auth{now: time.Now, limits: newRequestLimits(Limits{RunsPerMinute: 7, MessagesPerMinute: 100, UploadsPerMinute: 100, SSEConnections: 2})}
	var admitted atomic.Int32
	h := a.limit("POST /api/chats/{chatId}/messages", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { admitted.Add(1); w.WriteHeader(204) }))
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			w := limitRequest(h, "a")
			if w.Code != 204 && w.Code != 429 {
				t.Errorf("status %d", w.Code)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 7 {
		t.Fatalf("admitted %d, want 7", admitted.Load())
	}
}

func TestSSEQuotaReleasedAfterHandlerReturns(t *testing.T) {
	a := &Auth{now: time.Now, limits: newRequestLimits(Limits{RunsPerMinute: 9, MessagesPerMinute: 9, UploadsPerMinute: 9, SSEConnections: 1})}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	h := a.limit("GET /api/runs/{runId}/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }))
	var wg sync.WaitGroup
	wg.Go(func() { limitRequest(h, "a") })
	<-entered
	assertLimited(t, limitRequest(h, "a"), false)
	wg.Go(func() { limitRequest(h, "b") })
	<-entered
	close(release)
	wg.Wait()
	if w := limitRequest(h, "a"); w.Code != 204 {
		t.Fatalf("slot leaked: %d", w.Code)
	}
}

func TestRequestLimitsBoundedAndEvicted(t *testing.T) {
	now := time.Unix(100, 0)
	a := &Auth{now: func() time.Time { return now }, limits: newRequestLimits(DefaultLimits())}
	h := a.limit("POST /api/assets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := range maxLimitAccounts {
		if w := limitRequest(h, strconv.Itoa(i)); w.Code != 204 {
			t.Fatalf("account %d: %d", i, w.Code)
		}
	}
	assertLimited(t, limitRequest(h, "overflow"), false)
	if len(a.limits.accounts) != maxLimitAccounts {
		t.Fatalf("map grew to %d", len(a.limits.accounts))
	}
	now = now.Add(time.Minute)
	if w := limitRequest(h, "new"); w.Code != 204 {
		t.Fatalf("expired counters not evicted: %d", w.Code)
	}
	if len(a.limits.accounts) != 1 {
		t.Fatalf("retained expired accounts: %d", len(a.limits.accounts))
	}
}

func TestSSEQuotaSurvivesCounterExpiry(t *testing.T) {
	limits := DefaultLimits()
	limits.SSEConnections = 1
	l := newRequestLimits(limits)
	now := time.Unix(100, 0)
	if retry, release := l.acquire("a", "GET /api/runs/{runId}/events", now); retry != 0 {
		t.Fatalf("first stream denied: %d", retry)
	} else {
		now = now.Add(2 * time.Minute)
		if retry, _ := l.acquire("b", "POST /api/assets", now); retry != 0 {
			t.Fatal("B denied")
		}
		if retry, _ := l.acquire("a", "GET /api/runs/{runId}/events", now); retry != -1 {
			t.Fatalf("active stream expired: retry %d", retry)
		}
		release()
	}
	if retry, release := l.acquire("a", "GET /api/runs/{runId}/events", now); retry != 0 {
		t.Fatal("released slot still occupied")
	} else {
		release()
	}
}

func TestRateLimitedEnvelope(t *testing.T) {
	for _, retry := range []int{12, 0, -1} {
		w := httptest.NewRecorder()
		w.Header().Set("Retry-After", "999")
		WriteRateLimited(w, "test limit", retry)
		assertLimited(t, w, retry > 0)
		var body map[string]map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		e := body["error"]
		requestID, ok := e["requestId"].(string)
		if len(e) != 4 || e["message"] != "test limit" || !ok || requestID == "" {
			t.Fatalf("wrong envelope: %s", w.Body.String())
		}
		if retry > 0 && w.Header().Get("Retry-After") != strconv.Itoa(retry) {
			t.Fatalf("retry hint: %v", w.Header())
		}
	}
}
