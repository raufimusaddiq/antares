package blog

import (
	"net/http"
	"strings"
	"testing"
)

// The author's list (T065): only their own posts, with the status each one is really in.
func TestTheListCarriesEachPostsRealStatus(t *testing.T) {
	a := newApp(t)
	alice, bob := a.active("alice"), a.active("bob")

	want := map[string]string{} // post id -> status
	want[alice.create().ID] = "draft"

	pub := alice.publish("Published one", "published-one", "x")
	want[pub.ID] = "published"

	gone := alice.publish("Taken down", "taken-down", "x")
	if w := alice.do("POST", "/api/blog/posts/"+gone.ID+"/unpublish", nil); w.Code != http.StatusOK {
		t.Fatalf("unpublish: %d %s", w.Code, w.Body.String())
	}
	want[gone.ID] = "unpublished"

	// A check in progress and a finished one are states the run machinery sets; the list only has to report them.
	for _, st := range []string{"checking", "ready"} {
		id := alice.create().ID
		if _, err := a.db.Exec(`UPDATE posts SET status = ? WHERE id = ?`, st, id); err != nil {
			t.Fatal(err)
		}
		want[id] = st
	}

	// Another author with posts in several states: none of them may show up for alice.
	for i := 0; i < 3; i++ {
		bob.create()
	}
	bob.publish("Bob's post", "bobs-post", "x")

	got := into[[]Summary](t, alice.do("GET", "/api/blog/posts", nil))
	if len(got) != len(want) {
		t.Fatalf("alice sees %d posts, wants %d: %+v", len(got), len(want), got)
	}
	seen := map[string]bool{}
	for _, s := range got {
		st, mine := want[s.ID]
		if !mine {
			t.Fatalf("alice's list holds a post that is not hers: %+v", s)
		}
		if s.Status != st {
			t.Fatalf("post %s is %q, the list says %q", s.ID, st, s.Status)
		}
		seen[s.Status] = true
	}
	for _, st := range []string{"draft", "checking", "ready", "published", "unpublished"} {
		if !seen[st] {
			t.Fatalf("no post in the %q state came back: %+v", st, got)
		}
	}
	for _, s := range got {
		if s.ID == pub.ID && (s.Slug != "published-one" || s.PublishedAt == "") {
			t.Fatalf("a published post lists its address and publication time: %+v", s)
		}
	}

	if raw := alice.do("GET", "/api/blog/posts", nil).Body.String(); strings.Contains(raw, "Bob's post") || strings.Contains(raw, "bobs-post") {
		t.Fatalf("another author's post leaked into the list: %s", raw)
	}
}

// An author with no posts gets an empty array, not null: the list screen reads .length.
func TestAnEmptyListIsAnArray(t *testing.T) {
	a := newApp(t)
	w := a.active("alice").do("GET", "/api/blog/posts", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %q", w.Code, w.Body.String())
	}
}
