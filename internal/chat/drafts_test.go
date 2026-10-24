package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

// UXP-007/UXP-006, UXP-ACC-005/008/009 (issue #107): server drafts are owner-scoped and
// revision-safe; templates are owner-scoped literal text.

type draftResp struct {
	Body     string
	Revision int64
	Current  *struct {
		Body     string
		Revision int64
	}
	Error struct{ Code string }
}

func (p *person) putDraft(path, ifMatch string, body any) (*draftResp, int, string) {
	hdr := map[string]string{}
	if ifMatch != "" {
		hdr["If-Match"] = ifMatch
	}
	w := p.do("PUT", path, hdr, body)
	var d draftResp
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	return &d, w.Code, w.Header().Get("ETag")
}

func (p *person) readDraft(path string) draftResp {
	p.a.t.Helper()
	w := p.get(path)
	if w.Code != 200 {
		p.a.t.Fatalf("GET %s = %d %s", path, w.Code, w.Body.String())
	}
	return decodeBody[draftResp](p.a.t, w)
}

func TestDraftRestoresOnAnotherDeviceAndRevisionsAdvance(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	chat := u.newChat()
	path := "/api/chats/" + chat + "/draft"

	if d := u.readDraft(path); d.Revision != 0 || d.Body != "" {
		t.Fatalf("a chat with no draft = %+v", d)
	}
	d, code, etag := u.putDraft(path, `"0"`, map[string]string{"body": "half a thought"})
	if code != 200 || d.Revision != 1 || etag != `"1"` {
		t.Fatalf("first write: %d %+v etag %s", code, d, etag)
	}
	// "Another device": a fresh read with nothing but the session cookie.
	if got := u.readDraft(path); got.Body != "half a thought" || got.Revision != 1 {
		t.Fatalf("restore = %+v", got)
	}
	if d, code, _ = u.putDraft(path, `"1"`, map[string]string{"body": "a whole thought"}); code != 200 || d.Revision != 2 {
		t.Fatalf("second write: %d %+v", code, d)
	}
	// A different chat and the new-chat draft are separate drafts.
	if got := u.readDraft("/api/chats/" + u.newChat() + "/draft"); got.Body != "" {
		t.Fatalf("draft bled into another chat: %+v", got)
	}
	if got := u.readDraft("/api/me/new-chat-draft"); got.Body != "" || got.Revision != 0 {
		t.Fatalf("draft bled into the new-chat draft: %+v", got)
	}
}

func TestStaleDraftWriteNeverOverwritesNewerText(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	for _, path := range []string{"/api/chats/" + u.newChat() + "/draft", "/api/me/new-chat-draft"} {
		t.Run(path[:12], func(t *testing.T) {
			u.putDraft(path, `"0"`, map[string]string{"body": "phone v1"})
			u.putDraft(path, `"1"`, map[string]string{"body": "laptop v2"}) // the newer text

			d, code, _ := u.putDraft(path, `"1"`, map[string]string{"body": "phone stale"})
			if code != http.StatusConflict || d.Error.Code != "REVISION_CONFLICT" || d.Current == nil || d.Current.Body != "laptop v2" || d.Current.Revision != 2 {
				t.Fatalf("stale write: %d %+v", code, d)
			}
			if _, code, _ := u.putDraft(path, `"0"`, map[string]string{"body": "a second first write"}); code != http.StatusConflict {
				t.Fatalf("a second create over an existing draft = %d", code)
			}
			if got := u.readDraft(path); got.Body != "laptop v2" || got.Revision != 2 {
				t.Fatalf("newer text was lost: %+v", got)
			}
			if _, code, _ := u.putDraft(path, "", map[string]string{"body": "no precondition"}); code != http.StatusPreconditionRequired {
				t.Fatalf("write without If-Match = %d", code)
			}
			if _, code, _ := u.putDraft(path, "banana", map[string]string{"body": "x"}); code != http.StatusBadRequest {
				t.Fatalf("write with a malformed If-Match = %d", code)
			}
			// A stale delete must not discard the newer text either.
			if w := u.do("DELETE", path, map[string]string{"If-Match": `"1"`}, nil); w.Code != http.StatusConflict {
				t.Fatalf("stale delete = %d %s", w.Code, w.Body.String())
			}
			if w := u.do("DELETE", path, nil, nil); w.Code != http.StatusPreconditionRequired {
				t.Fatalf("delete without If-Match = %d", w.Code)
			}
			if got := u.readDraft(path); got.Body != "laptop v2" {
				t.Fatalf("stale delete removed the draft: %+v", got)
			}
			w := u.do("DELETE", path, map[string]string{"If-Match": `"2"`}, nil)
			if tomb := decodeBody[draftResp](t, w); w.Code != http.StatusOK || tomb.Revision != 3 || tomb.Body != "" {
				t.Fatalf("delete at the current revision = %d %s", w.Code, w.Body.String())
			}
			// The tombstone keeps the revision: it reads empty at 3, never back at 0.
			if got := u.readDraft(path); got.Revision != 3 || got.Body != "" {
				t.Fatalf("deleted draft reads %+v", got)
			}
			if _, code, _ := u.putDraft(path, `"0"`, map[string]string{"body": "from a device that never saw the delete"}); code != http.StatusConflict {
				t.Fatalf("a write from revision 0 over a tombstone = %d", code)
			}
			if d, code, _ := u.putDraft(path, `"3"`, map[string]string{"body": "again"}); code != 200 || d.Revision != 4 {
				t.Fatalf("write after delete: %d %+v", code, d)
			}
		})
	}
}

// Many devices write from the same revision at once: exactly one wins, everyone else is
// told, and the stored text is the winner's.
func TestConcurrentDraftWritesResolveByRevision(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	chat := u.newChat()
	for _, path := range []string{"/api/chats/" + chat + "/draft", "/api/me/new-chat-draft"} {
		const n = 12
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, codes[i], _ = u.putDraft(path, `"0"`, map[string]string{"body": fmt.Sprintf("device-%d", i)})
			}(i)
		}
		wg.Wait()
		winner, lost := -1, 0
		for i, c := range codes {
			switch c {
			case 200:
				if winner >= 0 {
					t.Fatalf("%s: two writers both won revision 0: %v", path, codes)
				}
				winner = i
			case 409:
				lost++
			default:
				t.Fatalf("%s: unexpected status %d: %v", path, c, codes)
			}
		}
		if winner < 0 || lost != n-1 {
			t.Fatalf("%s: winner %d, lost %d: %v", path, winner, lost, codes)
		}
		if got := u.readDraft(path); got.Body != fmt.Sprintf("device-%d", winner) || got.Revision != 1 {
			t.Fatalf("%s: stored %+v, winner was device-%d", path, got, winner)
		}
	}
}

// UXP-ACC-005: B guessing A's chat id gets the same 404 as an id that never existed,
// reads and changes nothing, and the new-chat draft is per user.
func TestDraftIsOwnerScoped(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chat := alice.newChat()
	path := "/api/chats/" + chat + "/draft"
	alice.putDraft(path, `"0"`, map[string]string{"body": "alice-draft-secret-61c"})
	alice.putDraft("/api/me/new-chat-draft", `"0"`, map[string]string{"body": "alice-new-secret-77d"})
	absent := "/api/chats/" + store.NewID() + "/draft"

	for _, c := range []struct {
		method string
		hdr    map[string]string
		body   any
	}{
		{"GET", nil, nil},
		{"PUT", map[string]string{"If-Match": `"1"`}, map[string]string{"body": "bob overwrites"}},
		{"PUT", map[string]string{"If-Match": `"0"`}, map[string]string{"body": "bob creates"}},
		{"DELETE", map[string]string{"If-Match": `"1"`}, nil},
	} {
		got, want := bob.do(c.method, path, c.hdr, c.body), bob.do(c.method, absent, c.hdr, c.body)
		if got.Code != 404 || !sameAnswer(got, want) {
			t.Fatalf("%s: foreign %d %q vs unknown %d %q", c.method, got.Code, got.Body.String(), want.Code, want.Body.String())
		}
		if strings.Contains(got.Body.String(), "alice-draft-secret") || strings.Contains(got.Body.String(), chat) {
			t.Fatalf("%s refusal leaks: %s", c.method, got.Body.String())
		}
	}
	if got := alice.readDraft(path); got.Body != "alice-draft-secret-61c" || got.Revision != 1 {
		t.Fatalf("alice's draft changed: %+v", got)
	}
	// The new-chat draft is keyed by the caller alone: bob sees his own (empty) one.
	if got := bob.readDraft("/api/me/new-chat-draft"); got.Body != "" || got.Revision != 0 {
		t.Fatalf("bob read %+v from the new-chat draft", got)
	}
	bob.putDraft("/api/me/new-chat-draft", `"0"`, map[string]string{"body": "bob's"})
	if got := alice.readDraft("/api/me/new-chat-draft"); got.Body != "alice-new-secret-77d" || got.Revision != 1 {
		t.Fatalf("bob's write reached alice's new-chat draft: %+v", got)
	}
	// A draft can never be addressed by a user id, a profile or a session: such a field is refused.
	for _, f := range []string{"userId", "profile", "sessionId", "chatId"} {
		if _, code, _ := alice.putDraft(path, `"1"`, map[string]any{"body": "x", f: "someone-else"}); code != 400 {
			t.Fatalf("field %q accepted: %d", f, code)
		}
	}
	// Malformed ids are plain 404s.
	if w := alice.get("/api/chats/not-an-id/draft"); w.Code != 404 {
		t.Fatalf("malformed id = %d", w.Code)
	}
	var rows int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM chat_drafts WHERE user_id = ?`, bob.id).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("bob owns %d chat_drafts rows (err %v)", rows, err)
	}
}

func TestDraftValidationAndAccountState(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	path := "/api/chats/" + u.newChat() + "/draft"
	if _, code, _ := u.putDraft(path, `"0"`, map[string]string{"body": strings.Repeat("x", store.MaxDraftBytes+1)}); code != 422 {
		t.Fatalf("oversize draft = %d", code)
	}
	if _, code, _ := u.putDraft(path, `"0"`, map[string]string{"body": strings.Repeat("é", store.MaxDraftBytes/2)}); code != 200 {
		t.Fatalf("draft at the limit = %d", code)
	}
	if w := u.get(path); w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("draft is cacheable: %v", w.Header())
	}
	for name, p := range map[string]*person{
		"pending":   a.account("pend", "USER", "PENDING", "UNPROVISIONED"),
		"suspended": a.account("susp", "USER", "SUSPENDED", "DISABLED"),
		"no tenant": a.account("nobind", "USER", "APPROVED", "UNPROVISIONED"),
	} {
		for _, path := range []string{"/api/me/new-chat-draft", "/api/me/templates"} {
			if w := p.get(path); w.Code < 400 || w.Code >= 500 {
				t.Errorf("%s: GET %s = %d", name, path, w.Code)
			}
		}
	}
}

type tplResp struct {
	ID, Name, Body string
	Error          struct{ Code string }
}

func (p *person) tpl(method, path string, body any) (tplResp, int) {
	w := p.do(method, path, nil, body)
	var r tplResp
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return r, w.Code
}

func (p *person) templates() []tplResp {
	p.a.t.Helper()
	w := p.get("/api/me/templates")
	if w.Code != 200 {
		p.a.t.Fatalf("list templates: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ Templates []tplResp }](p.a.t, w).Templates
}

// UXP-ACC-009: create/use/update/delete, owner only, and the text is stored and returned as text.
func TestTemplatesAreOwnerScopedLiteralText(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	reqs := len(a.hx.requests())

	cmd := "/run terminal rm -rf / && {{secrets}} <script>alert(1)</script>\n"
	r, code := alice.tpl("POST", "/api/me/templates", map[string]string{"name": " Deploy-Notes ", "body": cmd})
	if code != 201 || r.Name != "deploy-notes" || r.Body != cmd {
		t.Fatalf("create: %d %+v", code, r)
	}
	if _, code := alice.tpl("POST", "/api/me/templates", map[string]string{"name": "DEPLOY-notes", "body": "other"}); code != 409 {
		t.Fatalf("duplicate name (case-insensitive) = %d", code)
	}
	// Bob may use the same name: uniqueness is per owner.
	if _, code := bob.tpl("POST", "/api/me/templates", map[string]string{"name": "deploy-notes", "body": "bob's"}); code != 201 {
		t.Fatalf("same name for another owner = %d", code)
	}
	if got := alice.templates(); len(got) != 1 || got[0].Body != cmd {
		t.Fatalf("alice lists %+v", got)
	}
	if got := bob.templates(); len(got) != 1 || got[0].Body != "bob's" {
		t.Fatalf("bob lists %+v", got)
	}

	// Foreign update/delete are the same 404 as an unknown id and change nothing.
	for _, m := range []string{"PUT", "DELETE"} {
		got, gc := bob.tpl(m, "/api/me/templates/"+r.ID, map[string]string{"name": "mine-now", "body": "hijack"})
		want, wc := bob.tpl(m, "/api/me/templates/"+store.NewID(), map[string]string{"name": "mine-now", "body": "hijack"})
		if gc != 404 || wc != 404 || got.Error.Code != want.Error.Code {
			t.Fatalf("%s foreign = %d, unknown = %d", m, gc, wc)
		}
	}
	if got := alice.templates(); len(got) != 1 || got[0].Name != "deploy-notes" || got[0].Body != cmd {
		t.Fatalf("alice's template changed: %+v", got)
	}

	if u, code := alice.tpl("PUT", "/api/me/templates/"+r.ID, map[string]string{"name": "ship", "body": "v2"}); code != 200 || u.Name != "ship" || u.Body != "v2" {
		t.Fatalf("update: %d %+v", code, u)
	}
	if _, code := alice.tpl("DELETE", "/api/me/templates/"+r.ID, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if len(alice.templates()) != 0 || len(bob.templates()) != 1 {
		t.Fatal("delete touched the wrong owner's templates")
	}
	// Creating, reading or "using" a template is data only: Hermes was never contacted.
	if n := len(a.hx.requests()); n != reqs {
		t.Fatalf("template traffic made %d request(s) to Hermes", n-reqs)
	}
}

func TestTemplateValidationAndBounds(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	for name, body := range map[string]map[string]string{
		"empty name":   {"name": "", "body": "x"},
		"space inside": {"name": "a b", "body": "x"},
		"slash":        {"name": "/run", "body": "x"},
		"too long":     {"name": strings.Repeat("a", 33), "body": "x"},
		"empty body":   {"name": "ok", "body": ""},
		"big body":     {"name": "ok", "body": strings.Repeat("x", store.MaxTemplateBody+1)},
		"nul":          {"name": "ok", "body": "a\x00b"},
	} {
		if _, code := u.tpl("POST", "/api/me/templates", body); code != 422 {
			t.Errorf("%s accepted: %d", name, code)
		}
	}
	if _, code := u.tpl("POST", "/api/me/templates", map[string]any{"name": "ok", "body": "x", "command": "run"}); code != 400 {
		t.Errorf("unknown field accepted: %d", code)
	}
	for i := 0; i < store.MaxTemplates; i++ {
		if _, code := u.tpl("POST", "/api/me/templates", map[string]string{"name": fmt.Sprintf("t%d", i), "body": "x"}); code != 201 {
			t.Fatalf("template %d = %d", i, code)
		}
	}
	if _, code := u.tpl("POST", "/api/me/templates", map[string]string{"name": "one-more", "body": "x"}); code != 422 {
		t.Errorf("template over the limit = %d", code)
	}
}

// Revision ABA: device A holds revision 1; B deletes and recreates. A's stale PUT and DELETE
// must still be refused, in both scopes, and B's text must survive.
func TestDeleteRecreateNeverReusesARevision(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	for _, path := range []string{"/api/chats/" + u.newChat() + "/draft", "/api/me/new-chat-draft"} {
		u.putDraft(path, `"0"`, map[string]string{"body": "A's draft"}) // rev 1, A keeps this revision
		if w := u.do("DELETE", path, map[string]string{"If-Match": `"1"`}, nil); w.Code != 200 {
			t.Fatalf("%s: B deletes: %d", path, w.Code)
		}
		d, code, _ := u.putDraft(path, `"2"`, map[string]string{"body": "B's newer draft"})
		if code != 200 || d.Revision != 3 {
			t.Fatalf("%s: B recreates: %d %+v", path, code, d)
		}
		if _, code, _ := u.putDraft(path, `"1"`, map[string]string{"body": "A stale"}); code != http.StatusConflict {
			t.Fatalf("%s: A's stale PUT = %d", path, code)
		}
		if w := u.do("DELETE", path, map[string]string{"If-Match": `"1"`}, nil); w.Code != http.StatusConflict {
			t.Fatalf("%s: A's stale DELETE = %d", path, w.Code)
		}
		if got := u.readDraft(path); got.Body != "B's newer draft" || got.Revision != 3 {
			t.Fatalf("%s: B's text was lost: %+v", path, got)
		}
	}
}
