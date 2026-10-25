package chat

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Chat management (issue #148, UXP-ACC-018): rename, pin, archive and delete are the owner's, change
// every place the chat is mirrored, and never report a delete that did not happen. The Hermes side is
// the local fake, which models gateway/platforms/api_server.py _handle_delete_session; no production
// service is involved.

// deleteAdvertised makes /v1/capabilities advertise the session_delete route, from the recorded fixture.
func deleteAdvertised(t *testing.T, a *app) {
	t.Helper()
	a.hx.mu.Lock()
	a.hx.canned["/v1/capabilities"] = readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json")
	a.hx.mu.Unlock()
}

// upstreamDeletes returns the DELETE /api/sessions/{id} requests Hermes received.
func (a *app) upstreamDeletes() []rec {
	var out []rec
	for _, r := range a.hx.requests() {
		if r.Method == "DELETE" && strings.Contains(r.Path, "/api/sessions/") {
			out = append(out, r)
		}
	}
	return out
}

type chatJSON struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Pinned       bool   `json:"pinned"`
	Archived     bool   `json:"archived"`
	DeleteFailed bool   `json:"deleteFailed"`
}

type listJSON struct {
	Chats      []chatJSON `json:"chats"`
	NextCursor string     `json:"nextCursor"`
}

func (p *person) patch(chat string, body any) int {
	return p.do("PATCH", "/api/chats/"+chat, nil, body).Code
}

func (p *person) list(query string) listJSON {
	p.a.t.Helper()
	w := p.get("/api/chats" + query)
	if w.Code != 200 {
		p.a.t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[listJSON](p.a.t, w)
}

func (p *person) newTitledChat(title string) string {
	p.a.t.Helper()
	w := p.do("POST", "/api/chats", nil, map[string]string{"title": title})
	if w.Code != 201 {
		p.a.t.Fatalf("create chat: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[chatJSON](p.a.t, w).ID
}

// completeImageRuns finishes every fake run that carries an image: their input is a message list, which the
// fake does not index by text.
func completeImageRuns(t *testing.T, a *app, wantDone int) {
	t.Helper()
	eventually(t, func() bool {
		a.hx.mu.Lock()
		var pending []*fakeRun
		for _, fr := range a.hx.runs {
			if fr.input == "" && fr.status == "running" {
				pending = append(pending, fr)
			}
		}
		a.hx.mu.Unlock()
		for _, fr := range pending {
			a.hx.setStatus(fr, "completed", "seen")
			a.hx.push(fr, "run.completed", `{"output":"seen"}`)
		}
		return a.count(t, `SELECT COUNT(*) FROM runs WHERE status = 'COMPLETED'`) == wantDone
	}, "image runs to complete")
}

func ids(l listJSON) []string {
	var out []string
	for _, c := range l.Chats {
		out = append(out, c.ID)
	}
	return out
}

func (a *app) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// NegativeIDOR: Bob can neither change nor delete Alice's chat, by any route, and learns nothing.
func TestManagementIsOwnerScopedNegativeIDOR(t *testing.T) {
	a := newApp(t)
	deleteAdvertised(t, a)
	alice, bob := a.user("alice"), a.user("bob")
	chat := alice.newChat()

	for name, body := range map[string]any{
		"rename":  map[string]any{"title": "bob was here"},
		"pin":     map[string]any{"pinned": true},
		"archive": map[string]any{"archived": true},
	} {
		if code := bob.patch(chat, body); code != 404 {
			t.Errorf("bob %s of alice's chat = %d, want 404", name, code)
		}
	}
	// A chat that does not exist answers the same way as one that is not his.
	missing := bob.do("PATCH", "/api/chats/"+strings.Repeat("a", 32), nil, map[string]any{"pinned": true})
	foreign := bob.do("PATCH", "/api/chats/"+chat, nil, map[string]any{"pinned": true})
	if missing.Code != 404 || foreign.Code != 404 || errCode(t, missing) != errCode(t, foreign) {
		t.Errorf("foreign and missing differ: %d %s vs %d %s", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	w := bob.do("DELETE", "/api/chats/"+chat, nil, nil)
	if w.Code != 404 {
		t.Errorf("bob delete of alice's chat = %d", w.Code)
	}
	if n := len(a.upstreamDeletes()); n != 0 {
		t.Errorf("a foreign delete reached Hermes %d times", n)
	}
	got := decodeBody[chatJSON](t, alice.get("/api/chats/"+chat))
	if got.Title != "t" || got.Pinned || got.Archived {
		t.Errorf("alice's chat was changed by bob: %+v", got)
	}
	if l := bob.list(""); len(l.Chats) != 0 {
		t.Errorf("bob lists alice's chat: %v", ids(l))
	}
}

// A request that tries to choose the profile, the session or the owner is refused, not ignored.
func TestManagementRefusesServerOwnedFields(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	for _, body := range []any{
		map[string]any{"title": "x", "profile": "u_other"},
		map[string]any{"title": "x", "sessionId": "sess_1"},
		map[string]any{"pinned": true, "userId": "someone"},
		map[string]any{"title": "x", "pinned": true}, // two changes in one request
		map[string]any{},               // none
		map[string]any{"title": "   "}, // a blank name
		map[string]any{"title": nil},
	} {
		if code := u.patch(chat, body); code != 400 {
			t.Errorf("PATCH %v = %d, want 400", body, code)
		}
	}
	if got := decodeBody[chatJSON](t, u.get("/api/chats/"+chat)); got.Title != "t" || got.Pinned {
		t.Errorf("a refused request changed the chat: %+v", got)
	}
	// Same-origin is required like any mutation.
	r := u.newRequest("PATCH", "/api/chats/"+chat, map[string]string{"Origin": "https://evil.example"}, map[string]any{"pinned": true})
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code < 400 {
		t.Errorf("cross-origin PATCH = %d", w.Code)
	}
}

// Rename changes the sidebar title and the searchable title together, and only the title.
func TestRenameUpdatesListAndSearch(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newTitledChat("Roadmap draft")
	if r := searchTitles(t, u, "roadmap"); len(r) != 1 {
		t.Fatalf("setup: title not searchable: %v", r)
	}
	if code := u.patch(chat, map[string]any{"title": "  Quarterly\x07 plan \n"}); code != 200 {
		t.Fatalf("rename = %d", code)
	}
	if l := u.list(""); len(l.Chats) != 1 || l.Chats[0].Title != "Quarterly plan" {
		t.Fatalf("list after rename: %+v", l.Chats)
	}
	if r := searchTitles(t, u, "quarterly"); len(r) != 1 || r[0] != chat {
		t.Errorf("new title not searchable: %v", r)
	}
	if r := searchTitles(t, u, "roadmap"); len(r) != 0 {
		t.Errorf("old title still searchable: %v", r)
	}
	if a.count(t, `SELECT COUNT(*) FROM search_docs WHERE chat_id = ? AND seq = ?`, chat, store.TitleSeq) != 1 {
		t.Errorf("title search rows != 1")
	}
	// Rename is not activity: it does not reorder the list.
	other := u.newTitledChat("Another")
	time.Sleep(2 * time.Millisecond)
	if code := u.patch(chat, map[string]any{"title": "Renamed again"}); code != 200 {
		t.Fatal(code)
	}
	if got := ids(u.list("")); len(got) != 2 || got[0] != other {
		t.Errorf("rename moved the chat: %v (newest %s)", got, other)
	}
	// Rename never touches Hermes.
	for _, r := range a.hx.requests() {
		if r.Method == "PATCH" {
			t.Errorf("rename reached Hermes: %s %s", r.Method, r.Path)
		}
	}
}

func searchTitles(t *testing.T, p *person, q string) []string {
	t.Helper()
	var out []string
	for _, r := range doSearch(t, p, "/api/search/chats?q="+q, 200).Results {
		if r.Kind == "title" {
			out = append(out, r.ChatID)
		}
	}
	return out
}

// Pinned chats list first, the order survives paging, and an archive leaves the main list.
func TestPinArchiveOrderingAndPaging(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	var made []string
	for i := 0; i < 5; i++ {
		made = append(made, u.newTitledChat("note "+strings.Repeat("x", i+1)))
		time.Sleep(2 * time.Millisecond)
	}
	// newest first: made[4], made[3], ...; pin the two oldest.
	for _, c := range []string{made[0], made[1]} {
		if code := u.patch(c, map[string]any{"pinned": true}); code != 200 {
			t.Fatalf("pin = %d", code)
		}
	}
	want := []string{made[1], made[0], made[4], made[3], made[2]} // pinned (newest first), then the rest
	if got := ids(u.list("")); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	var paged []string
	cursor := ""
	for {
		q := "?limit=2"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		l := u.list(q)
		paged = append(paged, ids(l)...)
		if cursor = l.NextCursor; cursor == "" {
			break
		}
	}
	if strings.Join(paged, ",") != strings.Join(want, ",") {
		t.Errorf("paged = %v, want %v", paged, want)
	}

	// Archive: out of the main list, in the archive, unpinned, still openable and searchable.
	if code := u.patch(made[1], map[string]any{"archived": true}); code != 200 {
		t.Fatal(code)
	}
	if strings.Contains(strings.Join(ids(u.list("")), ","), made[1]) {
		t.Error("archived chat still in the main list")
	}
	arch := u.list("?archived=1")
	if len(arch.Chats) != 1 || arch.Chats[0].ID != made[1] || !arch.Chats[0].Archived || arch.Chats[0].Pinned {
		t.Errorf("archive = %+v", arch.Chats)
	}
	if w := u.get("/api/chats/" + made[1]); w.Code != 200 {
		t.Errorf("archived chat can't be opened: %d", w.Code)
	}
	if r := searchTitles(t, u, "note"); !strings.Contains(strings.Join(r, ","), made[1]) {
		t.Errorf("archived chat dropped out of search: %v", r)
	}
	// An archived chat cannot be pinned; unarchive then pin works.
	if code := u.patch(made[1], map[string]any{"pinned": true}); code != 409 {
		t.Errorf("pin of archived = %d, want 409", code)
	}
	if code := u.patch(made[1], map[string]any{"archived": false}); code != 200 {
		t.Fatal(code)
	}
	if code := u.patch(made[1], map[string]any{"archived": false}); code != 200 { // repeat is not an error
		t.Errorf("repeat unarchive = %d", code)
	}
	if len(u.list("?archived=1").Chats) != 0 || len(u.list("").Chats) != 5 {
		t.Error("unarchive did not restore the chat")
	}
	// A message into the archive brings the chat back.
	if code := u.patch(made[2], map[string]any{"archived": true}); code != 200 {
		t.Fatal(code)
	}
	completeRun(t, a, u, made[2], "hello again")
	if got := decodeBody[chatJSON](t, u.get("/api/chats/"+made[2])); got.Archived {
		t.Error("a new message left the chat archived")
	}
}

// Deleting removes the session upstream, then every local trace, and nothing of anyone else's.
func TestDeleteRemovesUpstreamAndEveryLocalTrace(t *testing.T) {
	a := mediaApp(t, visionModel)
	deleteAdvertised(t, a)
	alice, bob := a.user("alice"), a.user("bob")
	doomed, kept, bobs := alice.newChat(), alice.newChat(), bob.newChat()
	png := sceneFixture(t)

	a.setTranscript(t, alice, doomed, [2]string{"user", "secret launch codename"}, [2]string{"assistant", "ok"})
	a.setTranscript(t, bob, bobs, [2]string{"user", "secret launch codename"})
	if r := doSearch(t, alice, "/api/search/chats?q=codename", 200); len(r.Results) == 0 {
		t.Fatal("setup: nothing indexed")
	}
	doSearch(t, bob, "/api/search/chats?q=codename", 200)

	// A staged image sent only in the doomed chat, one sent in both chats, one never sent.
	only, shared, unsent := alice.stagePNG(png), alice.stagePNG(png), alice.stagePNG(png)
	for i, send := range []struct {
		chat, text string
		assets     []string
	}{
		{doomed, "look at this", []string{only, shared}},
		{kept, "and this", []string{shared}},
	} {
		if w := alice.sendImages(send.chat, send.text, send.assets...); w.Code != 202 {
			t.Fatalf("send: %d %s", w.Code, w.Body.String())
		}
		eventually(t, func() bool { return len(a.runPosts(t)) == i+1 }, "run to reach Hermes")
		completeImageRuns(t, a, i+1)
	}
	if code := alice.do("PUT", "/api/chats/"+doomed+"/draft", map[string]string{"If-Match": `"0"`}, map[string]string{"body": "unsent thought"}).Code; code != 200 {
		t.Fatalf("draft: %d", code)
	}
	if _, err := a.db.Exec(`UPDATE runs SET input_tokens = 100, output_tokens = 50 WHERE chat_id = ?`, doomed); err != nil {
		t.Fatal(err)
	}
	usedBefore, _, _ := store.UserReportedTokens(context.Background(), a.db, alice.id)
	session := decodeSession(t, a, alice, doomed)

	w := alice.do("DELETE", "/api/chats/"+doomed, nil, nil)
	if w.Code != 200 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}

	// Upstream: exactly one delete, on Alice's own profile route and key, for her session.
	dels := a.upstreamDeletes()
	if len(dels) != 1 || dels[0].Path != "/p/"+alice.profile+"/api/sessions/"+session || dels[0].Auth != "Bearer "+alice.key {
		t.Fatalf("upstream deletes = %+v", dels)
	}
	// Local: the chat and everything hanging off it.
	for table, q := range map[string]string{
		"chats":        `SELECT COUNT(*) FROM chats WHERE id = ?`,
		"runs":         `SELECT COUNT(*) FROM runs WHERE chat_id = ?`,
		"search_docs":  `SELECT COUNT(*) FROM search_docs WHERE chat_id = ?`,
		"search_state": `SELECT COUNT(*) FROM search_state WHERE chat_id = ?`,
		"chat_drafts":  `SELECT COUNT(*) FROM chat_drafts WHERE chat_id = ?`,
		"messages":     `SELECT COUNT(*) FROM messages WHERE chat_id = ?`,
	} {
		if n := a.count(t, q, doomed); n != 0 {
			t.Errorf("%s still has %d rows for the deleted chat", table, n)
		}
	}
	if a.count(t, `SELECT COUNT(*) FROM run_images WHERE run_id NOT IN (SELECT id FROM runs)`) != 0 {
		t.Error("orphan run_images")
	}
	// Assets: the image only the deleted chat sent is gone; the one the other chat also sent, and the unsent one, stay.
	for id, wantGone := range map[string]bool{only: true, shared: false, unsent: false} {
		if w := alice.get("/api/assets/" + id); (w.Code == 404) != wantGone {
			t.Errorf("asset %s: status %d, gone want %v", id[:6], w.Code, wantGone)
		}
	}
	// Search no longer finds it, and the other user and the other chat are untouched.
	if r := doSearch(t, alice, "/api/search/chats?q=codename", 200); len(r.Results) != 0 {
		t.Errorf("deleted chat still searchable: %+v", r.Results)
	}
	if r := doSearch(t, bob, "/api/search/chats?q=codename", 200); len(r.Results) == 0 {
		t.Error("alice's delete removed bob's search rows")
	}
	if got := alice.get("/api/chats/" + doomed); got.Code != 404 {
		t.Errorf("deleted chat opens: %d", got.Code)
	}
	if got := alice.get("/api/chats/" + doomed + "/draft"); got.Code != 404 {
		t.Errorf("deleted chat's draft reads: %d", got.Code)
	}
	if got := alice.get("/api/chats/" + kept); got.Code != 200 {
		t.Errorf("her other chat vanished: %d", got.Code)
	}
	if got := bob.get("/api/chats/" + bobs); got.Code != 200 {
		t.Errorf("bob's chat vanished: %d", got.Code)
	}
	if len(alice.list("").Chats) != 1 {
		t.Errorf("list after delete: %v", ids(alice.list("")))
	}
	// A second delete is a plain 404 and sends nothing upstream.
	if got := alice.do("DELETE", "/api/chats/"+doomed, nil, nil); got.Code != 404 || len(a.upstreamDeletes()) != 1 {
		t.Errorf("repeat delete = %d, upstream %d", got.Code, len(a.upstreamDeletes()))
	}
	// Deleting a chat does not reset the token budget.
	if used, _, _ := store.UserReportedTokens(context.Background(), a.db, alice.id); used != usedBefore || used < 150 {
		t.Errorf("reported tokens %d -> %d across a delete", usedBefore, used)
	}
	if _, _, err := store.AdmitRunWithin(context.Background(), a.db, alice.id, kept, "idem-after-delete", time.Now(), 150); err != store.ErrBudget {
		t.Errorf("a budget the deleted chat had used was reset: %v", err)
	}

	// The audit trail names the action and the opaque chat id, and carries no content.
	var rows []string
	q, err := a.db.Query(`SELECT action||'|'||COALESCE(target_kind,'')||'|'||COALESCE(target_id,'')||'|'||outcome FROM audit_log WHERE action LIKE 'chat.%'`)
	if err != nil {
		t.Fatal(err)
	}
	for q.Next() {
		var s string
		_ = q.Scan(&s)
		rows = append(rows, s)
	}
	q.Close()
	joinedRows := strings.Join(rows, "\n")
	if joinedRows != "chat.delete|chat|"+doomed+"|ok" {
		t.Errorf("audit = %q", joinedRows)
	}
	for _, leak := range []string{session, alice.profile, alice.key, "codename", "unsent thought"} {
		if strings.Contains(joinedRows, leak) {
			t.Errorf("audit carries %q", leak)
		}
	}
}

// Every upstream failure leaves the chat where it was, flagged, with its data; a retry can finish it.
func TestDeleteFailureIsVisibleAndRetryable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		http   int
	}{
		{"hermes down", 503, "HERMES_UNAVAILABLE", 503},
		{"turn active upstream", 409, "RUN_CONFLICT", 409},
		{"profile route unknown", 404, "CAPABILITY_UNAVAILABLE", 501},
		{"upstream timeout", 504, "UPSTREAM_TIMEOUT", 504},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t)
			deleteAdvertised(t, a)
			u := a.user("alice")
			chat := u.newChat()
			a.setTranscript(t, u, chat, [2]string{"user", "keep me findable"})
			doSearch(t, u, "/api/search/chats?q=findable", 200)
			if code := u.do("PUT", "/api/chats/"+chat+"/draft", map[string]string{"If-Match": `"0"`}, map[string]string{"body": "draft"}).Code; code != 200 {
				t.Fatal(code)
			}
			a.hx.mu.Lock()
			a.hx.failNext["DELETE /api/sessions/"] = c.status
			a.hx.mu.Unlock()

			w := u.do("DELETE", "/api/chats/"+chat, nil, nil)
			if w.Code != c.http || errCode(t, w) != c.code {
				t.Fatalf("delete = %d %s, want %d %s", w.Code, w.Body.String(), c.http, c.code)
			}
			if strings.Contains(w.Body.String(), u.key) {
				t.Error("the upstream key was echoed to the browser")
			}
			// Still there, flagged, with its data; not presented as deleted.
			l := u.list("")
			if len(l.Chats) != 1 || l.Chats[0].ID != chat || !l.Chats[0].DeleteFailed {
				t.Fatalf("list after failure = %+v", l.Chats)
			}
			if u.get("/api/chats/"+chat).Code != 200 {
				t.Error("a chat whose delete failed can't be opened")
			}
			if u.readDraft("/api/chats/"+chat+"/draft").Body != "draft" {
				t.Error("the draft went with a delete that failed")
			}
			if r := doSearch(t, u, "/api/search/chats?q=findable", 200); len(r.Results) == 0 {
				t.Error("search lost the chat of a failed delete")
			}
			if got := a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'chat.delete' AND outcome = 'failed:'||?`, c.code); got != 1 {
				t.Errorf("failed delete audited %d times", got)
			}
			// Retry once Hermes answers.
			if w := u.do("DELETE", "/api/chats/"+chat, nil, nil); w.Code != 200 {
				t.Fatalf("retry = %d %s", w.Code, w.Body.String())
			}
			if len(u.list("").Chats) != 0 || a.count(t, `SELECT COUNT(*) FROM chats`) != 0 {
				t.Error("retry left the chat behind")
			}
		})
	}
}

// Hermes says the session is already gone: the job is finished locally. Any other 404 is not that answer.
func TestDeleteTreatsOnlyHermesSessionNotFoundAsGone(t *testing.T) {
	a := newApp(t)
	deleteAdvertised(t, a)
	u := a.user("alice")
	chat := u.newChat()
	a.hx.mu.Lock()
	delete(a.hx.sessions, decodeSession(t, a, u, chat)) // deleted upstream by someone else
	a.hx.mu.Unlock()
	if w := u.do("DELETE", "/api/chats/"+chat, nil, nil); w.Code != 200 {
		t.Fatalf("already gone upstream = %d %s", w.Code, w.Body.String())
	}
	if a.count(t, `SELECT COUNT(*) FROM chats`) != 0 {
		t.Error("local chat left after upstream said not found")
	}
}

// A Hermes that does not advertise the route is never sent a delete, and nothing changes locally.
func TestDeleteIsUnsupportedWithoutTheAdvertisedRoute(t *testing.T) {
	for name, caps := range map[string]string{
		"no discovery document": "",
		"route missing":         `{"object":"hermes.api_server.capabilities","features":{},"endpoints":{}}`,
		"wrong method":          `{"object":"hermes.api_server.capabilities","features":{},"endpoints":{"session_delete":{"method":"POST","path":"/api/sessions/{session_id}"}}}`,
		"other path":            `{"object":"hermes.api_server.capabilities","features":{},"endpoints":{"session_delete":{"method":"DELETE","path":"/api/other/{session_id}"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			a := newApp(t)
			if caps != "" {
				a.hx.mu.Lock()
				a.hx.canned["/v1/capabilities"] = caps
				a.hx.mu.Unlock()
			}
			u := a.user("alice")
			chat := u.newChat()
			w := u.do("DELETE", "/api/chats/"+chat, nil, nil)
			if w.Code != 501 || errCode(t, w) != "CAPABILITY_UNAVAILABLE" {
				t.Fatalf("delete = %d %s", w.Code, w.Body.String())
			}
			if len(a.upstreamDeletes()) != 0 {
				t.Error("a delete was sent to a Hermes that did not advertise it")
			}
			if l := u.list(""); len(l.Chats) != 1 || l.Chats[0].DeleteFailed {
				t.Errorf("an unsupported delete changed the chat: %+v", l.Chats)
			}
			// The UI reads the same truth.
			var found bool
			for _, c := range decodeBody[struct{ Capabilities []struct{ ID, Status string } }](t, u.get("/api/capabilities")).Capabilities {
				if c.ID == "chat_delete" {
					found = true
					if c.Status != "unsupported" {
						t.Errorf("chat_delete = %s", c.Status)
					}
				}
			}
			if !found {
				t.Error("capabilities has no chat_delete verdict")
			}
		})
	}
}

// A reply in progress blocks the delete before anything is claimed or sent.
func TestDeleteWaitsForTheRunToEnd(t *testing.T) {
	a := newApp(t)
	deleteAdvertised(t, a)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "think for a while")
	_, fr := a.hx.runWithInput(t, "think for a while")

	w := u.do("DELETE", "/api/chats/"+chat, nil, nil)
	if w.Code != 409 || errCode(t, w) != "RUN_CONFLICT" {
		t.Fatalf("delete during a run = %d %s", w.Code, w.Body.String())
	}
	if len(a.upstreamDeletes()) != 0 {
		t.Error("a delete was sent under a running turn")
	}
	if got := decodeBody[chatJSON](t, u.get("/api/chats/"+chat)); got.DeleteFailed || got.Archived {
		t.Errorf("a refused delete changed the chat: %+v", got)
	}
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "run.completed", `{"output":"done"}`)
	s.waitFor(t, "run.completed")
	<-s.done
	if w := u.do("DELETE", "/api/chats/"+chat, nil, nil); w.Code != 200 {
		t.Fatalf("delete after the run = %d %s", w.Code, w.Body.String())
	}
}

// No healthy binding, no Hermes request, and no local change either.
func TestDeleteWithoutAHealthyBindingTouchesNothing(t *testing.T) {
	a := newApp(t)
	deleteAdvertised(t, a)
	u := a.user("alice")
	chat := u.newChat()
	if _, err := a.db.Exec(`UPDATE tenant_bindings SET status = 'FAILED' WHERE user_id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	w := u.do("DELETE", "/api/chats/"+chat, nil, nil)
	if w.Code < 400 {
		t.Fatalf("delete with a degraded binding = %d", w.Code)
	}
	if len(a.upstreamDeletes()) != 0 {
		t.Error("a delete reached Hermes without a healthy binding")
	}
	if a.count(t, `SELECT COUNT(*) FROM chats WHERE id = ? AND status = 'ACTIVE'`, chat) != 1 {
		t.Error("the chat changed state")
	}
	// Rename and pin are local and need no Hermes, but they still require the account to be eligible: the guard is unchanged.
}

// Store level: a run cannot be admitted into a chat that is on its way out, a restart settles
// a half-done delete as failed, and the owner is in every statement.
func TestStoreDeleteLifecycle(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bob")
	chat := alice.newChat()
	ctx := context.Background()
	now := time.Now()

	if _, err := store.BeginDeleteChat(ctx, a.db, bob.id, chat, now); err != store.ErrNoChat {
		t.Fatalf("bob claimed alice's chat: %v", err)
	}
	if _, err := store.FinishDeleteChat(ctx, a.db, alice.id, chat); err != store.ErrNoChat {
		t.Fatalf("finish without a claim = %v", err)
	}
	if _, err := store.BeginDeleteChat(ctx, a.db, alice.id, chat, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AdmitRun(ctx, a.db, alice.id, chat, "idem-into-delete", now); err != store.ErrNoChat {
		t.Errorf("a run was admitted into a chat being deleted: %v", err)
	}
	if _, err := store.FinishDeleteChat(ctx, a.db, bob.id, chat); err != store.ErrNoChat {
		t.Errorf("bob finished alice's delete: %v", err)
	}
	if a.count(t, `SELECT COUNT(*) FROM chats WHERE id = ?`, chat) != 1 {
		t.Fatal("foreign finish removed the chat")
	}
	// The process dies between the claim and the answer.
	if _, _, err := store.RecoverRuns(ctx, a.db, now); err != nil {
		t.Fatal(err)
	}
	if a.count(t, `SELECT COUNT(*) FROM chats WHERE id = ? AND delete_failed_at IS NOT NULL`, chat) != 1 {
		t.Error("a delete interrupted by a restart is not shown as failed")
	}
	if _, err := store.BeginDeleteChat(ctx, a.db, alice.id, chat, now); err != nil {
		t.Errorf("retry of a failed delete: %v", err)
	}
}

// A failed delete puts the chat back exactly as it was: an archived chat stays in the archive and a pinned
// one stays pinned, both flagged; the next successful delete still works from there.
func TestFailedDeleteKeepsArchivedAndPinnedState(t *testing.T) {
	a := newApp(t)
	deleteAdvertised(t, a)
	u := a.user("alice")
	archived, pinned, plain := u.newTitledChat("old stuff"), u.newTitledChat("favourite"), u.newTitledChat("plain")
	if u.patch(archived, map[string]any{"archived": true}) != 200 || u.patch(pinned, map[string]any{"pinned": true}) != 200 {
		t.Fatal("setup")
	}
	for _, id := range []string{archived, pinned, plain} {
		a.hx.mu.Lock()
		a.hx.failNext["DELETE /api/sessions/"] = 503
		a.hx.mu.Unlock()
		if w := u.do("DELETE", "/api/chats/"+id, nil, nil); w.Code != 503 {
			t.Fatalf("delete %s = %d", id, w.Code)
		}
	}
	main, arch := u.list(""), u.list("?archived=1")
	if got := ids(main); len(got) != 2 || got[0] != pinned || got[1] != plain {
		t.Errorf("main list = %v, want pinned then plain", got)
	}
	if !main.Chats[0].Pinned || !main.Chats[0].DeleteFailed || !main.Chats[1].DeleteFailed || main.Chats[0].Archived {
		t.Errorf("main flags = %+v", main.Chats)
	}
	if len(arch.Chats) != 1 || arch.Chats[0].ID != archived || !arch.Chats[0].Archived || !arch.Chats[0].DeleteFailed {
		t.Errorf("archive = %+v", arch.Chats)
	}
	// Retrying the archived one deletes it, from the archive.
	if w := u.do("DELETE", "/api/chats/"+archived, nil, nil); w.Code != 200 {
		t.Fatalf("retry = %d %s", w.Code, w.Body.String())
	}
	if len(u.list("?archived=1").Chats) != 0 {
		t.Error("archive still lists the deleted chat")
	}
}

// Two overlapping deletes of one chat: the first claims it and its upstream request is held; the second is refused
// with a conflict (not a 404 a browser would read as "deleted"), sends nothing upstream, and no run is admitted
// into the chat until the first settles. When the first fails, the chat comes back as it was.
func TestOverlappingDeleteCallsHermesOnceAndNeverReportsGone(t *testing.T) {
	a := newApp(t)
	deleteAdvertised(t, a)
	alice, bob := a.user("alice"), a.user("bob")
	chat := alice.newTitledChat("contested")
	if alice.patch(chat, map[string]any{"archived": true}) != 200 {
		t.Fatal("setup")
	}
	if code := alice.do("PUT", "/api/chats/"+chat+"/draft", map[string]string{"If-Match": `"0"`}, map[string]string{"body": "keep this"}).Code; code != 200 {
		t.Fatal(code)
	}
	hold := make(chan struct{})
	a.hx.mu.Lock()
	a.hx.holdDelete = hold
	a.hx.failNext["DELETE /api/sessions/"] = 503 // the held upstream delete will fail once released
	a.hx.mu.Unlock()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- alice.do("DELETE", "/api/chats/"+chat, nil, nil) }()
	eventually(t, func() bool { return len(a.upstreamDeletes()) == 1 }, "the first delete to reach Hermes")
	if a.count(t, `SELECT COUNT(*) FROM chats WHERE id = ? AND status = 'PENDING_DELETE'`, chat) != 1 {
		t.Fatal("the first delete has not claimed the chat")
	}

	second := alice.do("DELETE", "/api/chats/"+chat, nil, nil)
	if second.Code != 409 || errCode(t, second) != "CHAT_STATE" {
		t.Errorf("overlapping delete = %d %s, want 409 CHAT_STATE", second.Code, second.Body.String())
	}
	if n := len(a.upstreamDeletes()); n != 1 {
		t.Errorf("Hermes was asked to delete %d times", n)
	}
	// The pending chat's existence is the owner's alone.
	if w := bob.do("DELETE", "/api/chats/"+chat, nil, nil); w.Code != 404 {
		t.Errorf("another account's delete of a pending chat = %d, want 404", w.Code)
	}
	// No run joins a chat being deleted, and nothing else about it is changed meanwhile.
	if w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "too late"}); w.Code != 404 {
		t.Errorf("message into a chat being deleted = %d, want 404", w.Code)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM runs WHERE chat_id = ?`, chat); n != 0 {
		t.Errorf("%d run(s) admitted before the delete settled", n)
	}
	if w := alice.do("PATCH", "/api/chats/"+chat, nil, map[string]any{"archived": false}); w.Code != 404 {
		t.Errorf("unarchive of a chat being deleted = %d, want 404", w.Code)
	}

	close(hold)
	if w := <-first; w.Code != 503 {
		t.Fatalf("the first delete = %d %s, want 503", w.Code, w.Body.String())
	}
	// Settled: back in the archive, flagged, draft intact, deletable again, with still exactly one upstream call so far.
	arch := alice.list("?archived=1")
	if len(arch.Chats) != 1 || arch.Chats[0].ID != chat || !arch.Chats[0].DeleteFailed || len(alice.list("").Chats) != 0 {
		t.Errorf("after the failure: archive %+v main %+v", arch.Chats, alice.list("").Chats)
	}
	if alice.readDraft("/api/chats/"+chat+"/draft").Body != "keep this" {
		t.Error("the draft did not survive")
	}
	if n := len(a.upstreamDeletes()); n != 1 {
		t.Errorf("upstream deletes = %d", n)
	}
	a.hx.mu.Lock()
	a.hx.holdDelete = nil
	a.hx.mu.Unlock()
	if w := alice.do("DELETE", "/api/chats/"+chat, nil, nil); w.Code != 200 {
		t.Errorf("retry after settlement = %d %s", w.Code, w.Body.String())
	}
}
