package blog

import (
	"context"
	"encoding/json"
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
	fh.output = goodReply
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
	if r.Instructions != checkPrompt || !strings.HasPrefix(r.SessionKey, "chatui:blog:") || !strings.Contains(r.Input, "TITLE: A post") ||
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
	release := make(chan struct{})
	fh.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
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
