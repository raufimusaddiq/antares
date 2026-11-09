package blog

import (
	"context"
	"encoding/json"
	"github.com/raufimusaddiq/antares/internal/store"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

const goodReply = `{"version":1,
 "notes":[{"kind":"fact","text":"Check the 2019 figure.","afterNode":0}],
 "blocks":[
  {"kind":"table","afterNode":1,"markup":"root = Card([tbl])\ntbl = Table([Col(\"A\", [1, 2])])","fallback":"A: 1, 2",
   "sources":[{"url":"https://example.org/data","title":"The data","retrievedAt":"2026-11-08"}]},
  {"kind":"summary","afterNode":2,"markup":"root = Card([t])\nt = TextContent(\"In short.\")","fallback":"In short.","sources":[]},
  {"kind":"banner","afterNode":0,"markup":"x","fallback":"x","sources":[]}
 ]}`

// A draft with three top-level parts, ready to check.
func (p *person) draftToCheck(title string) Post {
	p.a.t.Helper()
	post := p.create()
	doc := map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "First part of the draft."}}},
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Second part, with a claim."}}},
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{map[string]any{"type": "text", "text": "A heading"}}},
	}}
	w := p.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": post.LatestVersion, "title": title, "doc": doc})
	if w.Code != 200 {
		p.a.t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	return into[Post](p.a.t, w)
}

func (p *person) startCheck(post Post) CheckView {
	p.a.t.Helper()
	w := p.do("POST", "/api/blog/posts/"+post.ID+"/check", nil)
	if w.Code != 202 {
		p.a.t.Fatalf("start check: %d %s", w.Code, w.Body)
	}
	return into[CheckView](p.a.t, w)
}

// waitCheck polls until the post's latest check is in one of the states.
func (p *person) waitCheck(postID string, want ...string) CheckView {
	p.a.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := p.do("GET", "/api/blog/posts/"+postID+"/check", nil)
		if w.Code == 200 {
			c := into[CheckView](p.a.t, w)
			for _, s := range want {
				if c.State == s {
					return c
				}
			}
			if time.Now().After(deadline) {
				p.a.t.Fatalf("check is %q, wanted %v", c.State, want)
			}
		} else if time.Now().After(deadline) {
			p.a.t.Fatalf("GET check: %d %s", w.Code, w.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *person) post(id string) Post {
	p.a.t.Helper()
	return into[Post](p.a.t, p.do("GET", "/api/blog/posts/"+id, nil))
}

func TestACheckRunsOnTheAuthorsOwnAssistantAndShowsWhatCameBack(t *testing.T) {
	fh := newFake()
	release := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		<-release // held, so the running state can be seen
		_ = emit("tool.started", map[string]any{"tool": "web_search", "preview": "a query", "tool_call_id": "c1"})
		_ = emit("tool.completed", map[string]any{"tool": "web_search", "preview": "", "error": false, "tool_call_id": "c1"})
		return emit("run.completed", map[string]any{"output": goodReply})
	}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("A post")

	started := u.startCheck(post)
	if started.State != "running" || len(started.Blocks) != 0 {
		t.Fatalf("started: %+v", started)
	}
	if got := u.post(post.ID); got.Status != "checking" || got.Check == nil || got.Check.State != "running" {
		t.Fatalf("while it runs the post says so: %+v", got)
	}
	close(release)
	c := u.waitCheck(post.ID, "ready")
	if len(c.Notes) != 1 || c.Notes[0].Text != "Check the 2019 figure." {
		t.Fatalf("notes: %+v", c.Notes)
	}
	if len(c.Blocks) != 2 || len(c.Notices) != 1 || !strings.HasPrefix(c.Notices[0], "Block 3 was removed") {
		t.Fatalf("blocks %d notices %v (the bad third block is dropped with a notice)", len(c.Blocks), c.Notices)
	}
	data := c.Blocks[0]
	if !data.NeedsAck || data.Decision != "proposed" || len(data.Sources) != 1 || data.Sources[0].Verified || data.AfterNode != 1 {
		t.Fatalf("a data block must be unverified and need acknowledgement: %+v", data)
	}
	if c.Blocks[1].NeedsAck {
		t.Fatalf("words with no sources need no acknowledgement: %+v", c.Blocks[1])
	}
	if got := u.post(post.ID); got.Status != "ready" || got.Check.State != "ready" {
		t.Fatalf("after: %+v", got)
	}

	// What was sent, and as whom: one session and one run of this author's own, the versioned instruction, the numbered draft.
	sessions, runs, bindings, _ := fh.snapshot()
	if sessions != 1 || len(runs) != 1 {
		t.Fatalf("sessions %d runs %d", sessions, len(runs))
	}
	r := runs[0]
	if r.Instructions != checkPrompt || r.SessionKey != "chatui:user:"+u.id || !strings.Contains(r.Input, "TITLE: A post") ||
		!strings.Contains(r.Input, "[0] First part") || !strings.Contains(r.Input, "[2] ## A heading") {
		t.Fatalf("run request: %+v", r)
	}
	for _, b := range bindings {
		if b.Profile != u.profile {
			t.Fatalf("a call was made as %q, not as the author's %q", b.Profile, u.profile)
		}
	}
	// It is no chat: nothing in the chat tables, nothing in the chats' run table.
	for _, table := range []string{"chats", "runs", "messages"} {
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s has %d rows (%v)", table, n, err)
		}
	}
}

// Every way a check can fail is shown as that failure; the post goes back as it was; nothing can be published from it.
func TestEveryFailureIsShownAsItselfAndNothingIsPublished(t *testing.T) {
	prompt := func(ctx context.Context, emit func(string, map[string]any) error) error {
		return emit("approval.request", map[string]any{"request_id": "r1", "command": "rm -rf"})
	}
	for name, tc := range map[string]struct {
		setup  func(f *fakeHermes)
		state  string
		reason string
	}{
		"run failed": {func(f *fakeHermes) {
			f.script = func(c context.Context, e func(string, map[string]any) error) error {
				return e("run.failed", map[string]any{})
			}
		}, "failed", "error"},
		"timed out": {func(f *fakeHermes) {
			f.script = func(c context.Context, e func(string, map[string]any) error) error {
				return &hermes.Error{Code: hermes.CodeUpstreamTimeout}
			}
		}, "timed_out", ""},
		"connection lost": {func(f *fakeHermes) {
			f.script = func(c context.Context, e func(string, map[string]any) error) error { return errBoom }
			f.getRunErr = errBoom
		}, "failed", "disconnected"},
		"unusable reply": {func(f *fakeHermes) { f.output = "Looks good to me!" }, "failed", "unusable"},
		"empty reply":    {func(f *fakeHermes) { f.output = "" }, "failed", "unusable"},
		"a question":     {func(f *fakeHermes) { f.script = prompt }, "failed", "error"},
		"cannot start":   {func(f *fakeHermes) { f.createRunErr = errBoom }, "failed", "error"},
		"no session":     {func(f *fakeHermes) { f.createSessionErr = errBoom }, "failed", "error"},
		"reported cancelled": {func(f *fakeHermes) {
			f.script = func(c context.Context, e func(string, map[string]any) error) error {
				return e("run.cancelled", map[string]any{})
			}
		}, "cancelled", ""},
	} {
		fh := newFake()
		tc.setup(fh)
		a := newAppWithHermes(t, fh)
		u := a.active("alice")
		post := u.draftToCheck("Fails: " + name)
		u.startCheck(post)
		c := u.waitCheck(post.ID, tc.state)
		if c.State != tc.state || c.Reason != tc.reason || len(c.Blocks) != 0 {
			t.Errorf("%s: got %s/%q with %d blocks, want %s/%q", name, c.State, c.Reason, len(c.Blocks), tc.state, tc.reason)
		}
		if got := u.post(post.ID); got.Status != "draft" {
			t.Errorf("%s: the post is %q, it must go back to draft", name, got.Status)
		}
		u.do("PATCH", "/api/blog/posts/"+post.ID+"/settings", map[string]any{"slug": "fails-post"})
		if w := u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "enriched"}); w.Code != 409 {
			t.Errorf("%s: publishing enriched after a failed check: %d", name, w.Code)
		}
		if w := a.anon("GET", "/api/public/blog/posts/fails-post"); w.Code != 404 {
			t.Errorf("%s: something was published: %d", name, w.Code)
		}
	}
}

func TestCancellingStopsTheRunAndALateAnswerChangesNothing(t *testing.T) {
	fh := newFake()
	release, live := make(chan struct{}), make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		close(live)
		select {
		case <-release:
			return emit("run.completed", map[string]any{"output": goodReply})
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Cancel me")
	u.startCheck(post)
	<-live // the run exists upstream: cancelling now has something to stop
	w := u.do("POST", "/api/blog/posts/"+post.ID+"/check/cancel", nil)
	if w.Code != 200 || into[CheckView](t, w).State != "cancelled" {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	close(release)
	time.Sleep(80 * time.Millisecond)
	if c := u.waitCheck(post.ID, "cancelled"); len(c.Blocks) != 0 {
		t.Fatalf("a late answer was recorded: %+v", c)
	}
	if got := u.post(post.ID); got.Status != "draft" {
		t.Fatalf("status %q", got.Status)
	}
	_, _, _, stopped := fh.snapshot()
	if len(stopped) != 1 {
		t.Fatalf("the upstream run was not stopped: %v", stopped)
	}
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/check/cancel", nil); w.Code != 404 {
		t.Fatalf("cancelling with nothing running: %d", w.Code)
	}
}

func TestEditingTheDraftMakesTheCheckOutOfDate(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Edit me")
	u.startCheck(post)
	c := u.waitCheck(post.ID, "ready")
	cur := u.post(post.ID)
	w := u.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "Edited"})
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	got := u.post(post.ID)
	if got.Check == nil || got.Check.State != "out_of_date" || got.Status != "draft" {
		t.Fatalf("after an edit: status %q, check %+v", got.Status, got.Check)
	}
	if w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+c.Blocks[0].ID, map[string]any{"decision": "rejected"}); w.Code != 409 {
		t.Fatalf("deciding on a block of an out-of-date check: %d", w.Code)
	}
	u.do("PATCH", "/api/blog/posts/"+post.ID+"/settings", map[string]any{"slug": "edit-me-post"})
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "enriched"}); w.Code != 409 {
		t.Fatalf("publishing enriched from an out-of-date check: %d", w.Code)
	}
	// The original can still be published, and a new check can be started.
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "original"}); w.Code != 200 {
		t.Fatalf("publish original: %d %s", w.Code, w.Body)
	}
}

func TestEditingWhileItRunsDiscardsTheAnswer(t *testing.T) {
	fh := newFake()
	release := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		<-release
		return emit("run.completed", map[string]any{"output": goodReply})
	}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Race")
	u.startCheck(post)
	cur := u.post(post.ID)
	u.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "Changed under it"})
	close(release)
	time.Sleep(80 * time.Millisecond)
	c := u.waitCheck(post.ID, "out_of_date")
	if len(c.Blocks) != 0 {
		t.Fatalf("an answer about an older draft was kept: %+v", c)
	}
}

func TestAnotherUsersCheckIsA404AndEachUsesTheirOwnAssistant(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	alice, bob := a.active("alice"), a.active("bob")
	post := alice.draftToCheck("Alice's")
	alice.startCheck(post)
	c := alice.waitCheck(post.ID, "ready")
	missing := "00000000000000000000000000000000"
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/blog/posts/%s/check", nil}, {"GET", "/api/blog/posts/%s/check", nil}, {"POST", "/api/blog/posts/%s/check/cancel", nil},
		{"PUT", "/api/blog/posts/%s/blocks/" + c.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": true}},
		{"POST", "/api/blog/posts/%s/publish", map[string]any{"mode": "enriched"}},
	} {
		foreign := bob.do(r.method, strings.Replace(r.path, "%s", post.ID, 1), r.body)
		absent := bob.do(r.method, strings.Replace(r.path, "%s", missing, 1), r.body)
		if foreign.Code != 404 || absent.Code != 404 {
			t.Errorf("%s %s: foreign %d, missing %d", r.method, r.path, foreign.Code, absent.Code)
		}
	}
	// A block id of A's under B's own post is no block of B's.
	mine := bob.draftToCheck("Bob's")
	if w := bob.do("PUT", "/api/blog/posts/"+mine.ID+"/blocks/"+c.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": true}); w.Code != 404 {
		t.Errorf("A's block under B's post: %d", w.Code)
	}
	bob.startCheck(mine)
	bob.waitCheck(mine.ID, "ready")
	_, _, bindings, _ := fh.snapshot()
	seen := map[string]bool{}
	for _, b := range bindings {
		seen[b.Profile] = true
	}
	if len(seen) != 2 || !seen[alice.profile] || !seen[bob.profile] {
		t.Fatalf("profiles used: %v", seen)
	}
	if got := alice.post(post.ID); got.Check.State != "ready" || len(got.Check.Blocks) != 2 {
		t.Fatalf("B's actions changed A's check: %+v", got.Check)
	}
}

func TestBlockDecisionsAndPublishingTheAcceptedOnes(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Enriched")
	u.do("PATCH", "/api/blog/posts/"+post.ID+"/settings", map[string]any{"slug": "enriched-post"})
	u.startCheck(post)
	c := u.waitCheck(post.ID, "ready")
	dataBlock, wordsBlock := c.Blocks[0], c.Blocks[1]

	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "enriched"}); w.Code != 200 {
		t.Fatalf("publishing with every block undecided publishes none of them: %d %s", w.Code, w.Body)
	}
	if w := a.anon("GET", "/api/public/blog/posts/enriched-post"); !strings.Contains(w.Body.String(), `"blocks":[]`) {
		t.Fatalf("an undecided block was published: %s", w.Body)
	}
	// Accepting a block whose sources were not checked takes an explicit acknowledgement.
	w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+dataBlock.ID, map[string]any{"decision": "accepted"})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "ACK_REQUIRED") {
		t.Fatalf("accepting unverified sources without acknowledging: %d %s", w.Code, w.Body)
	}
	if w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+dataBlock.ID, map[string]any{"decision": "accepted", "ack": true}); w.Code != 200 || into[BlockView](t, w).Decision != "accepted" {
		t.Fatalf("accept with ack: %d %s", w.Code, w.Body)
	}
	if w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+wordsBlock.ID, map[string]any{"decision": "rejected"}); w.Code != 200 {
		t.Fatalf("reject: %d", w.Code)
	}
	if w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+dataBlock.ID, map[string]any{"decision": "maybe"}); w.Code != 422 && w.Code != 400 {
		t.Fatalf("a decision that is neither: %d", w.Code)
	}
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "enriched"}); w.Code != 200 {
		t.Fatalf("publish enriched: %d %s", w.Code, w.Body)
	}
	pub := a.anon("GET", "/api/public/blog/posts/enriched-post")
	var got PublicPost
	_ = json.Unmarshal(pub.Body.Bytes(), &got)
	if len(got.Blocks) != 1 || got.Blocks[0].Anchor != 1 || len(got.Blocks[0].Sources) != 1 || got.Blocks[0].Fallback != "A: 1, 2" {
		t.Fatalf("public blocks: %s", pub.Body)
	}
	// Readers get the sources as cited (and honestly not marked verified), and nothing of the run or the account.
	if !strings.Contains(pub.Body.String(), `"verified":false`) {
		t.Errorf("a source must never read as verified: %s", pub.Body)
	}
	for _, leak := range []string{u.id, post.ID, "hermes", "session", "run"} {
		if strings.Contains(strings.ToLower(pub.Body.String()), leak) {
			t.Errorf("the public post contains %q: %s", leak, pub.Body)
		}
	}
	if l := into[[]Summary](t, u.do("GET", "/api/blog/posts", nil)); len(l) != 1 || !l[0].Interactive {
		t.Fatalf("the list should mark the post interactive: %+v", l)
	}
	// Publishing the original afterwards takes the blocks off.
	u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "original"})
	if w := a.anon("GET", "/api/public/blog/posts/enriched-post"); !strings.Contains(w.Body.String(), `"blocks":[]`) {
		t.Fatalf("blocks survived publishing the original: %s", w.Body)
	}
}

func TestOneCheckAtATimeAndAPerAuthorLimit(t *testing.T) {
	fh := newFake()
	hold := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return ctx.Err()
	}
	a := newAppWithHermes(t, fh)
	t.Cleanup(func() { close(hold) })
	u := a.active("alice")
	p1, p2, p3 := u.draftToCheck("One"), u.draftToCheck("Two"), u.draftToCheck("Three")
	u.startCheck(p1)
	if w := u.do("POST", "/api/blog/posts/"+p1.ID+"/check", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "CHECK_RUNNING") {
		t.Fatalf("a second check of the same post: %d %s", w.Code, w.Body)
	}
	u.startCheck(p2)
	if w := u.do("POST", "/api/blog/posts/"+p3.ID+"/check", nil); w.Code != 422 || !strings.Contains(w.Body.String(), "LIMIT_REACHED") {
		t.Fatalf("a third check at once: %d %s", w.Code, w.Body)
	}
}

func TestACheckNeedsSomethingToCheckAndTheAssistant(t *testing.T) {
	fh := newFake()
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	empty := u.create()
	if w := u.do("POST", "/api/blog/posts/"+empty.ID+"/check", nil); w.Code != 422 {
		t.Fatalf("checking an empty draft: %d", w.Code)
	}
	// With no assistant configured nothing is sent, and the answer says so.
	off := newApp(t)
	o := off.active("alice")
	post := o.draftToCheck("No Hermes")
	if w := o.do("POST", "/api/blog/posts/"+post.ID+"/check", nil); w.Code != 503 || !strings.Contains(w.Body.String(), "CAPABILITY_UNAVAILABLE") {
		t.Fatalf("with no assistant configured: %d %s", w.Code, w.Body)
	}
	if s, r, _, _ := fh.snapshot(); s != 0 || len(r) != 0 {
		t.Fatal("something was sent to Hermes")
	}
}

func TestCapabilitiesAreOnlyWhatTheAssistantConfirms(t *testing.T) {
	get := func(a *app, u *person) map[string]bool {
		a.svc.capMu.Lock()
		a.svc.capCache = nil // each step asks the assistant afresh
		a.svc.capMu.Unlock()
		return into[map[string]bool](t, u.do("GET", "/api/blog/capabilities", nil))
	}
	fh := newFake()
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	if c := get(a, u); !c["writing"] || !c["check"] || !c["webSearch"] {
		t.Fatalf("with web search confirmed: %v", c)
	}
	fh.toolsets = []hermes.Toolset{{Name: "web", Status: hermes.StatusUnsupported}, {Name: "file", Status: hermes.StatusAvailable}}
	if c := get(a, u); c["check"] || c["webSearch"] || !c["writing"] {
		t.Fatalf("web search not available: %v", c)
	}
	fh.toolsets, fh.discoverErr = nil, errBoom
	if c := get(a, u); c["check"] || c["webSearch"] {
		t.Fatalf("a failed probe must not read as available: %v", c)
	}
	off := newApp(t)
	if c := get(off, off.active("bob")); c["check"] || c["webSearch"] || !c["writing"] {
		t.Fatalf("no Hermes configured: %v", c)
	}
}

func TestACheckRunningAtRestartIsNotFinishedAndTheDraftIsFreed(t *testing.T) {
	fh := newFake()
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Interrupted")
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	if _, err := a.db.Exec(`INSERT INTO post_checks (id, post_id, author_id, version, state, prev_status, started_at) VALUES ('c1', ?, ?, ?, 'running', 'draft', ?)`, post.ID, u.id, post.LatestVersion, now); err != nil {
		t.Fatal(err)
	}
	a.db.Exec(`UPDATE posts SET status = 'checking' WHERE id = ?`, post.ID)
	if _, err := New(Options{DB: a.db}); err != nil { // a new process over the same database
		t.Fatal(err)
	}
	got := u.post(post.ID)
	if got.Status != "draft" || got.Check == nil || got.Check.State != "failed" || got.Check.Reason != "disconnected" {
		t.Fatalf("after a restart: status %q check %+v", got.Status, got.Check)
	}
}

func TestDeletingAPostRemovesItsChecksAndBlocks(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Delete me")
	u.do("PATCH", "/api/blog/posts/"+post.ID+"/settings", map[string]any{"slug": "delete-me-post"})
	u.startCheck(post)
	c := u.waitCheck(post.ID, "ready")
	u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+c.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": true})
	u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "enriched"})
	if w := u.do("DELETE", "/api/blog/posts/"+post.ID, nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	for _, table := range []string{"post_checks", "post_blocks", "block_sources", "published_blocks", "published_block_sources", "post_versions"} {
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s still has %d rows (%v)", table, n, err)
		}
	}
}

func TestASourceCanNeverBeStoredAsVerified(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.draftToCheck("Guard")
	now := "2026-11-09T00:00:00.000000000Z"
	a.db.Exec(`INSERT INTO post_checks (id, post_id, author_id, version, state, prev_status, started_at) VALUES ('c1', ?, ?, 1, 'ready', 'draft', ?)`, p.ID, u.id, now)
	a.db.Exec(`INSERT INTO post_blocks (id, check_id, post_id, author_id, position, anchor, kind, markup, fallback, needs_ack) VALUES ('b1','c1',?,?,0,0,'table','m','f',1)`, p.ID, u.id)
	if _, err := a.db.Exec(`INSERT INTO block_sources (block_id, position, url, title, retrieved_at, verified) VALUES ('b1', 0, 'https://example.org/', 't', '2026-11-08', 1)`); err == nil {
		t.Fatal("the database accepted a verified source: nothing can verify one yet (research R4)")
	}
}

func TestCapabilitiesAreNotAskedOfHermesOnEveryPageLoad(t *testing.T) {
	fh := newFake()
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	for i := 0; i < 5; i++ {
		u.do("GET", "/api/blog/capabilities", nil)
	}
	fh.mu.Lock()
	n := fh.discovers
	fh.mu.Unlock()
	if n != 1 {
		t.Fatalf("Hermes was asked %d times for 5 page loads", n)
	}
}

// A graceful stop is not the author cancelling: the row stays running, and the next start says what happened to it.
func TestShuttingDownLeavesARunningCheckToBeRecoveredNotCancelled(t *testing.T) {
	fh := newFake()
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Restart me")
	u.startCheck(post)
	a.svc.Close()
	var state string
	if err := a.db.QueryRow(`SELECT state FROM post_checks WHERE post_id = ?`, post.ID).Scan(&state); err != nil || state != "running" {
		t.Fatalf("after a shutdown the check is %q (%v), it must still be running so a restart can recover it", state, err)
	}
	if _, err := New(Options{DB: a.db}); err != nil {
		t.Fatal(err)
	}
	got := u.post(post.ID)
	if got.Check == nil || got.Check.State != "failed" || got.Check.Reason != "disconnected" || got.Status != "draft" {
		t.Fatalf("after the restart: %q %+v", got.Status, got.Check)
	}
}

func TestOnlyTheLatestChecksBlocksCanBeDecidedOn(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Twice")
	u.startCheck(post)
	first := u.waitCheck(post.ID, "ready")
	u.startCheck(post) // a second check of the same version
	second := u.waitCheck(post.ID, "ready")
	if first.ID == second.ID {
		t.Fatal("expected a second check")
	}
	w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+first.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": true})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "CHECK_SUPERSEDED") {
		t.Fatalf("deciding on the older check's block: %d %s", w.Code, w.Body)
	}
	var decision string
	a.db.QueryRow(`SELECT decision FROM post_blocks WHERE id = ?`, first.Blocks[0].ID).Scan(&decision)
	if decision != "proposed" {
		t.Fatalf("the refused decision was stored anyway: %q", decision)
	}
	if w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+second.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": true}); w.Code != 200 {
		t.Fatalf("deciding on the latest check's block: %d", w.Code)
	}
}

func TestEditingStopsTheWorkerOfTheCheckItMadeStale(t *testing.T) {
	fh := newFake()
	started := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Stale")
	u.startCheck(post)
	<-started
	cur := u.post(post.ID)
	u.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "Edited"})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, _, _, stopped := fh.snapshot(); len(stopped) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the upstream run of the stale check was not stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A newer check that did not finish must not leave the author deciding on one check while another is published.
func TestAFailedNewerCheckLeavesNothingToPublishEnriched(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Two checks")
	u.do("PATCH", "/api/blog/posts/"+post.ID+"/settings", map[string]any{"slug": "two-checks-post"})
	u.startCheck(post)
	first := u.waitCheck(post.ID, "ready")
	u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+first.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": true})
	fh.mu.Lock()
	fh.output = "not an envelope"
	fh.mu.Unlock()
	u.startCheck(post)
	if c := u.waitCheck(post.ID, "failed"); c.Reason != "unusable" {
		t.Fatalf("%+v", c)
	}
	// What the author sees is the failed check, and the older one is not published behind it.
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "enriched"}); w.Code != 409 || !strings.Contains(w.Body.String(), "CHECK_REQUIRED") {
		t.Fatalf("publishing enriched while the newest check failed: %d %s", w.Code, w.Body)
	}
	if w := u.do("PUT", "/api/blog/posts/"+post.ID+"/blocks/"+first.Blocks[0].ID, map[string]any{"decision": "rejected"}); w.Code != 409 || strings.Contains(w.Body.String(), "OUT_OF_DATE") {
		t.Fatalf("the refusal must not claim the draft changed: %d %s", w.Code, w.Body)
	}
}

func TestTheCapForRunningChecksHoldsAcrossPosts(t *testing.T) {
	fh := newFake()
	hold := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return ctx.Err()
	}
	a := newAppWithHermes(t, fh)
	t.Cleanup(func() { close(hold) })
	u := a.active("alice")
	var posts []Post
	for i := 0; i < 5; i++ {
		posts = append(posts, u.draftToCheck("P"+string(rune('a'+i))))
	}
	codes := make(chan int, len(posts))
	for _, p := range posts {
		go func(id string) { codes <- u.do("POST", "/api/blog/posts/"+id+"/check", nil).Code }(p.ID)
	}
	ok := 0
	for range posts {
		if <-codes == 202 {
			ok++
		}
	}
	if ok != maxChecksPerUser {
		t.Fatalf("%d checks started at once, the limit is %d", ok, maxChecksPerUser)
	}
}

func TestACheckIsNotStartedWhenTheAssistantCannotBeConfirmed(t *testing.T) {
	fh := newFake()
	fh.toolsets = []hermes.Toolset{{Name: "file", Status: hermes.StatusAvailable}}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("No web")
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/check", nil); w.Code != 503 {
		t.Fatalf("with no web search the capability says unavailable, so must the start: %d %s", w.Code, w.Body)
	}
	if _, runs, _, _ := fh.snapshot(); len(runs) != 0 {
		t.Fatal("a run was sent")
	}
}

// A check is a run that spends: the same hard limit refuses it, what it spent is counted with the author's other spending, and
// the single-use Hermes session is deleted whatever the outcome.
func TestACheckIsSubjectToTheSpendingLimitAndIsCountedInIt(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	fh.usage = map[string]any{"input_tokens": 1200, "output_tokens": 300, "total_tokens": 1500}
	spend := &spendFake{}
	a := newAppWith(t, fh, spend)
	u := a.active("alice")
	post := u.draftToCheck("Spend")

	spend.reached = true
	w := u.do("POST", "/api/blog/posts/"+post.ID+"/check", nil)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "BUDGET_REACHED") {
		t.Fatalf("over the daily hard limit: %d %s", w.Code, w.Body)
	}
	if _, runs, _, _ := fh.snapshot(); len(runs) != 0 {
		t.Fatal("a run was sent past the limit")
	}

	spend.reached = false
	u.startCheck(post)
	u.waitCheck(post.ID, "ready")
	deadline := time.Now().Add(3 * time.Second)
	var in, out int64
	for {
		a.db.QueryRow(`SELECT COALESCE(input_tokens,0), COALESCE(output_tokens,0) FROM post_checks WHERE post_id = ?`, post.ID).Scan(&in, &out)
		if in != 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if in != 1200 || out != 300 {
		t.Fatalf("recorded tokens %d/%d", in, out)
	}
	usage, err := store.UserModelUsage(context.Background(), a.db, u.id, time.Now().Add(-time.Hour))
	if err != nil || len(usage) != 1 || usage[0].Model != "gpt-test" || usage[0].Runs != 1 || usage[0].Input != 1200 {
		t.Fatalf("the author's usage does not include the check: %+v %v", usage, err)
	}
}

func TestTheChecksHermesSessionIsDeletedWhateverHappens(t *testing.T) {
	for name, setup := range map[string]func(f *fakeHermes){
		"ready":  func(f *fakeHermes) { f.output = goodReply },
		"failed": func(f *fakeHermes) { f.output = "not an envelope" },
		"error": func(f *fakeHermes) {
			f.script = func(c context.Context, e func(string, map[string]any) error) error {
				return e("run.failed", map[string]any{})
			}
		},
	} {
		fh := newFake()
		setup(fh)
		a := newAppWithHermes(t, fh)
		u := a.active("alice")
		post := u.draftToCheck(name)
		u.startCheck(post)
		u.waitCheck(post.ID, "ready", "failed")
		deadline := time.Now().Add(3 * time.Second)
		for {
			fh.mu.Lock()
			n, d := fh.sessions, len(fh.deleted)
			fh.mu.Unlock()
			if n == 1 && d == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d sessions made, %d deleted", name, n, d)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestThereIsACapOnChecksPer24Hours(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Daily")
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	for i := 0; i < maxChecksPer24h; i++ {
		a.db.Exec(`INSERT INTO post_checks (id, post_id, author_id, version, state, prev_status, started_at) VALUES (?, ?, ?, 1, 'failed', 'draft', ?)`, "old"+string(rune('a'+i%26))+string(rune('a'+i/26)), post.ID, u.id, now)
	}
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/check", nil); w.Code != 422 || !strings.Contains(w.Body.String(), "LIMIT_REACHED") {
		t.Fatalf("past the daily cap: %d %s", w.Code, w.Body)
	}
}

func TestACheckRecordsWhichInstructionProducedIt(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Version")
	u.startCheck(post)
	u.waitCheck(post.ID, "ready")
	var v string
	a.db.QueryRow(`SELECT prompt_version FROM post_checks WHERE post_id = ?`, post.ID).Scan(&v)
	if v != "blog-check v1" {
		t.Fatalf("prompt version %q", v)
	}
}

// A deleted post must not reset the author's spending window.
func TestDeletingAPostKeepsWhatItsChecksCost(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	fh.usage = map[string]any{"input_tokens": 700, "output_tokens": 50, "total_tokens": 750}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Cost")
	u.startCheck(post)
	u.waitCheck(post.ID, "ready")
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM post_checks WHERE input_tokens IS NOT NULL`).Scan(&n)
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before, _ := store.UserModelUsage(context.Background(), a.db, u.id, time.Now().Add(-time.Hour))
	if w := u.do("DELETE", "/api/blog/posts/"+post.ID, nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	after, err := store.UserModelUsage(context.Background(), a.db, u.id, time.Now().Add(-time.Hour))
	if err != nil || len(before) != 1 || len(after) != 1 || after[0].Input != before[0].Input || after[0].Runs != 1 {
		t.Fatalf("spending before %+v after deleting the post %+v (%v)", before, after, err)
	}
}

func TestAPostWithARunningCheckCannotBeDeleted(t *testing.T) {
	fh := newFake()
	hold := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return ctx.Err()
	}
	a := newAppWithHermes(t, fh)
	t.Cleanup(func() { close(hold) })
	u := a.active("alice")
	post := u.draftToCheck("Busy")
	u.startCheck(post)
	if w := u.do("DELETE", "/api/blog/posts/"+post.ID, nil); w.Code != 409 || !strings.Contains(w.Body.String(), "CHECK_RUNNING") {
		t.Fatalf("deleting under a running check: %d %s", w.Code, w.Body)
	}
}

// Checks must not leave a frozen version row behind each: a check that ends without a result unfreezes the draft, and an edit
// after a finished check drops the old version.
func TestChecksDoNotAccumulateVersionRows(t *testing.T) {
	versions := func(a *app, id string) int {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM post_versions WHERE post_id = ?`, id).Scan(&n)
		return n
	}
	fh := newFake()
	fh.output = "not an envelope"
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Versions")
	base := versions(a, post.ID)
	for i := 0; i < 3; i++ {
		u.startCheck(post)
		u.waitCheck(post.ID, "failed")
		cur := u.post(post.ID)
		w := u.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "Versions " + string(rune('a'+i))})
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
		post = into[Post](t, w)
	}
	if n := versions(a, post.ID); n != base {
		t.Fatalf("%d version rows after three failed checks and three edits, started with %d", n, base)
	}
	fh.mu.Lock()
	fh.output = goodReply
	fh.mu.Unlock()
	for i := 0; i < 3; i++ {
		u.startCheck(post)
		u.waitCheck(post.ID, "ready")
		cur := u.post(post.ID)
		w := u.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "Ready " + string(rune('a'+i))})
		post = into[Post](t, w)
	}
	if n := versions(a, post.ID); n > base+1 {
		t.Fatalf("%d version rows after three finished checks and three edits", n)
	}
}

// The admin's blocked-model list applies to a check as to a chat: the model that would run is named, and a blocked one never runs.
func TestACheckNeverRunsOnABlockedModel(t *testing.T) {
	fh := newFake()
	fh.output = goodReply
	fh.defaultModel = hermes.ModelCatalog{Provider: "p", Model: "blocked-model"}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Policy")

	// No block list: nothing is pinned, the profile's own default runs.
	u.startCheck(post)
	u.waitCheck(post.ID, "ready")
	if _, runs, _, _ := fh.snapshot(); len(runs) != 1 || runs[0].Model != "" {
		t.Fatalf("with no block list the run is not pinned: %+v", runs)
	}

	if _, err := a.db.Exec(`INSERT INTO model_blocked (model_id, blocked_by, created_at) VALUES ('blocked-model', ?, 'x')`, u.id); err != nil {
		t.Fatal(err)
	}
	u.startCheck(post)
	if c := u.waitCheck(post.ID, "failed"); c.Reason != "error" {
		t.Fatalf("a check on a blocked default model: %+v", c)
	}
	if _, runs, _, _ := fh.snapshot(); len(runs) != 1 {
		t.Fatalf("a run was sent on a blocked model: %d runs", len(runs))
	}

	// A block list exists and the default is another model: it is named explicitly on the run.
	a.db.Exec(`DELETE FROM model_blocked`)
	if _, err := a.db.Exec(`INSERT INTO model_blocked (model_id, blocked_by, created_at) VALUES ('some-other', ?, 'x')`, u.id); err != nil {
		t.Fatal(err)
	}
	u.startCheck(post)
	u.waitCheck(post.ID, "ready")
	if _, runs, _, _ := fh.snapshot(); len(runs) != 2 || runs[1].Model != "blocked-model" || runs[1].Provider != "p" {
		t.Fatalf("the allowed default must be named on the run: %+v", runs)
	}

	// A block list and a default that cannot be determined: refused, not assumed allowed.
	fh.mu.Lock()
	fh.defaultModel = hermes.ModelCatalog{}
	fh.mu.Unlock()
	u.startCheck(post)
	u.waitCheck(post.ID, "failed")
	if _, runs, _, _ := fh.snapshot(); len(runs) != 2 {
		t.Fatalf("a run was sent with an unknown default: %d runs", len(runs))
	}
}

func TestAnAccountSuspendedDuringACheckGetsNoResult(t *testing.T) {
	fh := newFake()
	release := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
		<-release
		return emit("run.completed", map[string]any{"output": goodReply})
	}
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Suspended")
	u.startCheck(post)
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var state string
		var blocks int
		a.db.QueryRow(`SELECT state FROM post_checks WHERE post_id = ?`, post.ID).Scan(&state)
		a.db.QueryRow(`SELECT COUNT(*) FROM post_blocks WHERE post_id = ?`, post.ID).Scan(&blocks)
		if state == "cancelled" && blocks == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %q with %d blocks: a suspended account must get no result", state, blocks)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
