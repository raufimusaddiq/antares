package chat

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

type searchResp struct {
	Results []struct {
		ChatID    string    `json:"chatId"`
		ChatTitle string    `json:"chatTitle"`
		Kind      string    `json:"kind"`
		Turn      *int      `json:"turn"`
		Role      string    `json:"role"`
		CreatedAt string    `json:"createdAt"`
		Snippet   []Segment `json:"snippet"`
	} `json:"results"`
	NextCursor string `json:"nextCursor"`
	Index      struct {
		Complete bool `json:"complete"`
		Chats    int  `json:"chats"`
		Pending  int  `json:"pending"`
		Failed   int  `json:"failed"`
	} `json:"index"`
}

func (a *app) setTranscript(t *testing.T, p *person, chatID string, msgs ...[2]string) {
	t.Helper()
	var list []map[string]any
	for _, m := range msgs {
		list = append(list, map[string]any{"role": m[0], "content": m[1]})
	}
	a.hx.mu.Lock()
	a.hx.msgs[decodeSession(t, a, p, chatID)] = list
	a.hx.mu.Unlock()
}

func doSearch(t *testing.T, p *person, path string, code int) searchResp {
	t.Helper()
	w := p.get(path)
	if w.Code != code {
		t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
	}
	if code != 200 {
		return searchResp{}
	}
	return decodeBody[searchResp](t, w)
}

func joined(segs []Segment) (all string, hits []string) {
	for _, s := range segs {
		all += s.Text
		if s.Hit {
			hits = append(hits, s.Text)
		}
	}
	return
}

// UXP-003 / UXP-ACC-005: B's text is in the database, and A never sees it, by query,
// by guessed chat id, by cursor, or by scoped route.
func TestSearchIsOwnerScopedEndToEnd(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bob")
	ca, cb := alice.newChat(), bob.newChat()
	a.setTranscript(t, alice, ca, [2]string{"user", "plan the shared launch"}, [2]string{"assistant", "alice launch notes"})
	a.setTranscript(t, bob, cb, [2]string{"user", "bobs SECRETLAUNCH codename"}, [2]string{"assistant", "launch of SECRETLAUNCH approved"})

	// Bob's text only reaches the database once he searches (or opens the chat); do both.
	doSearch(t, bob, "/api/search/chats?q=secretlaunch", 200)
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM search_docs WHERE folded LIKE '%secretlaunch%'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("B's text must be in the database for this test to mean anything: %d %v", n, err)
	}

	r := doSearch(t, alice, "/api/search/chats?q=launch", 200)
	if len(r.Results) != 2 {
		t.Fatalf("alice's own hits: %+v", r.Results)
	}
	for _, x := range r.Results {
		if x.ChatID != ca {
			t.Errorf("alice got a foreign chat id %s", x.ChatID)
		}
		if s, _ := joined(x.Snippet); strings.Contains(strings.ToLower(s), "secret") {
			t.Errorf("foreign text in a snippet: %q", s)
		}
	}
	if r := doSearch(t, alice, "/api/search/chats?q=secretlaunch", 200); len(r.Results) != 0 || r.NextCursor != "" {
		t.Errorf("alice found bob's text: %+v", r)
	}
	// Naming bob's chat is a 404, the same as a chat that does not exist: no count, no snippet, no existence.
	for _, path := range []string{
		"/api/search/chats?q=secretlaunch&chatId=" + cb,
		"/api/chats/" + cb + "/search?q=secretlaunch",
		"/api/chats/" + strings.Repeat("0", 32) + "/search?q=secretlaunch",
	} {
		w := alice.get(path)
		if w.Code != 404 || errCode(t, w) != "NOT_FOUND" || strings.Contains(strings.ToLower(w.Body.String()), "secret") || strings.Contains(w.Body.String(), cb) {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// A cursor taken from bob's search is only a position in alice's own list.
	rb := doSearch(t, bob, "/api/search/chats?q=secretlaunch&limit=1", 200)
	if rb.NextCursor == "" {
		t.Fatal("expected a second page for bob")
	}
	if r := doSearch(t, alice, "/api/search/chats?q=secretlaunch&cursor="+url.QueryEscape(rb.NextCursor), 200); len(r.Results) != 0 {
		t.Errorf("bob's cursor revealed something to alice: %+v", r.Results)
	}
	// ...and it is not a position under any other query.
	if w := alice.get("/api/search/chats?q=launch&cursor=" + url.QueryEscape(rb.NextCursor)); w.Code != 400 {
		t.Errorf("bob's cursor under another query: %d", w.Code)
	}
}

func TestSearchRefusesWhatTheServerAlonePicks(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	for _, q := range []string{
		"?q=hello&profile=u_x", "?q=hello&apiKey=k", "?q=hello&sessionId=s", "?q=hello&X-Hermes-Profile=p",
		"?q=", "?q=a", "?q=" + strings.Repeat("x", 201), "?q=a+b+c+d+e+f+g", "?q=hello&limit=0", "?q=hello&role=system",
		"?q=hello&cursor=%25%25", "?q=hello&cursor=" + url.QueryEscape("bm90LWEtY3Vyc29y"), "", "?q=%ff%fe",
	} {
		if w := u.get("/api/search/chats" + q); w.Code != 400 || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("global %q: %d %s", q, w.Code, w.Body.String())
		}
	}
	// The path already names the chat; naming another in the query is a foreign parameter.
	if w := u.get("/api/chats/" + chat + "/search?q=hello&chatId=" + chat); w.Code != 400 {
		t.Errorf("scoped with chatId: %d", w.Code)
	}
}

func TestSearchRequiresAHealthyTenantAndSession(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	if _, err := a.db.Exec(`UPDATE tenant_bindings SET status = 'FAILED' WHERE user_id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	if w := u.get("/api/search/chats?q=hello"); w.Code != 409 || errCode(t, w) != "TENANT_NOT_READY" {
		t.Errorf("unhealthy binding: %d %s", w.Code, w.Body.String())
	}
	anon := &person{a: a}
	if w := anon.get("/api/search/chats?q=hello"); w.Code != 401 {
		t.Errorf("no session: %d", w.Code)
	}
}

// Highlights are text segments: markup in a message is returned as characters, and the
// matched span is marked at the right place, including after non-ASCII text.
func TestSearchHighlightsMatchedSpanAndNeverEmitsMarkup(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	a.setTranscript(t, u, chat,
		[2]string{"user", `Hubungi Ünal <img src=x onerror=alert(1)> sekarang`},
		[2]string{"assistant", "<script>alert('ÜNAL')</script> done"})
	r := doSearch(t, u, "/api/search/chats?q=%C3%9CNAL", 200) // ÜNAL, upper case
	if len(r.Results) != 2 {
		t.Fatalf("hits: %+v", r.Results)
	}
	for _, x := range r.Results {
		all, hits := joined(x.Snippet)
		if len(hits) != 1 || (hits[0] != "Ünal" && hits[0] != "ÜNAL") {
			t.Errorf("marked span %q in %q", hits, all)
		}
	}
	all, _ := joined(r.Results[1].Snippet) // turn 0, the one with the <img>
	if !strings.Contains(all, "<img src=x onerror=alert(1)>") {
		t.Errorf("markup must come back as plain characters: %q", all)
	}
	if got := r.Results[1].Turn; got == nil || *got != 0 {
		t.Errorf("turn anchor: %v", got)
	}
}

func TestSearchResultsCarryChatTurnAndRoleFilter(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	c1, c2 := u.newChat(), u.newChat()
	a.setTranscript(t, u, c1, [2]string{"user", "kopi hitam"}, [2]string{"assistant", "kopi susu"}, [2]string{"user", "teh"})
	a.setTranscript(t, u, c2, [2]string{"user", "kopi tubruk"})
	r := doSearch(t, u, "/api/search/chats?q=kopi", 200)
	if len(r.Results) != 3 || !r.Index.Complete || r.Index.Chats != 2 {
		t.Fatalf("%+v", r)
	}
	// chat 2 was created last, so it is the most recent; inside chat 1 the newest turn leads.
	if r.Results[0].ChatID != c2 || *r.Results[0].Turn != 0 || *r.Results[1].Turn != 1 || *r.Results[2].Turn != 0 || r.Results[1].ChatID != c1 {
		t.Errorf("order/anchors: %+v", r.Results)
	}
	if r := doSearch(t, u, "/api/search/chats?q=kopi&role=assistant", 200); len(r.Results) != 1 || r.Results[0].Role != "assistant" {
		t.Errorf("role filter: %+v", r.Results)
	}
	if r := doSearch(t, u, "/api/chats/"+c1+"/search?q=kopi", 200); len(r.Results) != 2 {
		t.Errorf("find in chat: %+v", r.Results)
	}
}

func TestSearchFindsTitlesAsTitleHitsWithNoTurn(t *testing.T) {
	a := newApp(t)
	u, other := a.user("alice"), a.user("bob")
	w := u.do("POST", "/api/chats", nil, map[string]string{"title": "Rencana Ünal"})
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	other.do("POST", "/api/chats", nil, map[string]string{"title": "Rencana Rahasia"})
	r := doSearch(t, u, "/api/search/chats?q=%C3%BCnal+RENCANA", 200)
	if len(r.Results) != 1 || r.Results[0].Kind != "title" || r.Results[0].Turn != nil || r.Results[0].Role != "title" || r.Results[0].ChatTitle != "Rencana Ünal" {
		t.Fatalf("%+v", r.Results)
	}
	if r := doSearch(t, u, "/api/search/chats?q=rahasia", 200); len(r.Results) != 0 {
		t.Errorf("another user's title: %+v", r.Results)
	}
	if r := doSearch(t, u, "/api/search/chats?q=rencana&role=user", 200); len(r.Results) != 0 {
		t.Errorf("role filter must exclude titles: %+v", r.Results)
	}
}

func TestSearchEmptyAndNoMatchAreHonest(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	r := doSearch(t, u, "/api/search/chats?q=anything", 200)
	if r.Results == nil || len(r.Results) != 0 || r.Index.Chats != 0 || !r.Index.Complete {
		t.Errorf("no chats: %+v", r)
	}
	chat := u.newChat()
	a.setTranscript(t, u, chat, [2]string{"user", "hello"})
	if r := doSearch(t, u, "/api/search/chats?q=zebra", 200); len(r.Results) != 0 || !r.Index.Complete {
		t.Errorf("no match: %+v", r)
	}
}

// A partial index is labelled as one: more stale chats than one search reads leave the rest pending.
func TestSearchReportsAPartialIndexAndCatchesUp(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	var chats []string
	for i := 0; i < 5; i++ {
		c := u.newChat()
		a.setTranscript(t, u, c, [2]string{"user", "needle in chat"})
		chats = append(chats, c)
	}
	r := doSearch(t, u, "/api/search/chats?q=needle", 200)
	if r.Index.Complete || r.Index.Pending != 2 || len(r.Results) != 3 {
		t.Fatalf("first search must say it is partial: %+v", r.Index)
	}
	r = doSearch(t, u, "/api/search/chats?q=needle", 200)
	if !r.Index.Complete || len(r.Results) != 5 {
		t.Fatalf("second search catches up: %+v %d", r.Index, len(r.Results))
	}
}

func TestSearchSeesTurnsAddedByLaterRuns(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	a.setTranscript(t, u, chat, [2]string{"user", "first question"})
	if r := doSearch(t, u, "/api/search/chats?q=question", 200); len(r.Results) != 1 {
		t.Fatalf("%+v", r)
	}
	// A run ends in the chat and the transcript gains a turn: the index is stale, the next search re-reads.
	a.setTranscript(t, u, chat, [2]string{"user", "first question"}, [2]string{"assistant", "the answer to the question"})
	if err := store.InvalidateChatIndex(t.Context(), a.db, u.id, chat, a.svc.now()); err != nil {
		t.Fatal(err)
	}
	r := doSearch(t, u, "/api/search/chats?q=question", 200)
	if len(r.Results) != 2 || !r.Index.Complete {
		t.Fatalf("%+v", r)
	}
	// Compaction/rewind: a turn that left the transcript leaves the index.
	a.setTranscript(t, u, chat, [2]string{"user", "something else"})
	_ = store.InvalidateChatIndex(t.Context(), a.db, u.id, chat, a.svc.now())
	if r := doSearch(t, u, "/api/search/chats?q=question", 200); len(r.Results) != 0 {
		t.Errorf("removed turns are still searchable: %+v", r.Results)
	}
}

func transcriptReads(a *app) int {
	n := 0
	for _, r := range a.hx.requests() {
		if r.Method == "GET" && strings.Contains(r.Path, "/api/sessions/") && strings.Contains(r.Path, "/messages") {
			n++
		}
	}
	return n
}

// Opening a chat indexes it from the transcript that read already fetched: the following search
// finds the text without a second transcript read.
func TestOpeningAChatIndexesItAndSearchReadsNothingMore(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	a.setTranscript(t, u, chat, [2]string{"user", "tentang rencana peluncuran"}, [2]string{"assistant", "jadwal peluncuran disetujui"})
	if n := transcriptReads(a); n != 0 {
		t.Fatalf("setup read the transcript %d times", n)
	}
	if w := u.get("/api/chats/" + chat + "/messages"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if n := transcriptReads(a); n != 1 {
		t.Fatalf("opening read the transcript %d times", n)
	}
	r := doSearch(t, u, "/api/search/chats?q=peluncuran", 200)
	if len(r.Results) != 2 || !r.Index.Complete || r.Index.Pending != 0 {
		t.Fatalf("hits after opening: %+v", r)
	}
	if n := transcriptReads(a); n != 1 {
		t.Errorf("search re-read an index that opening had just filled: %d reads", n)
	}
	// Opening again, with the index fresh, writes nothing and reads once more only for the page itself.
	u.get("/api/chats/" + chat + "/messages")
	if n := transcriptReads(a); n != 2 {
		t.Errorf("second open: %d reads", n)
	}
	// Another user opening their own chat does not index A's.
	b := a.user("bob")
	cb := b.newChat()
	a.setTranscript(t, b, cb, [2]string{"user", "peluncuran rahasia"})
	b.get("/api/chats/" + cb + "/messages")
	if r := doSearch(t, u, "/api/search/chats?q=rahasia", 200); len(r.Results) != 0 {
		t.Errorf("%+v", r.Results)
	}
}

func TestSearchCursorBelongsToItsQuery(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	a.setTranscript(t, u, chat, [2]string{"user", "kopi hitam"}, [2]string{"assistant", "kopi susu"}, [2]string{"user", "teh manis"}, [2]string{"assistant", "teh tawar"})
	first := doSearch(t, u, "/api/search/chats?q=kopi&limit=1", 200)
	if first.NextCursor == "" {
		t.Fatal("no cursor")
	}
	c := url.QueryEscape(first.NextCursor)
	if r := doSearch(t, u, "/api/search/chats?q=kopi&limit=1&cursor="+c, 200); len(r.Results) != 1 {
		t.Errorf("same query: %+v", r.Results)
	}
	for _, other := range []string{"q=teh&limit=1&cursor=" + c, "q=kopi&role=user&limit=1&cursor=" + c, "q=kopi&chatId=" + chat + "&limit=1&cursor=" + c} {
		if w := u.get("/api/search/chats?" + other); w.Code != 400 || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", other, w.Code, w.Body.String())
		}
	}
	if w := u.get("/api/chats/" + chat + "/search?q=kopi&cursor=" + c); w.Code != 400 {
		t.Errorf("a global cursor under the per-chat route: %d", w.Code)
	}
}

func TestSearchRefusesOneLetterWordsExceptInDenseScripts(t *testing.T) {
	for _, q := range []string{"a b", "x y", "ab c", "kopi s"} {
		if _, ok := parseSearchTerms(q); ok {
			t.Errorf("%q accepted", q)
		}
	}
	for _, q := range []string{"ab", "kopi susu", "猫", "猫 犬", "ありがとう"} {
		if _, ok := parseSearchTerms(q); !ok {
			t.Errorf("%q refused", q)
		}
	}
	if _, ok := parseSearchTerms("a"); ok {
		t.Error("a single letter accepted")
	}
}

func TestSearchResultsCarryATimestamp(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	a.hx.mu.Lock()
	a.hx.msgs[decodeSession(t, a, u, chat)] = []map[string]any{{"role": "user", "content": "kopi", "created_at": "2026-10-20T08:30:00Z"}}
	a.hx.mu.Unlock()
	r := doSearch(t, u, "/api/search/chats?q=kopi", 200)
	if len(r.Results) != 1 || r.Results[0].CreatedAt != "2026-10-20T08:30:00Z" {
		t.Errorf("message hit: %+v", r.Results)
	}
	w := u.do("POST", "/api/chats", nil, map[string]string{"title": "Judul kopi"})
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	r = doSearch(t, u, "/api/search/chats?q=judul&role=title", 200)
	if len(r.Results) != 1 || r.Results[0].CreatedAt == "" {
		t.Errorf("title hit has no timestamp: %+v", r.Results)
	}
}

// A chat whose transcript cannot be read is not retried by every later search, so it cannot use up
// the read budget; it is reported as failed, not as still indexing.
func TestSearchBacksOffAChatItCannotRead(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	a.setTranscript(t, u, chat, [2]string{"user", "kopi"})
	a.hx.mu.Lock()
	a.hx.keys[u.profile] = "a-different-key-than-the-bound-one" // Hermes now refuses this profile
	a.hx.mu.Unlock()
	r := doSearch(t, u, "/api/search/chats?q=kopi", 200)
	if r.Index.Complete || r.Index.Failed != 1 || r.Index.Pending != 0 {
		t.Fatalf("%+v", r.Index)
	}
	reads := transcriptReads(a)
	if reads == 0 {
		t.Fatal("expected a first attempt")
	}
	doSearch(t, u, "/api/search/chats?q=kopi", 200)
	if got := transcriptReads(a); got != reads {
		t.Errorf("a failed chat was retried by the next search: %d -> %d reads", reads, got)
	}
	// After the backoff it is tried again and, once Hermes answers, indexed.
	a.svc.now = func() time.Time { return time.Now().Add(indexRetryAfter + time.Minute) }
	a.hx.mu.Lock()
	a.hx.keys[u.profile] = u.key
	a.hx.mu.Unlock()
	r = doSearch(t, u, "/api/search/chats?q=kopi", 200)
	if !r.Index.Complete || len(r.Results) != 1 {
		t.Errorf("after the backoff: %+v", r)
	}
}
