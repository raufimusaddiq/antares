package chat

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

type historyPage struct {
	Messages   []hermes.Message `json:"messages"`
	NextCursor string           `json:"nextCursor"`
	Total      int              `json:"total"`
}

// seedTurns stores n user/assistant turns (2n messages) as the chat's Hermes transcript.
func seedTurns(t *testing.T, a *app, p *person, chatID string, turns int) []map[string]any {
	t.Helper()
	msgs := make([]map[string]any, 0, turns*2)
	for i := 0; i < turns; i++ {
		msgs = append(msgs,
			map[string]any{"id": fmt.Sprintf("m%05du", i), "role": "user", "content": fmt.Sprintf("question %d", i)},
			map[string]any{"id": fmt.Sprintf("m%05da", i), "role": "assistant", "content": fmt.Sprintf("answer %d with some body text to give the page weight", i)})
	}
	a.hx.mu.Lock()
	a.hx.msgs[decodeSession(t, a, p, chatID)] = msgs
	a.hx.mu.Unlock()
	return msgs
}

func getPage(t *testing.T, p *person, chatID, query string) historyPage {
	t.Helper()
	w := p.get("/api/chats/" + chatID + "/messages" + query)
	if w.Code != 200 {
		t.Fatalf("history %q: %d %s", query, w.Code, w.Body.String())
	}
	return decodeBody[historyPage](t, w)
}

// HIST-003 / PERF-001: a 500-turn conversation opens with the newest page only;
// older pages come by cursor with no turn lost, repeated or reordered.
func TestHistoryOpensWithTheNewestPageOnlyAndPagesBackExactly(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	seedTurns(t, a, u, chat, 500) // 1000 messages

	w := u.get("/api/chats/" + chat + "/messages")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	first := decodeBody[historyPage](t, w)
	if len(first.Messages) != 50 || first.Total != 1000 || first.NextCursor == "" {
		t.Fatalf("first page: %d messages, total %d, cursor %q", len(first.Messages), first.Total, first.NextCursor)
	}
	if first.Messages[0].Seq != 950 || first.Messages[49].Seq != 999 || first.Messages[49].Text != "answer 499 with some body text to give the page weight" {
		t.Errorf("first page is not the newest 50: %d..%d", first.Messages[0].Seq, first.Messages[49].Seq)
	}
	if w.Body.Len() > 16<<10 {
		t.Errorf("opening a chat sent %d bytes; the whole conversation is %d messages", w.Body.Len(), first.Total)
	}

	seen := map[int]bool{}
	next := -1
	pages := 0
	page := first
	for {
		pages++
		for i, m := range page.Messages {
			if seen[m.Seq] {
				t.Fatalf("seq %d returned twice", m.Seq)
			}
			seen[m.Seq] = true
			if i > 0 && m.Seq != page.Messages[i-1].Seq+1 {
				t.Fatalf("page not contiguous at seq %d", m.Seq)
			}
			if m.ID != fmt.Sprintf("m%05d%s", m.Seq/2, map[int]string{0: "u", 1: "a"}[m.Seq%2]) {
				t.Fatalf("seq %d carries %q", m.Seq, m.ID)
			}
		}
		if next >= 0 && page.Messages[len(page.Messages)-1].Seq != next-1 {
			t.Fatalf("page does not end where the previous began (%d)", next)
		}
		next = page.Messages[0].Seq
		if page.NextCursor == "" {
			break
		}
		page = getPage(t, u, chat, "?cursor="+url.QueryEscape(page.NextCursor))
	}
	if pages != 20 || len(seen) != 1000 || next != 0 {
		t.Errorf("pages=%d seen=%d oldest=%d", pages, len(seen), next)
	}
}

func TestHistoryLimitIsServerBoundedAndValidated(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	seedTurns(t, a, u, chat, 200)
	if p := getPage(t, u, chat, "?limit=5000"); len(p.Messages) != 100 {
		t.Errorf("limit=5000 returned %d, want the 100 ceiling", len(p.Messages))
	}
	if p := getPage(t, u, chat, "?limit=7"); len(p.Messages) != 7 {
		t.Errorf("limit=7 returned %d", len(p.Messages))
	}
	for _, q := range []string{"?limit=0", "?limit=-3", "?limit=abc", "?cursor=%%%", "?cursor=" + url.QueryEscape("bm90LWEtY3Vyc29y")} {
		if w := u.get("/api/chats/" + chat + "/messages" + q); w.Code != 400 || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
}

// New turns arriving while the user scrolls back must not disturb older pages.
func TestHistoryCursorSurvivesNewerTurnsBeingAppended(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	seedTurns(t, a, u, chat, 60) // 120 messages
	first := getPage(t, u, chat, "?limit=50")
	a.hx.mu.Lock()
	sid := decodeSession(t, a, u, chat)
	a.hx.msgs[sid] = append(a.hx.msgs[sid], map[string]any{"id": "mnew1", "role": "user", "content": "new"}, map[string]any{"id": "mnew2", "role": "assistant", "content": "newer"})
	a.hx.mu.Unlock()
	second := getPage(t, u, chat, "?limit=50&cursor="+url.QueryEscape(first.NextCursor))
	if second.Messages[len(second.Messages)-1].Seq != first.Messages[0].Seq-1 || second.Total != 122 {
		t.Errorf("after an append the older page moved: ends at %d, first began at %d, total %d",
			second.Messages[len(second.Messages)-1].Seq, first.Messages[0].Seq, second.Total)
	}
	// The newest page now includes the new turns.
	if p := getPage(t, u, chat, "?limit=2"); p.Messages[1].Text != "newer" {
		t.Errorf("newest page: %+v", p.Messages)
	}
}

// A cursor that no longer matches the transcript (compaction, rewind) is a
// visible HISTORY_CHANGED, never a page that quietly disagrees with the last.
func TestHistoryCursorMismatchIsVisible(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat, other := u.newChat(), u.newChat()
	seedTurns(t, a, u, chat, 80)
	seedTurns(t, a, u, other, 80)
	first := getPage(t, u, chat, "?limit=50")
	q := "?cursor=" + url.QueryEscape(first.NextCursor)

	// The same cursor on another of the user's own chats.
	if w := u.get("/api/chats/" + other + "/messages" + q); w.Code != 409 || errCode(t, w) != "HISTORY_CHANGED" {
		t.Errorf("cursor reused on another chat: %d %s", w.Code, w.Body.String())
	}
	// The boundary turn is rewritten upstream.
	a.hx.mu.Lock()
	sid := decodeSession(t, a, u, chat)
	a.hx.msgs[sid][110]["content"] = "edited upstream"
	a.hx.mu.Unlock()
	if w := u.get("/api/chats/" + chat + "/messages" + q); w.Code != 409 || errCode(t, w) != "HISTORY_CHANGED" {
		t.Errorf("rewritten boundary: %d %s", w.Code, w.Body.String())
	}
	// The transcript shrank below the cursor.
	a.hx.mu.Lock()
	a.hx.msgs[sid] = a.hx.msgs[sid][:20]
	a.hx.mu.Unlock()
	if w := u.get("/api/chats/" + chat + "/messages" + q); w.Code != 409 {
		t.Errorf("shrunk transcript: %d %s", w.Code, w.Body.String())
	}
	// Recovery is the newest page.
	if p := getPage(t, u, chat, ""); len(p.Messages) != 20 {
		t.Errorf("reload: %d messages", len(p.Messages))
	}
}

// PRD §5.6 / rule 3: another user's cursor, chat id or transcript is never reachable.
func TestHistoryIsOwnerScopedIncludingCursors(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chatA, chatB := alice.newChat(), bob.newChat()
	seedTurns(t, a, alice, chatA, 80)
	seedTurns(t, a, bob, chatB, 80)
	first := getPage(t, alice, chatA, "?limit=50")
	before := len(a.hx.requests())

	for _, path := range []string{
		"/api/chats/" + chatA + "/messages",
		"/api/chats/" + chatA + "/messages?cursor=" + url.QueryEscape(first.NextCursor),
		"/api/chats/" + chatA + "/messages?limit=100",
	} {
		if w := bob.get(path); w.Code != 404 || errCode(t, w) != "NOT_FOUND" || strings.Contains(w.Body.String(), "question") {
			t.Errorf("bob %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if n := len(a.hx.requests()); n != before {
		t.Errorf("a foreign history read made %d Hermes request(s)", n-before)
	}
	// Alice's cursor on Bob's chat id: Bob's chat is Bob's, Alice gets 404.
	if w := alice.get("/api/chats/" + chatB + "/messages?cursor=" + url.QueryEscape(first.NextCursor)); w.Code != 404 {
		t.Errorf("alice with her cursor on bob's chat: %d", w.Code)
	}
	// Bob's own page never contains Alice's text, whatever cursor he forges.
	forged := encodeHistoryCursor(chatB, 40, hermes.Message{Role: "user", Text: "x"})
	if w := bob.get("/api/chats/" + chatB + "/messages?cursor=" + url.QueryEscape(forged)); w.Code != 409 {
		t.Errorf("forged cursor: %d", w.Code)
	}
	// A suspended account reads nothing (default deny), cursor or not.
	if _, err := a.db.Exec(`UPDATE users SET approval_status='SUSPENDED' WHERE id = ?`, alice.id); err != nil {
		t.Fatal(err)
	}
	n := len(a.hx.requests())
	if w := alice.get("/api/chats/" + chatA + "/messages?cursor=" + url.QueryEscape(first.NextCursor)); w.Code == 200 {
		t.Errorf("suspended account read history: %d", w.Code)
	}
	if len(a.hx.requests()) != n {
		t.Error("a suspended account's history read reached Hermes")
	}
}

// Limits fail visibly. A transcript over the adapter's ceiling is an error,
// never a silently shortened conversation presented as complete.
func TestHistoryOverTheCeilingFailsInsteadOfTruncating(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	msgs := make([]map[string]any, 0, 20001)
	for i := 0; i < 20001; i++ {
		msgs = append(msgs, map[string]any{"id": fmt.Sprintf("m%d", i), "role": "user", "content": "x"})
	}
	a.hx.mu.Lock()
	a.hx.msgs[decodeSession(t, a, u, chat)] = msgs
	a.hx.mu.Unlock()
	w := u.get("/api/chats/" + chat + "/messages")
	if w.Code != 503 || errCode(t, w) != "HERMES_UNAVAILABLE" || strings.Contains(w.Body.String(), `"messages":[{`) {
		t.Errorf("over the ceiling: %d %.200s", w.Code, w.Body.String())
	}
}

func TestChatListPagesByCursorAndIsOwnerScoped(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	var mine []string
	for i := 0; i < 5; i++ {
		mine = append(mine, alice.newChat())
	}
	bobChat := bob.newChat()
	type listResp struct {
		Chats      []struct{ ID string }
		NextCursor string
	}
	var got []string
	cursor := ""
	for i := 0; i < 5; i++ {
		w := alice.get("/api/chats?limit=2&cursor=" + url.QueryEscape(cursor))
		l := decodeBody[listResp](t, w)
		for _, c := range l.Chats {
			got = append(got, c.ID)
		}
		if l.NextCursor == "" {
			break
		}
		if len(l.Chats) != 2 {
			t.Fatalf("short page with more to come: %d", len(l.Chats))
		}
		cursor = l.NextCursor
	}
	if len(got) != 5 {
		t.Fatalf("listed %d of 5 chats: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] || id == bobChat {
			t.Errorf("duplicate or foreign chat %s", id)
		}
		seen[id] = true
	}
	for _, id := range mine {
		if !seen[id] {
			t.Errorf("chat %s missing", id)
		}
	}
	if w := alice.get("/api/chats?cursor=garbage"); w.Code != 400 {
		t.Errorf("bad chat cursor: %d", w.Code)
	}
	if w := alice.get("/api/chats?limit=0"); w.Code != 400 {
		t.Errorf("limit=0: %d", w.Code)
	}
}

// TestMeasureOpenAndPageBack500TurnChat is the PERF-001 measurement for the
// server half: the newest-page and older-page requests for a 500-turn
// (1,000-message) conversation, through the real auth guard, handler and Hermes
// adapter against a local fake Hermes (so it is the app's cost, not a model's or
// a network's). Run it with
//
//	go test -run Measure500 -v -count=1 ./internal/chat
//
// It fails if the median is over a (deliberately loose) budget, so a regression
// to eager hydration cannot pass quietly.
func TestMeasureOpenAndPageBack500TurnChat(t *testing.T) {
	limits := auth.DefaultLimits() // 400 back-to-back reads would meet the history budget; this measures latency, not the budget
	limits.HistoryPerMinute = math.MaxInt
	a := newApp(t, appOpts{limits: &limits})
	u := a.user("alice")
	chat := u.newChat()
	seedTurns(t, a, u, chat, 500)
	measure := func(name, path string, wantBytes int) {
		const n = 200
		d := make([]time.Duration, 0, n)
		var size int
		for i := 0; i < n; i++ {
			t0 := time.Now()
			w := u.get(path)
			d = append(d, time.Since(t0))
			if w.Code != 200 {
				t.Fatalf("%s: %d", name, w.Code)
			}
			size = w.Body.Len()
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		p50, p95 := d[n/2], d[n*95/100]
		t.Logf("%-28s n=%d p50=%v p95=%v max=%v response=%d bytes", name, n, p50, p95, d[n-1], size)
		if size > wantBytes {
			t.Errorf("%s response is %d bytes (budget %d)", name, size, wantBytes)
		}
		if p50 > 200*time.Millisecond {
			t.Errorf("%s p50 %v is over the 200ms budget", name, p50)
		}
	}
	measure("open (newest 50 of 1000)", "/api/chats/"+chat+"/messages", 16<<10)
	cur := getPage(t, u, chat, "").NextCursor
	measure("page back (50 of 1000)", "/api/chats/"+chat+"/messages?cursor="+url.QueryEscape(cur), 16<<10)
}

// #171: history paging and index writes log their latency, with counts and durations only: no chat id, title,
// session or turn text reaches the log.
func TestHistoryAndIndexLatencyAreLoggedWithoutContent(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	c, err := store.GetChat(a.svc.base, a.db, u.id, chat)
	if err != nil {
		t.Fatal(err)
	}
	a.hx.mu.Lock()
	a.hx.msgs[c.HermesSessionID] = []map[string]any{{"role": "user", "content": "TELEMETRY-SECRET-77 question"}, {"role": "assistant", "content": "TELEMETRY-SECRET-77 answer"}}
	a.hx.mu.Unlock()
	before := len(logBuf.String())
	if w := u.get("/api/chats/" + chat + "/messages"); w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	got := logBuf.String()[before:]
	for _, want := range []string{"chat: history page fetch_ms=", "chat: search index write_ms="} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	for _, secret := range []string{"TELEMETRY-SECRET-77", chat, u.id} {
		if strings.Contains(got, secret) {
			t.Errorf("telemetry logged %q:\n%s", secret, got)
		}
	}
}
