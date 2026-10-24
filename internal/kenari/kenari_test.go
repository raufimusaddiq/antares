package kenari

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// testdata/models.json is a trimmed copy of the real https://kenari.id/v1/models answer
// (read 2026-10-24, unauthenticated): one model of each published modality combination.
func fixtureServer(t *testing.T, hits *atomic.Int32, fail *atomic.Bool) *Catalogue {
	t.Helper()
	body, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("catalogue request carried credentials")
		}
		if fail.Load() {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, nil)
}

func TestLookupRealModalities(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	ctx := context.Background()
	cases := []struct {
		id                                  string
		image, outImage, outAudio, outVideo bool
	}{
		{"deepseek-v4-1-flash", true, false, false, false}, // the chat model Hermes is configured with
		{"beam-1-0:free", false, false, false, false},      // text + file only: no vision
		{"gemini-3-1-flash-tts", false, false, true, false},
		{"kling-v3", true, false, false, true},
		{"grok-imagine-image", false, true, false, false},
	}
	for _, tc := range cases {
		m, found, err := c.Lookup(ctx, tc.id)
		if err != nil || !found {
			t.Fatalf("%s: found=%v err=%v", tc.id, found, err)
		}
		if m.AcceptsInput("image") != tc.image || m.Produces("image") != tc.outImage || m.Produces("audio") != tc.outAudio || m.Produces("video") != tc.outVideo {
			t.Errorf("%s: %+v", tc.id, m)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("catalogue fetched %d times, want 1 (cached)", hits.Load())
	}
}

func TestUnlistedModelIsUnknownNotTextOnly(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	m, found, err := c.Lookup(context.Background(), "not-a-kenari-model")
	if err != nil || found || m.AcceptsInput("image") {
		t.Fatalf("m=%+v found=%v err=%v", m, found, err)
	}
}

func TestOutageUsesRecentAnswerThenFailsClosed(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if _, _, err := c.Lookup(ctx, "deepseek-v4-1-flash"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	now = now.Add(freshFor + time.Minute)
	if m, found, err := c.Lookup(ctx, "deepseek-v4-1-flash"); err != nil || !found || !m.AcceptsInput("image") {
		t.Fatalf("recent answer not served during outage: %v %v", found, err)
	}
	now = now.Add(staleFor)
	if _, _, err := c.Lookup(ctx, "deepseek-v4-1-flash"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestNeverFollowsRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect followed")
	}))
	defer target.Close()
	srv := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer srv.Close()
	if _, _, err := New(srv.URL, nil).Lookup(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
