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

// The picker's metadata is read from the same real answer; what Kenari does not publish stays nil.
func TestListRealPickerMetadata(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	ctx := context.Background()
	ds, found, err := c.Get(ctx, "deepseek-v4-1-flash")
	if err != nil || !found || !ds.Chat() || ds.ContextLength == nil || *ds.ContextLength != 1000000 || ds.Reasoning == nil || !*ds.Reasoning {
		t.Fatalf("deepseek: %+v %v %v", ds, found, err)
	}
	if got := ds.ReasoningLevels; len(got) != 4 || got[0] != "none" || got[3] != "max" {
		t.Fatalf("levels: %v", got)
	}
	if p := ds.Pricing; p == nil || p.Currency != "IDR" || p.Input == nil || *p.Input != 2750000000 || p.Free {
		t.Fatalf("pricing: %+v", p)
	}
	beam, _, _ := c.Get(ctx, "beam-1-0:free")
	if beam.Pricing == nil || !beam.Pricing.Free || beam.Pricing.Input != nil || beam.Reasoning != nil || beam.ReasoningLevels != nil {
		t.Fatalf("an unpublished value must stay nil: %+v", beam)
	}
	if img, _, _ := c.Get(ctx, "agnes-image-2-0-flash:free"); img.Chat() {
		t.Fatal("an images-endpoint model is not a chat model")
	}
	all, err := c.List(ctx)
	if err != nil || len(all) != 19 || all[0].ID != "deepseek-v4-1-flash" {
		t.Fatalf("list keeps Kenari's order: %d %v", len(all), err)
	}
	if _, found, err := c.Get(ctx, "nope"); found || err != nil {
		t.Fatal("an unlisted id is not found, and that is not an outage")
	}
}

// A row with no modalities is still listed, but Lookup keeps treating its modalities as unknown.
func TestRowWithoutModalitiesIsListedButUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"bare","endpoints":["chat"],"context_length":8192.5,"sunset_at":"2030-01-01T00:00:00Z"}]}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, nil)
	if _, found, err := c.Lookup(context.Background(), "bare"); found || err != nil {
		t.Fatalf("modalities must stay unknown: %v %v", found, err)
	}
	m, found, _ := c.Get(context.Background(), "bare")
	if !found || m.HasModalities || m.SunsetAt != "2030-01-01T00:00:00Z" || m.ContextLength == nil {
		t.Fatalf("%+v", m)
	}
}

// #145: the picker's refresh must reach Kenari, not only Hermes. A forced read bypasses the ten-minute
// cache, but never faster than refreshEvery, so a client opening the picker in a loop cannot hammer Kenari.
func TestRefreshReadsKenariAgainButIsThrottled(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if _, _, _, err := c.Snapshot(ctx, false); err != nil || hits.Load() != 1 {
		t.Fatalf("first read: err %v hits %d", err, hits.Load())
	}
	now = now.Add(time.Second)
	if _, _, _, err := c.Snapshot(ctx, true); err != nil || hits.Load() != 1 {
		t.Fatalf("a refresh right after a read must be served from it: err %v hits %d", err, hits.Load())
	}
	now = now.Add(refreshEvery)
	if _, at, _, err := c.Snapshot(ctx, true); err != nil || hits.Load() != 2 || !at.Equal(now) {
		t.Fatalf("a refresh past the throttle must re-read Kenari: err %v hits %d at %v want %v", err, hits.Load(), at, now)
	}
	now = now.Add(refreshEvery)
	if _, _, _, err := c.Snapshot(ctx, false); err != nil || hits.Load() != 2 {
		t.Fatalf("a plain read inside the ten minutes must use the cache: err %v hits %d", err, hits.Load())
	}
}

// #145: an answer served during an outage says how old it is and that it is stale; a fresh one says neither.
func TestSnapshotSaysWhenTheAnswerIsStale(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	start := time.Now()
	now := start
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if _, at, stale, err := c.Snapshot(ctx, false); err != nil || stale || !at.Equal(start) {
		t.Fatalf("fresh: err %v stale %v at %v", err, stale, at)
	}
	fail.Store(true)
	now = start.Add(freshFor + time.Minute)
	if _, at, stale, err := c.Snapshot(ctx, false); err != nil || !stale || !at.Equal(start) {
		t.Fatalf("outage: want the old answer marked stale with its own time, got err %v stale %v at %v", err, stale, at)
	}
	// A failed forced refresh keeps the old answer and says so; it does not turn into an error.
	now = now.Add(refreshEvery)
	if models, _, stale, err := c.Snapshot(ctx, true); err != nil || !stale || len(models) == 0 {
		t.Fatalf("failed refresh: err %v stale %v rows %d", err, stale, len(models))
	}
}

// #145 review: failed attempts are throttled too, and the failed state outlives the attempt.
func TestOutageRefreshesAreThrottledAndStaleSticks(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	c := fixtureServer(t, &hits, &fail)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if _, _, _, err := c.Snapshot(ctx, false); err != nil || hits.Load() != 1 {
		t.Fatalf("first read: %v %d", err, hits.Load())
	}
	fail.Store(true)
	now = now.Add(refreshEvery + time.Second)
	if _, _, stale, err := c.Snapshot(ctx, true); err != nil || !stale || hits.Load() != 2 {
		t.Fatalf("failed refresh: err %v stale %v hits %d", err, stale, hits.Load())
	}
	for _, refresh := range []bool{true, false, true} {
		if _, _, stale, err := c.Snapshot(ctx, refresh); err != nil || !stale || hits.Load() != 2 {
			t.Fatalf("immediately after a failure (refresh=%v): err %v stale %v hits %d, want stale and no new upstream hit", refresh, err, stale, hits.Load())
		}
	}
	now = now.Add(refreshEvery)
	if _, _, stale, _ := c.Snapshot(ctx, true); !stale || hits.Load() != 3 {
		t.Fatalf("after the throttle: stale %v hits %d", stale, hits.Load())
	}
	fail.Store(false)
	now = now.Add(refreshEvery)
	if _, _, stale, err := c.Snapshot(ctx, true); err != nil || stale || hits.Load() != 4 {
		t.Fatalf("recovery: err %v stale %v hits %d", err, stale, hits.Load())
	}
}

// Production outage 2026-11-20: one row carried sunset_at as a number and took the whole catalogue down.
func TestOddRowsNeverTakeTheCatalogueDown(t *testing.T) {
	body, err := os.ReadFile("testdata/models_odd_rows.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	c := New(srv.URL, nil)
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		id     string
		listed bool
		sunset string
		pastAt time.Time // when the model counts as sunset; zero = never
		bad    bool
	}{
		{"sunset-number", true, "2026-11-23T12:00:00Z", time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), false},
		{"sunset-string", true, "2030-01-01T00:00:00Z", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"sunset-null", true, "", time.Time{}, false},
		{"sunset-absent", true, "", time.Time{}, false},
		{"sunset-garbage-type", true, "", now, true},
		{"sunset-garbage-string", true, "", now, true},
		{"bad-endpoints", false, "", time.Time{}, false},
		{"bad-context", false, "", time.Time{}, false},
		{"good-after-bad", true, "", time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			m, found, err := c.Get(context.Background(), tc.id)
			if err != nil {
				t.Fatalf("catalogue must stay available: %v", err)
			}
			if found != tc.listed {
				t.Fatalf("listed = %v, want %v", found, tc.listed)
			}
			if !found {
				return
			}
			if m.SunsetAt != tc.sunset || m.SunsetInvalid != tc.bad {
				t.Fatalf("sunset = %q invalid=%v, want %q invalid=%v", m.SunsetAt, m.SunsetInvalid, tc.sunset, tc.bad)
			}
			if got := m.PastSunset(now); got != (!tc.pastAt.IsZero() && !tc.pastAt.After(now)) {
				t.Fatalf("PastSunset(now) = %v", got)
			}
			if !tc.pastAt.IsZero() && !m.PastSunset(tc.pastAt) {
				t.Fatalf("not sunset at %v", tc.pastAt)
			}
		})
	}
}

func TestUnreadableBodyStillMakesCatalogueUnavailable(t *testing.T) {
	for name, body := range map[string]string{"not json": "<html>", "no data": `{"object":"list"}`, "data not array": `{"data":5}`} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			t.Cleanup(srv.Close)
			if _, err := New(srv.URL, nil).List(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
