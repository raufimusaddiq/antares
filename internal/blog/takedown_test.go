package blog

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var requestID = regexp.MustCompile(`"requestId":"[^"]*"`)

// The administrator's takedown of a published post (specs/004-admin-full-control). Each test is one behaviour somebody can see.

func (a *app) admin(sub string) *person {
	a.t.Helper()
	p := a.active(sub)
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, p.id); err != nil {
		a.t.Fatal(err)
	}
	return p
}

type adminItem struct {
	ID, Slug, Title, Summary, AuthorName, PublishedAt, State string
	Takedown                                                 *struct{ At, Reason, ByName string }
}

func (p *person) takeDown(admin *person, id, reason string) *httptest.ResponseRecorder {
	return admin.do("POST", "/api/admin/blog-posts/"+id+"/take-down", map[string]string{"reason": reason})
}

func sameAnswer(t *testing.T, what string, a, b *httptest.ResponseRecorder) {
	t.Helper()
	if a.Code != b.Code || requestID.ReplaceAllString(a.Body.String(), "") != requestID.ReplaceAllString(b.Body.String(), "") || a.Header().Get("Cache-Control") != b.Header().Get("Cache-Control") {
		t.Errorf("%s: %d %q (%s) differs from %d %q (%s)", what, a.Code, a.Body.String(), a.Header().Get("Cache-Control"), b.Code, b.Body.String(), b.Header().Get("Cache-Control"))
	}
}

func TestTakeDownHidesAPostFromEveryPublicSurfaceAndRestoreReturnsItUnchanged(t *testing.T) {
	a := newApp(t)
	author, admin := a.active("writer"), a.admin("boss")
	post := author.publish("Cheap flights", "cheap-flights", "The body of the post")
	other := author.publish("Another post", "another-post", "Other body")
	pub := func(path string) *httptest.ResponseRecorder { return a.anon("GET", path) }

	before := pub("/api/public/blog/posts/cheap-flights")
	if before.Code != 200 {
		t.Fatalf("published post: %d", before.Code)
	}
	if w := author.takeDown(admin, post.ID, "Advertising a booking site"); w.Code != 200 {
		t.Fatalf("take down: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/public/blog/posts", "/api/public/blog/posts?q=flights", "/api/public/blog/posts?limit=3"} {
		if w := pub(path); strings.Contains(w.Body.String(), "cheap-flights") || strings.Contains(w.Body.String(), "Cheap flights") {
			t.Errorf("GET %s still lists the post: %s", path, w.Body)
		} else if !strings.Contains(w.Body.String(), "another-post") && path != "/api/public/blog/posts?q=flights" {
			t.Errorf("GET %s lost the author's other post: %s", path, w.Body)
		}
	}
	// A taken-down post answers exactly as an address that never existed: JSON, page and image alike.
	sameAnswer(t, "post JSON", pub("/api/public/blog/posts/cheap-flights"), pub("/api/public/blog/posts/never-existed"))
	sameAnswer(t, "post page", pub("/b/cheap-flights"), pub("/b/never-existed"))
	sameAnswer(t, "image", pub("/api/public/blog/posts/cheap-flights/assets/"+strings.Repeat("a", 32)), pub("/api/public/blog/posts/never-existed/assets/"+strings.Repeat("a", 32)))
	if w := pub("/api/public/blog/posts/another-post"); w.Code != 200 {
		t.Errorf("the author's other post: %d", w.Code)
	}

	w := admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil)
	if w.Code != 200 {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	after := pub("/api/public/blog/posts/cheap-flights")
	if after.Code != 200 || after.Body.String() != before.Body.String() {
		t.Errorf("restored post differs:\nbefore %s\nafter  %s", before.Body, after.Body)
	}
	if w := pub("/api/public/blog/posts?q=flights"); !strings.Contains(w.Body.String(), "cheap-flights") {
		t.Errorf("restored post is not searchable: %s", w.Body)
	}
	_ = other
}

func TestATakedownNeedsAReasonAndHappensOnce(t *testing.T) {
	a := newApp(t)
	author, admin := a.active("writer"), a.admin("boss")
	post := author.publish("A post", "a-post", "Body")
	draft := author.create()

	for name, reason := range map[string]string{"empty": "", "blank": "   \n ", "control": "bad\x00reason", "too long": strings.Repeat("x", 501)} {
		if w := author.takeDown(admin, post.ID, reason); w.Code != 400 {
			t.Errorf("%s reason: %d %s", name, w.Code, w.Body)
		}
	}
	if w := a.anon("GET", "/api/public/blog/posts/a-post"); w.Code != 200 {
		t.Fatalf("a refused takedown changed the post: %d", w.Code)
	}
	if w := author.takeDown(admin, post.ID, strings.Repeat("é", 500)); w.Code != 200 {
		t.Fatalf("500 characters are allowed: %d %s", w.Code, w.Body)
	}
	if w := author.takeDown(admin, post.ID, "again"); w.Code != 409 || !strings.Contains(w.Body.String(), "ALREADY_TAKEN_DOWN") {
		t.Errorf("second takedown: %d %s", w.Code, w.Body)
	}
	if w := admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil); w.Code != 200 {
		t.Fatalf("restore: %d", w.Code)
	}
	if w := admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "NOT_TAKEN_DOWN") {
		t.Errorf("second restore: %d %s", w.Code, w.Body)
	}
	// A draft, and an id nobody has, are the same 404 as each other.
	missing := admin.do("POST", "/api/admin/blog-posts/"+strings.Repeat("0", 32)+"/take-down", map[string]string{"reason": "x"})
	sameAnswer(t, "draft vs unknown", author.takeDown(admin, draft.ID, "x"), missing)
	if missing.Code != 404 {
		t.Errorf("unknown post: %d", missing.Code)
	}
}

func TestTheAuthorSeesATakedownAndCannotRepublishUntilItIsRestored(t *testing.T) {
	a := newApp(t)
	author, admin := a.active("writer"), a.admin("boss")
	post := author.publish("Flights", "flights", "Body")
	other := a.active("other")
	if w := author.takeDown(admin, post.ID, "Advertising a booking site"); w.Code != 200 {
		t.Fatal(w.Body)
	}

	list := author.do("GET", "/api/blog/posts", nil)
	if !strings.Contains(list.Body.String(), `"reason":"Advertising a booking site"`) || strings.Contains(list.Body.String(), "boss") {
		t.Errorf("author's list must show the reason and never who: %s", list.Body)
	}
	if got := into[Post](t, author.do("GET", "/api/blog/posts/"+post.ID, nil)); got.Takedown == nil || got.Takedown.Reason != "Advertising a booking site" {
		t.Errorf("author's post carries no takedown: %+v", got.Takedown)
	}
	if strings.Contains(other.do("GET", "/api/blog/posts", nil).Body.String(), "takedown") {
		t.Error("another author's list mentions a takedown")
	}
	// Editing a draft is allowed; publishing is not, and says why; nothing about the live state changes.
	cur := into[Post](t, author.do("GET", "/api/blog/posts/"+post.ID, nil))
	if w := author.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "Flights, rewritten"}); w.Code != 200 {
		t.Errorf("editing while taken down: %d %s", w.Code, w.Body)
	}
	w := author.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "original"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "TAKEN_DOWN") || !strings.Contains(w.Body.String(), "Advertising a booking site") {
		t.Errorf("publish while taken down: %d %s", w.Code, w.Body)
	}
	if a.anon("GET", "/api/public/blog/posts/flights").Code != 404 {
		t.Error("a refused publish brought the post back")
	}

	// The author unpublishes meanwhile: the mark stays, and a restore leaves it unpublished.
	if w := author.do("POST", "/api/blog/posts/"+post.ID+"/unpublish", nil); w.Code != 200 {
		t.Fatalf("unpublish: %d %s", w.Code, w.Body)
	}
	rw := admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "unpublished") {
		t.Fatalf("restore after unpublish: %d %s", rw.Code, rw.Body)
	}
	if a.anon("GET", "/api/public/blog/posts/flights").Code != 404 {
		t.Error("restoring a post its author had unpublished made it public")
	}
	if w := author.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "original"}); w.Code != 200 {
		t.Errorf("publish after restore: %d %s", w.Code, w.Body)
	}
}

func TestSuspendingThenReinstatingAnAuthorNeverRevivesATakenDownPost(t *testing.T) {
	a := newApp(t)
	author, admin := a.active("writer"), a.admin("boss")
	post := author.publish("Flights", "flights", "Body")
	if w := author.takeDown(admin, post.ID, "Spam"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	for _, st := range []string{"SUSPENDED", "APPROVED"} {
		if _, err := a.db.Exec(`UPDATE users SET approval_status = ? WHERE id = ?`, st, author.id); err != nil {
			t.Fatal(err)
		}
	}
	if a.anon("GET", "/api/public/blog/posts/flights").Code != 404 {
		t.Error("a reinstated author's taken-down post is public again")
	}
}

func TestAnAuthorHearsOfATakedownOnceAndOnlyOwnNotices(t *testing.T) {
	a := newApp(t)
	author, admin, other := a.active("writer"), a.admin("boss"), a.active("other")
	post := author.publish("Flights", "flights", "Body")
	if n := into[struct{ Notices []Notice }](t, author.do("GET", "/api/blog/notices", nil)); len(n.Notices) != 0 {
		t.Fatalf("a notice before any takedown: %+v", n)
	}
	author.takeDown(admin, post.ID, "Spam")
	n := into[struct{ Notices []Notice }](t, author.do("GET", "/api/blog/notices", nil))
	if len(n.Notices) != 1 || n.Notices[0].PostID != post.ID || n.Notices[0].Reason != "Spam" || n.Notices[0].Title != "Flights" {
		t.Fatalf("notices: %+v", n)
	}
	if strings.Contains(author.do("GET", "/api/blog/notices", nil).Body.String(), "boss") {
		t.Error("a notice names the administrator")
	}
	if got := into[struct{ Notices []Notice }](t, other.do("GET", "/api/blog/notices", nil)); len(got.Notices) != 0 {
		t.Errorf("another author sees a notice: %+v", got)
	}
	if w := other.do("POST", "/api/blog/notices/"+post.ID+"/seen", nil); w.Code != 404 {
		t.Errorf("another author's post: %d", w.Code)
	}
	if w := author.do("POST", "/api/blog/notices/"+post.ID+"/seen", nil); w.Code != 204 {
		t.Fatalf("seen: %d", w.Code)
	}
	if got := into[struct{ Notices []Notice }](t, author.do("GET", "/api/blog/notices", nil)); len(got.Notices) != 0 {
		t.Errorf("a seen notice came back: %+v", got)
	}
	admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil)
	author.takeDown(admin, post.ID, "Again")
	if got := into[struct{ Notices []Notice }](t, author.do("GET", "/api/blog/notices", nil)); len(got.Notices) != 1 || got.Notices[0].Reason != "Again" {
		t.Errorf("a second takedown is not announced: %+v", got)
	}
}

func TestOnlyAnActiveAdministratorCanListTakeDownOrRestore(t *testing.T) {
	a := newApp(t)
	author, admin := a.active("writer"), a.admin("boss")
	pending := a.user("pend", "PENDING", "UNPROVISIONED", false)
	post := author.publish("Flights", "flights", "Body")
	calls := [][3]string{
		{"GET", "/api/admin/blog-posts", ""},
		{"GET", "/api/admin/blog-posts/" + post.ID, ""},
		{"POST", "/api/admin/blog-posts/" + post.ID + "/take-down", `{"reason":"x"}`},
		{"POST", "/api/admin/blog-posts/" + post.ID + "/restore", ""},
	}
	for _, c := range calls {
		if w := a.anon(c[0], c[1]); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d", c[0], c[1], w.Code)
		}
		for who, p := range map[string]*person{"author": author, "pending": pending} {
			var body any
			if c[2] != "" {
				body = c[2]
			}
			if w := p.do(c[0], c[1], body); w.Code != 403 {
				t.Errorf("%s %s %s: %d", who, c[0], c[1], w.Code)
			}
		}
	}
	if a.anon("GET", "/api/public/blog/posts/flights").Code != 200 {
		t.Error("a refused request changed the post")
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'post.%'`).Scan(&n)
	if n != 0 {
		t.Errorf("refused requests wrote %d audit rows", n)
	}
	admin.takeDown(admin, post.ID, "Spam, as typed")
	admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil)
	rows, err := a.db.Query(`SELECT actor_user_id, action, target_id, detail FROM audit_log WHERE action LIKE 'post.%' ORDER BY created_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var actor, action, target string
		var detail *string
		_ = rows.Scan(&actor, &action, &target, &detail)
		d := ""
		if detail != nil {
			d = *detail
		}
		if actor != admin.id || target != post.ID {
			t.Errorf("audit row %s by %s on %s", action, actor, target)
		}
		got = append(got, action+":"+d)
	}
	if strings.Join(got, ",") != "post.take_down:Spam, as typed,post.restore:" {
		t.Errorf("audit rows: %v", got)
	}
}

func TestTheAdminListShowsPublishedPostsWithTheirStateAndPagesThem(t *testing.T) {
	a := newApp(t)
	good, bad, admin := a.active("good"), a.active("bad"), a.admin("boss")
	p1 := good.publish("Visible post", "visible-post", "Body")
	p2 := bad.publish("Hidden post", "hidden-post", "Body")
	p3 := good.publish("Down post", "down-post", "Body")
	good.create() // a draft: never listed
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, bad.id); err != nil {
		t.Fatal(err)
	}
	admin.takeDown(admin, p3.ID, "Spam")

	state := func(q string) map[string]string {
		w := admin.do("GET", "/api/admin/blog-posts"+q, nil)
		if w.Code != 200 {
			t.Fatalf("list %s: %d %s", q, w.Code, w.Body)
		}
		out := map[string]string{}
		for _, it := range into[struct{ Items []adminItem }](t, w).Items {
			out[it.ID] = it.State
		}
		return out
	}
	all := state("")
	if len(all) != 3 || all[p1.ID] != "visible" || all[p2.ID] != "hidden_account" || all[p3.ID] != "taken_down" {
		t.Errorf("states: %v", all)
	}
	if got := state("?state=visible"); len(got) != 1 || got[p1.ID] == "" {
		t.Errorf("visible: %v", got)
	}
	if got := state("?state=hidden_account"); len(got) != 1 || got[p2.ID] == "" {
		t.Errorf("hidden_account: %v", got)
	}
	if got := state("?state=taken_down"); len(got) != 1 || got[p3.ID] == "" {
		t.Errorf("taken_down: %v", got)
	}
	if got := state("?q=hidden"); len(got) != 1 || got[p2.ID] == "" {
		t.Errorf("search: %v", got)
	}
	if w := admin.do("GET", "/api/admin/blog-posts?state=nonsense", nil); w.Code != 400 {
		t.Errorf("bad state: %d", w.Code)
	}
	// A post hidden because its author's account is suspended can be read, and taken down, by the administrator.
	if w := admin.do("GET", "/api/admin/blog-posts/"+p2.ID, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Hidden post") {
		t.Errorf("hidden post view: %d %s", w.Code, w.Body)
	}
	if w := good.takeDown(admin, p2.ID, "Spam"); w.Code != 200 {
		t.Errorf("take down a hidden post: %d", w.Code)
	}
	// Paging: more than one page, no repeats, none missing.
	for i := 0; i < 24; i++ {
		good.publish("Bulk", "bulk-"+strings.Repeat("a", i+1), "Body")
	}
	seen, after := map[string]bool{}, ""
	for page := 0; page < 5; page++ {
		w := admin.do("GET", "/api/admin/blog-posts?after="+after, nil)
		r := into[struct {
			Items []adminItem
			Next  string
		}](t, w)
		for _, it := range r.Items {
			if seen[it.ID] {
				t.Fatalf("page %d repeats %s", page, it.ID)
			}
			seen[it.ID] = true
		}
		if r.Next == "" {
			break
		}
		after = r.Next
	}
	if len(seen) != 27 {
		t.Errorf("paging reached %d of 27 posts", len(seen))
	}
}

// An administrator may admin a post of their own like any other, and it is audited like any other.
func TestAnAdministratorCanTakeDownAndRestoreTheirOwnPost(t *testing.T) {
	a := newApp(t)
	admin := a.admin("boss")
	post := admin.publish("Mine", "mine", "Body")
	if w := admin.takeDown(admin, post.ID, "Testing my own"); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if a.anon("GET", "/api/public/blog/posts/mine").Code != 404 {
		t.Error("still public")
	}
	if w := admin.do("POST", "/api/admin/blog-posts/"+post.ID+"/restore", nil); w.Code != 200 {
		t.Fatalf("restore %d", w.Code)
	}
}

// The administrator never receives a draft, a later unpublished version, a check or anything about readers (owner decision
// 2026-11-10: published content only).
func TestAdminBlogPostRoutesReturnOnlyPublishedFields(t *testing.T) {
	a := newApp(t)
	author, admin := a.active("writer"), a.admin("boss")
	post := author.publish("Published title", "published-title", "Published body text")
	draft := author.create()
	// A later, unpublished version of the live post, and a draft of its own, with recognisable text.
	cur := into[Post](t, author.do("GET", "/api/blog/posts/"+post.ID, nil))
	later := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "LEAK-later-body"}}}}}
	if w := author.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "LEAK-later-title", "summary": "LEAK-later-summary", "doc": later}); w.Code != 200 {
		t.Fatalf("later version: %d %s", w.Code, w.Body)
	}
	dv := into[Post](t, author.do("GET", "/api/blog/posts/"+draft.ID, nil))
	author.do("PUT", "/api/blog/posts/"+draft.ID, map[string]any{"baseVersion": dv.LatestVersion, "title": "LEAK-draft-title", "doc": later})
	_, _ = a.db.Exec(`INSERT INTO post_checks (id, post_id, author_id, version, state, started_at) VALUES ('chk1', ?, ?, 1, 'ready', '2026-11-09T00:00:00Z')`, post.ID, author.id)

	check := func(when string) {
		t.Helper()
		for _, path := range []string{"/api/admin/blog-posts", "/api/admin/blog-posts/" + post.ID, "/api/admin/blog-posts?state=visible", "/api/admin/blog-posts?state=taken_down"} {
			w := admin.do("GET", path, nil)
			if w.Code != 200 {
				t.Errorf("%s: GET %s: %d", when, path, w.Code)
			}
			for _, bad := range []string{"LEAK", draft.ID, "chk1", "readers", "reads", "views"} {
				if strings.Contains(w.Body.String(), bad) {
					t.Errorf("%s: GET %s tells the administrator %q: %s", when, path, bad, w.Body)
				}
			}
		}
		if w := admin.do("GET", "/api/admin/blog-posts/"+draft.ID, nil); w.Code != 404 {
			t.Errorf("%s: a draft is not a 404: %d", when, w.Code)
		}
		if w := admin.do("GET", "/api/admin/blog-posts/"+post.ID, nil); !strings.Contains(w.Body.String(), "Published body text") || !strings.Contains(w.Body.String(), "Published title") {
			t.Errorf("%s: the published content is missing: %s", when, w.Body)
		}
	}
	check("a live post")
	author.takeDown(admin, post.ID, "Spam")
	check("a taken-down post")
	// Taken down, then unpublished by its author: the title saved at the takedown is all that is left to show.
	author.do("POST", "/api/blog/posts/"+post.ID+"/unpublish", nil)
	w := admin.do("GET", "/api/admin/blog-posts?state=taken_down", nil)
	if !strings.Contains(w.Body.String(), "Published title") || strings.Contains(w.Body.String(), "LEAK") {
		t.Errorf("an unpublished, taken-down post: %s", w.Body)
	}
	if w := admin.do("GET", "/api/admin/blog-posts/"+post.ID, nil); w.Code != 200 || strings.Contains(w.Body.String(), "LEAK") || strings.Contains(w.Body.String(), `"doc"`) {
		t.Errorf("its view: %d %s", w.Code, w.Body)
	}
}
