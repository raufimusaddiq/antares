package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type blogPost struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Slug          string `json:"slug"`
	LatestVersion int    `json:"latestVersion"`
}

// blog is the production check of the blog (specs/001-blog-cms-openui-publish): A's whole lifecycle through the real
// routes, with B and an anonymous reader probing what they must not see. The post it makes is deleted at the end, and on
// any failure on the way out (the fixture cleanup removes the accounts and so anything left).
func (s *scn) blog(ctx context.Context) (string, error) {
	a, b := s.cfg.A, s.cfg.B
	title := "Smoke post " + s.cfg.RunID
	slug := "smoke-" + s.cfg.RunID
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Written by the smoke test."}}}}}

	step := func(want int, as *Actor, method, path string, body any) (*Response, error) {
		r, err := s.c.Do(ctx, as.Session, method, path, nil, body)
		if err != nil {
			return nil, err
		}
		if r.Status != want {
			return r, fmt.Errorf("%s %s: wanted %d, got %s", method, path, want, r.Brief())
		}
		return r, nil
	}
	anon := func(want int, path string) (*Response, error) {
		r, err := s.c.Do(ctx, nil, "GET", path, nil, nil)
		if err != nil {
			return nil, err
		}
		if r.Status != want {
			return r, fmt.Errorf("anonymous GET %s: wanted %d, got %s", path, want, r.Brief())
		}
		return r, nil
	}

	r, err := step(200, a, "GET", "/api/blog/capabilities", nil)
	if err != nil {
		return "", err
	}
	var caps struct {
		Writing bool `json:"writing"`
	}
	if r.JSON(&caps) != nil || !caps.Writing {
		return "", fmt.Errorf("capabilities do not offer writing: %s", r.Brief())
	}
	r, err = step(200, a, "POST", "/api/blog/posts", map[string]any{})
	if err != nil {
		return "", err
	}
	var p blogPost
	if r.JSON(&p) != nil || p.ID == "" {
		return "", fmt.Errorf("create: unreadable answer")
	}
	path := "/api/blog/posts/" + p.ID
	defer func() { _, _ = s.c.Do(context.WithoutCancel(ctx), a.Session, "DELETE", path, nil, nil) }()

	if _, err := step(200, a, "PUT", path, map[string]any{"baseVersion": p.LatestVersion, "title": title, "doc": doc}); err != nil {
		return "", err
	}
	if _, err := step(409, a, "PUT", path, map[string]any{"baseVersion": p.LatestVersion, "title": "stale"}); err != nil {
		return "", fmt.Errorf("a save from a stale version must be refused: %w", err)
	}
	if _, err := step(200, a, "PATCH", path+"/settings", map[string]any{"slug": slug, "summary": "A post the smoke test made."}); err != nil {
		return "", err
	}
	// B is a stranger to A's draft: the same 404 as for a post that does not exist.
	for _, probe := range []struct{ method, path string }{{"GET", path}, {"DELETE", path}} {
		if _, err := step(404, b, probe.method, probe.path, nil); err != nil {
			return "", fmt.Errorf("another user: %w", err)
		}
	}
	if _, err := anon(404, "/api/public/blog/posts/"+slug); err != nil {
		return "", fmt.Errorf("a draft must not be public: %w", err)
	}
	if _, err := step(200, a, "POST", path+"/publish", map[string]string{"mode": "original"}); err != nil {
		return "", err
	}
	pub, err := anon(200, "/api/public/blog/posts/"+slug)
	if err != nil {
		return "", err
	}
	me, mr, merr := s.me(ctx, a)
	if merr != nil {
		return "", merr
	}
	if mr.Status != 200 || me.ID == "" {
		return "", fmt.Errorf("cannot read A's own account (%s), so the check that the public post carries no account id cannot be made", mr.Brief())
	}
	// By what the post says about its author, not by text: a reader's prose may contain "@" or the word "profile".
	var shape map[string]any
	if err := pub.JSON(&shape); err != nil {
		return "", fmt.Errorf("the public post is not JSON")
	}
	for _, key := range []string{"id", "postId", "authorId", "author_id", "userId", "email", "authorEmail", "profile", "tenant", "chatId", "runId"} {
		if _, there := shape[key]; there {
			return "", fmt.Errorf("the public post has a %q field", key)
		}
	}
	for _, leak := range []string{p.ID, me.ID} {
		if strings.Contains(string(pub.Body), leak) {
			return "", fmt.Errorf("the public post contains an internal id")
		}
	}
	if !strings.Contains(string(pub.Body), title) {
		return "", fmt.Errorf("the public post does not carry its title")
	}
	page, err := anon(200, "/b/"+slug)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(page.Body), "<title>"+title+"</title>") || strings.Contains(string(page.Body), `content="noindex"`) {
		return "", fmt.Errorf("the page /b/%s has no title of its own or is marked noindex", slug)
	}
	if list, err := anon(200, "/api/public/blog/posts"); err != nil || !strings.Contains(string(list.Body), slug) {
		return "", fmt.Errorf("the published post is not in the public list")
	}
	if _, err := step(200, a, "POST", path+"/unpublish", map[string]any{}); err != nil {
		return "", err
	}
	if _, err := anon(404, "/api/public/blog/posts/"+slug); err != nil {
		return "", fmt.Errorf("an unpublished post must not be public: %w", err)
	}
	if _, err := step(200, a, "DELETE", path, nil); err != nil {
		return "", err
	}
	if _, err := step(404, a, "GET", path, nil); err != nil {
		return "", err
	}
	return "draft private, 409 on a stale save, stranger got 404, published post readable signed out (no ids), page carries its own title, unpublished and deleted posts gone", nil
}

// blogCheck runs the AI check for real through the routes: a draft with claims worth checking, a run on the account's own
// assistant, and everything the author does with the result. It skips (never passes) where the deployment does not offer the
// check, because then nothing was exercised.
func (s *scn) blogCheck(ctx context.Context) (string, error) {
	a := s.cfg.A
	var caps struct {
		Writing, Check, WebSearch bool
	}
	r, err := s.c.Do(ctx, a.Session, "GET", "/api/blog/capabilities", nil, nil)
	if err != nil {
		return "", err
	}
	if r.Status != 200 || r.JSON(&caps) != nil {
		return "", fmt.Errorf("capabilities: %s", r.Brief())
	}
	if !caps.Check {
		return "", Skipped("this deployment does not offer the AI check (capabilities: check=false); use --skip-blog-check if that is intended")
	}

	chatsBefore, err := s.chatCount(ctx, a)
	if err != nil {
		return "", err
	}
	call := func(want int, method, path string, body any) (*Response, error) {
		r, err := s.c.Do(ctx, a.Session, method, path, nil, body)
		if err != nil {
			return nil, err
		}
		if r.Status != want {
			return r, fmt.Errorf("%s %s: wanted %d, got %s", method, path, want, r.Brief())
		}
		return r, nil
	}
	para := func(text string) any {
		return map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}
	}
	doc := map[string]any{"type": "doc", "content": []any{
		para("The Eiffel Tower in Paris was completed in 1889."),
		para("Jakarta is the capital of Indonesia and has roughly eleven million registered residents."),
	}}
	slug := "smoke-check-" + s.cfg.RunID
	r, err = call(200, "POST", "/api/blog/posts", map[string]any{})
	if err != nil {
		return "", err
	}
	var p blogPost
	if r.JSON(&p) != nil || p.ID == "" {
		return "", fmt.Errorf("create: unreadable answer")
	}
	path := "/api/blog/posts/" + p.ID
	defer func() { // a running check blocks a delete, so stop it first (best effort)
		_, _ = s.c.Do(context.WithoutCancel(ctx), a.Session, "POST", path+"/check/cancel", nil, map[string]any{})
		_, _ = s.c.Do(context.WithoutCancel(ctx), a.Session, "DELETE", path, nil, nil)
	}()
	if _, err := call(200, "PUT", path, map[string]any{"baseVersion": p.LatestVersion, "title": "Smoke check " + s.cfg.RunID, "doc": doc}); err != nil {
		return "", err
	}
	if _, err := call(200, "PATCH", path+"/settings", map[string]any{"slug": slug}); err != nil {
		return "", err
	}
	if _, err := call(202, "POST", path+"/check", map[string]any{}); err != nil {
		return "", err
	}

	type checkView struct {
		ID      string   `json:"id"`
		State   string   `json:"state"`
		Reason  string   `json:"reason"`
		Notices []string `json:"notices"`
		Notes   []struct {
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			AfterNode int    `json:"afterNode"`
		} `json:"notes"`
		Blocks []struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			AfterNode int    `json:"afterNode"`
			Markup    string `json:"markup"`
			Fallback  string `json:"fallback"`
			Decision  string `json:"decision"`
			NeedsAck  bool   `json:"needsAck"`
			Sources   []struct {
				URL      string `json:"url"`
				Verified bool   `json:"verified"`
			} `json:"sources"`
		} `json:"blocks"`
	}
	timeout := s.cfg.CheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var c checkView
	start := time.Now()
	last := "no answer yet"
	for {
		rr, err := s.c.Do(cctx, a.Session, "GET", path+"/check", nil, nil)
		switch {
		case err != nil:
			last = err.Error()
		case rr.Status != 200:
			last = "GET /check answered " + rr.Brief()
		case rr.JSON(&c) != nil:
			last = "GET /check answered something that is not a check"
		case c.State == "running":
			last = "still running"
		default:
			last = ""
		}
		if last == "" {
			break
		}
		if !sleep(cctx, 2*time.Second) {
			return "", fmt.Errorf("the check did not finish within %s; the last answer was: %s", timeout, last)
		}
	}
	took := time.Since(start).Round(time.Second)
	if c.State != "ready" {
		return "", fmt.Errorf("the check ended %s (reason %q) after %s: a failed check is a failure", c.State, c.Reason, took)
	}
	for _, n := range c.Notes {
		if n.AfterNode < 0 || n.AfterNode > 1 || strings.TrimSpace(n.Text) == "" {
			return "", fmt.Errorf("a note points at node %d or is empty", n.AfterNode)
		}
	}
	for i, b := range c.Blocks {
		if b.AfterNode < 0 || b.AfterNode > 1 || b.Markup == "" || b.Fallback == "" || b.Decision != "proposed" {
			return "", fmt.Errorf("block %d is malformed (anchor %d, decision %q)", i+1, b.AfterNode, b.Decision)
		}
		for _, src := range b.Sources {
			if src.Verified {
				return "", fmt.Errorf("block %d has a source marked verified: nothing can verify one yet", i+1)
			}
		}
		if len(b.Sources) > 0 && !b.NeedsAck {
			return "", fmt.Errorf("block %d cites sources that were not checked but needs no acknowledgement", i+1)
		}
	}

	// The author's choices: accept the first block (knowingly), publish with it, and a reader sees exactly that.
	accepted := 0
	if len(c.Blocks) > 0 {
		if _, err := call(200, "PUT", path+"/blocks/"+c.Blocks[0].ID, map[string]any{"decision": "accepted", "ack": c.Blocks[0].NeedsAck}); err != nil {
			return "", err
		}
		accepted = 1
		if c.Blocks[0].NeedsAck {
			if r, err := s.c.Do(ctx, a.Session, "PUT", path+"/blocks/"+c.Blocks[0].ID, nil, map[string]any{"decision": "accepted"}); err != nil || r.Status != 422 {
				return "", fmt.Errorf("accepting a block with unchecked sources without acknowledging must be refused (got %v %v)", err, r)
			}
		}
	}
	if _, err := call(200, "POST", path+"/publish", map[string]any{"mode": "enriched"}); err != nil {
		return "", err
	}
	pub, err := s.c.Do(ctx, nil, "GET", "/api/public/blog/posts/"+slug, nil, nil)
	if err != nil || pub.Status != 200 {
		return "", fmt.Errorf("the published post is not readable signed out: %v %v", err, pub)
	}
	var got struct {
		Blocks []struct {
			Markup string `json:"markup"`
		} `json:"blocks"`
	}
	if pub.JSON(&got) != nil || len(got.Blocks) != accepted {
		return "", fmt.Errorf("readers got %d blocks, the author accepted %d", len(got.Blocks), accepted)
	}
	// By identity, not by wording: the assistant's own notes and fallbacks are prose and may say "session" or "Hermes". What must
	// not reach a reader is an id of ours: the post's, the account's, and a run or session id (Hermes ids carry these prefixes).
	me, mr, merr := s.me(ctx, a)
	if merr != nil || mr.Status != 200 || me.ID == "" {
		return "", fmt.Errorf("cannot read A's own account, so the public post cannot be checked for an account id")
	}
	for _, id := range []string{p.ID, me.ID} {
		if strings.Contains(string(pub.Body), id) {
			return "", fmt.Errorf("the public post contains an internal id")
		}
	}
	var shape map[string]any
	if pub.JSON(&shape) != nil {
		return "", fmt.Errorf("the public post is not JSON")
	}
	for _, key := range []string{"id", "postId", "authorId", "userId", "email", "profile", "tenant", "chatId", "runId", "sessionId", "hermesRun", "hermesSession"} {
		if _, there := shape[key]; there {
			return "", fmt.Errorf("the public post has a %q field", key)
		}
	}
	if regexp.MustCompile(`\b(?:run|sess)_[A-Za-z0-9]{6,}`).Match(pub.Body) {
		return "", fmt.Errorf("the public post carries what looks like a run or session id")
	}
	// An edit after the check makes it out of date, and the published post keeps what was published.
	var cur blogPost
	if g, err := call(200, "GET", path, nil); err != nil || g.JSON(&cur) != nil {
		return "", fmt.Errorf("read post: %v", err)
	}
	if _, err := call(200, "PUT", path, map[string]any{"baseVersion": cur.LatestVersion, "title": "Smoke check " + s.cfg.RunID + " edited"}); err != nil {
		return "", err
	}
	var after checkView
	if g, err := call(200, "GET", path+"/check", nil); err != nil || g.JSON(&after) != nil || after.State != "out_of_date" {
		return "", fmt.Errorf("after an edit the check must be out of date (got %q, %v)", after.State, err)
	}
	if _, err := call(200, "POST", path+"/unpublish", map[string]any{}); err != nil {
		return "", err
	}
	// A check is no chat: the account's chat list is what it was.
	chatsAfter, err := s.chatCount(ctx, a)
	if err != nil {
		return "", err
	}
	if chatsAfter != chatsBefore {
		return "", fmt.Errorf("the account has %d chats after the check, %d before: a check must not create one", chatsAfter, chatsBefore)
	}
	return fmt.Sprintf("a real check ran in %s on the account's own assistant: %d notes, %d blocks (all sources unverified, acknowledgement required), accepted %d, published with exactly those, out of date after an edit, no chat created", took, len(c.Notes), len(c.Blocks), accepted), nil
}

func (s *scn) chatCount(ctx context.Context, a *Actor) (int, error) {
	r, err := s.c.Do(ctx, a.Session, "GET", "/api/chats", nil, nil)
	if err != nil {
		return 0, err
	}
	var v struct {
		Chats []json.RawMessage `json:"chats"`
	}
	if r.Status != 200 || r.JSON(&v) != nil {
		return 0, fmt.Errorf("list chats: %s", r.Brief())
	}
	return len(v.Chats), nil
}
