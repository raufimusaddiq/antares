package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Cross-user threat suite (PRD §1131 Phase 3, §25, issue #63).
//
// For every surface that exists, user B (and an approved ADMIN, and accounts
// that are not active) is refused user A's data with no content in the body,
// the refusal is indistinguishable from "no such id", and nothing reaches
// Hermes. For every surface in the Phase 3 list that is NOT built, the test
// says so, says why, and fails the day a route for it appears without a
// matching test. Run it alone with `make threat`; a failure names the surface.

// ---- what exists ------------------------------------------------------------------

// probe is one owner-scoped route. {chatId} / {runId} are filled with ids.
type probe struct {
	surface string // names the thing that broke
	key     string // "METHOD /path" exactly as in Handlers() and auth.ChatRoutes
}

var builtProbes = []probe{
	{"chat read", "GET /api/chats/{chatId}"},
	{"history/messages read", "GET /api/chats/{chatId}/messages"},
	{"send message (run admission)", "POST /api/chats/{chatId}/messages"},
	{"artifact download", "GET /api/chats/{chatId}/messages/{seq}/artifacts/{n}"},
	{"artifact preview", "GET /api/chats/{chatId}/messages/{seq}/artifacts/{n}/preview"},
	{"search within a chat", "GET /api/chats/{chatId}/search"},
	{"chat usage and budget", "GET /api/chats/{chatId}/usage"},
	{"set chat model", "PUT /api/chats/{chatId}/model"},
	{"run status", "GET /api/runs/{runId}"},
	{"run events (SSE recovery)", "GET /api/runs/{runId}/events"},
	{"run cancel", "POST /api/runs/{runId}/cancel"},
	{"steer", "POST /api/runs/{runId}/steer"},
	{"regenerate", "POST /api/chats/{chatId}/regenerate"},
	{"edit turn", "POST /api/chats/{chatId}/edit-turn"},
	{"rewind", "POST /api/chats/{chatId}/rewind"},
	{"staged audio read", "GET /api/assets/{assetId}"},
	{"staged audio delete", "DELETE /api/assets/{assetId}"},
}

// idFree are built routes with no id in the path: they must scope by the caller.
var idFree = []string{"GET /api/chats", "POST /api/chats", "GET /api/search/chats", "GET /api/me/budget", "PUT /api/me/budget", "POST /api/assets", "GET /api/models"}

// notBuilt are the §25 / Phase 3 surfaces with no route in this build. Each is
// an honest gap, not an omission: the test below proves the gap is real (no
// route, nothing leaks) and fails when a route appears.
var notBuilt = []struct {
	surface, ref, reason string
	paths                []string // where it would live; must stay unrouted
	stub                 bool     // a registered 501 placeholder rather than no route at all
}{
	{"saved drafts", "UXP-ACC-005/008", "no draft storage exists yet", []string{"/api/drafts", "/api/drafts/%s", "/api/chats/%s/draft"}, false},
	{"templates", "UXP-ACC-005/009", "no template storage exists yet", []string{"/api/templates", "/api/templates/%s"}, false},
	{"push subscriptions", "UXP-ACC-002/003", "no push subscription endpoint exists yet", []string{"/api/push/subscriptions", "/api/push/subscribe", "/api/push/subscriptions/%s"}, false},
	{"exports", "UXP-ACC-016", "no export endpoint exists yet", []string{"/api/chats/%s/export", "/api/exports", "/api/exports/%s"}, false},
	{"voice uploads", "UXP-ACC-006", "no transcription endpoint exists: Hermes has no speech-to-text route (BLOCKED_BY_UPSTREAM); /api/assets only stages audio, see assets_test.go", []string{"/api/voice", "/api/transcribe", "/api/voice/%s", "/api/assets/%s/transcribe"}, false},
	{"preview sandbox", "UXP-ACC-015", "previews are served per chat turn (probed above); no free-standing preview/artifact id exists", []string{"/api/preview", "/api/preview/%s", "/api/artifacts/%s"}, false},
	{"fork", "UXP-ACC-012", "POST /api/chats/{id}/fork is a 501 stub; Hermes forks only a whole session (docs/runbooks/turn-controls.md)", []string{"/api/chats/%s/fork"}, true},
	{"delayed notifications", "UXP-ACC-002/019", "no notification outbox or endpoint exists yet", []string{"/api/notifications", "/api/notifications/%s"}, false},
}

// ---- the fixture ----------------------------------------------------------------------

type hermesMsg = hermes.Message

type victim struct {
	a                          *app
	alice                      *person
	chatID, runID              string
	title, output, session     string
	hermesRun, ref             string
	markers                    []string // anything that must never appear in a refusal
	runRows, chatRows, hxAlice int
	assetID                    string
}

const aliceAudio = "\x1a\x45\xdf\xa3alice-voice-marker-8b2f"

func newVictim(t testing.TB) *victim {
	t.Helper()
	a := newApp(t)
	alice := a.user("alice")
	v := &victim{a: a, alice: alice, title: "alice-private-title-7f3a", output: "alice-private-answer-9c1d"}
	w := alice.do("POST", "/api/chats", nil, map[string]string{"title": v.title})
	if w.Code != 201 {
		t.Fatalf("create chat: %d %s", w.Code, w.Body.String())
	}
	v.chatID = decodeBody[struct{ ID string }](t, w).ID
	c, err := store.GetChat(a.svc.base, a.db, alice.id, v.chatID)
	if err != nil {
		t.Fatal(err)
	}
	v.session = c.HermesSessionID
	// Seed the transcript Hermes would return, then run one turn to completion.
	a.hx.mu.Lock()
	a.hx.msgs[v.session] = []map[string]any{{"role": "user", "content": "alice-private-question-2b8e"}, {"role": "assistant", "content": v.output + "\n```html\n<p>alice-artifact-secret-4e7a</p>\n```\n"}}
	a.hx.mu.Unlock()
	input := "alice-private-input-5d40"
	v.runID = alice.send(v.chatID, input)
	hid, fr := a.hx.runWithInput(t, input)
	v.hermesRun = hid
	s := alice.open(v.runID, "")
	recv(t, fr.opens, "alice's stream")
	a.hx.push(fr, "run.completed", fmt.Sprintf(`{"output":%q}`, v.output))
	s.waitFor(t, "run.completed")
	<-s.done
	w = alice.do("POST", "/api/assets", map[string]string{"Content-Type": "audio/webm"}, aliceAudio)
	if w.Code != 201 {
		t.Fatalf("stage alice's audio: %d %s", w.Code, w.Body.String())
	}
	v.assetID = decodeBody[struct{ ID string }](t, w).ID
	v.markers = []string{v.assetID, "alice-voice-marker-8b2f", v.chatID, v.runID, v.title, v.output, "alice-artifact-secret-4e7a", v.session, v.hermesRun, alice.profile, alice.key, alice.id,
		"alice-private-question-2b8e", "alice-private-input-5d40", alice.cookie}
	// Alice searches her own chats once, so her content is in the index (and her
	// transcript has been read from Hermes) before anyone else probes.
	if w := alice.get("/api/search/chats?q=alice-private"); w.Code != 200 {
		t.Fatalf("alice's own search: %d %s", w.Code, w.Body.String())
	}
	for _, m := range decodeBody[struct{ Messages []hermesMsg }](t, alice.get("/api/chats/"+v.chatID+"/messages")).Messages {
		if m.Seq == 1 {
			v.ref = m.Ref
		}
	}
	if v.ref == "" {
		t.Fatal("history stamped no ref on alice's assistant turn")
	}
	v.snapshot(t)
	return v
}

var requestIDRE = regexp.MustCompile(`"requestId":"[^"]*"`)

// sameAnswer compares two refusals ignoring the per-request correlation id.
func sameAnswer(a, b *httptest.ResponseRecorder) bool {
	n := func(w *httptest.ResponseRecorder) string {
		return requestIDRE.ReplaceAllString(w.Body.String(), `"requestId":""`)
	}
	return a.Code == b.Code && n(a) == n(b)
}

// aliceHermesReqs counts requests that reached Hermes under alice's profile.
func (v *victim) aliceHermesReqs() int {
	n := 0
	for _, r := range v.a.hx.requests() {
		if strings.HasPrefix(r.Path, "/p/"+v.alice.profile+"/") {
			n++
		}
	}
	return n
}

func (v *victim) snapshot(t testing.TB) {
	t.Helper()
	v.hxAlice = v.aliceHermesReqs()
	v.runRows, v.chatRows = v.aliceRowCounts(t)
}

// aliceRowCounts fails the test when the count cannot be read, so a broken
// query can never read as "no rows changed".
func (v *victim) aliceRowCounts(t testing.TB) (runs, chats int) {
	t.Helper()
	if err := v.a.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE user_id = ?`, v.alice.id).Scan(&runs); err != nil {
		t.Fatalf("count alice's runs: %v", err)
	}
	if err := v.a.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE user_id = ?`, v.alice.id).Scan(&chats); err != nil {
		t.Fatalf("count alice's chats: %v", err)
	}
	return runs, chats
}

// untouched fails when an attacker's probes changed A's data or reached Hermes.
func (v *victim) untouched(t testing.TB, what string) {
	t.Helper()
	runs, chats := v.aliceRowCounts(t)
	if runs != v.runRows || chats != v.chatRows {
		t.Errorf("%s: alice's rows changed (runs %d->%d, chats %d->%d)", what, v.runRows, runs, v.chatRows, chats)
	}
	var assets int
	if err := v.a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE user_id = ? AND id = ?`, v.alice.id, v.assetID).Scan(&assets); err != nil || assets != 1 {
		t.Errorf("%s: alice's staged audio is gone or unreadable (rows %d, err %v)", what, assets, err)
	}
	if st := v.alice.runStatus(v.runID); st != store.RunCompleted {
		t.Errorf("%s: alice's completed run is now %s", what, st)
	}
	if n := v.aliceHermesReqs(); n != v.hxAlice {
		t.Errorf("%s: probes made %d request(s) to Hermes under alice's profile", what, n-v.hxAlice)
	}
}

// fill is the path for a probe, including alice's real turn ref on artifact routes, so a
// refusal is about the caller and not about a missing ref.
func (v *victim) fill(key, chatID, runID string) (string, string) {
	m, p := fill(key, chatID, runID)
	asset := store.NewID() // the "absent" twin of a probe is called with other chat and run ids
	if chatID == v.chatID {
		asset = v.assetID
	}
	p = strings.ReplaceAll(p, "{assetId}", asset)
	if strings.Contains(p, "/artifacts/") {
		p += "?ref=" + v.ref
	}
	return m, p
}

func fill(key, chatID, runID string) (method, path string) {
	method, path, _ = strings.Cut(key, " ")
	path = strings.ReplaceAll(path, "{chatId}", chatID)
	path = strings.ReplaceAll(path, "{runId}", runID)
	path = strings.ReplaceAll(path, "{seq}", "1") // alice's seeded assistant turn
	path = strings.ReplaceAll(path, "{n}", "0")
	if strings.HasSuffix(path, "/search") { // a search with nothing to search for is a 400, not a test
		path += "?q=alice-private"
	}
	return
}

func requestBody(method string) any {
	if method == "POST" {
		return map[string]string{"input": "probe"}
	}
	return nil
}

func (v *victim) leaks(w *httptest.ResponseRecorder) string {
	blob := w.Body.String() + fmt.Sprint(w.Header())
	for _, m := range v.markers {
		if strings.Contains(blob, m) {
			return m
		}
	}
	return ""
}

// ---- tests ----------------------------------------------------------------------------

// Every built, owner-scoped surface refuses a foreign caller with a 404 that is
// byte-identical to the answer for an id that never existed (no existence
// oracle), carries no content, changes nothing and reaches Hermes zero times.
func TestCrossUserThreatBuiltSurfaces(t *testing.T) {
	v := newVictim(t)
	a := v.a
	attackers := map[string]*person{"approved user B": a.user("bobby")}
	admin := a.user("carol")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	attackers["approved ADMIN (admin is blind)"] = admin

	for who, atk := range attackers {
		for _, p := range builtProbes {
			t.Run(who+"/"+p.surface, func(t *testing.T) {
				before := len(a.hx.requests())
				mtd, path := v.fill(p.key, v.chatID, v.runID)
				hdr := map[string]string{"Idempotency-Key": nextIdem()}
				got := atk.do(mtd, path, hdr, requestBody(mtd))
				_, absent := v.fill(p.key, store.NewID(), store.NewID())
				want := atk.do(mtd, absent, map[string]string{"Idempotency-Key": nextIdem()}, requestBody(mtd))
				if got.Code != http.StatusNotFound && got.Code != http.StatusForbidden {
					t.Fatalf("SURFACE BROKEN [%s]: foreign caller got %d, want 404/403: %s", p.surface, got.Code, got.Body.String())
				}
				if m := v.leaks(got); m != "" {
					t.Fatalf("SURFACE BROKEN [%s]: refusal leaks %q: %s", p.surface, m, got.Body.String())
				}
				if !sameAnswer(got, want) {
					t.Fatalf("SURFACE BROKEN [%s]: foreign id answers %d %q but unknown id answers %d %q (existence oracle)",
						p.surface, got.Code, got.Body.String(), want.Code, want.Body.String())
				}
				v.untouched(t, p.surface)
				if n := len(a.hx.requests()); n != before {
					t.Fatalf("SURFACE BROKEN [%s]: a refused request made %d call(s) to Hermes", p.surface, n-before)
				}
			})
		}
	}

	t.Run("lists carry only the caller's own", func(t *testing.T) {
		for who, atk := range attackers {
			w := atk.get("/api/chats")
			if w.Code != 200 {
				t.Fatalf("%s list: %d", who, w.Code)
			}
			if m := v.leaks(w); m != "" {
				t.Errorf("SURFACE BROKEN [chat list]: %s's list leaks %q: %s", who, m, w.Body.String())
			}
			// Searching everything for A's words finds nothing of A's.
			w = atk.get("/api/search/chats?q=alice-private")
			if w.Code != 200 || v.leaks(w) != "" || strings.Contains(w.Body.String(), "alice-private") {
				t.Errorf("SURFACE BROKEN [global search]: %s: %d %s", who, w.Code, w.Body.String())
			}
			// Creating a chat must not adopt a foreign id, title or session.
			w = atk.do("POST", "/api/chats", nil, map[string]string{"title": "x"})
			if w.Code != 201 || v.leaks(w) != "" {
				t.Errorf("SURFACE BROKEN [chat create]: %s: %d %s", who, w.Code, w.Body.String())
			}
		}
		v.untouched(t, "lists")
	})

	t.Run("owner still reaches her own data", func(t *testing.T) { // the refusals above are not a broken route
		if w := v.alice.get("/api/search/chats?q=alice-private"); w.Code != 200 || !strings.Contains(w.Body.String(), v.chatID) {
			t.Errorf("owner's global search does not find her own chat: %d %s", w.Code, w.Body.String())
		}
		for _, p := range builtProbes {
			if p.key == "GET /api/runs/{runId}/events" || strings.HasPrefix(p.key, "POST") || strings.HasPrefix(p.key, "PUT") || strings.HasPrefix(p.key, "DELETE") { // the owner's own delete is proven in assets_test.go, her own model choice in models_test.go
				continue
			}
			mtd, path := v.fill(p.key, v.chatID, v.runID)
			if w := v.alice.do(mtd, path, nil, nil); w.Code != 200 {
				t.Errorf("owner %s %s = %d %s", mtd, path, w.Code, w.Body.String())
			}
		}
	})
}

// Accounts that are not active (pending, suspended, rejected) and callers with
// no session get nothing, and Hermes is not contacted.
func TestCrossUserThreatInactiveAndAnonymous(t *testing.T) {
	v := newVictim(t)
	a := v.a
	cases := map[string]*person{
		"pending P":    a.account("pend", "USER", "PENDING", "UNPROVISIONED"),
		"rejected":     a.account("rejd", "USER", "REJECTED", "UNPROVISIONED"),
		"suspended":    a.account("susp", "USER", "SUSPENDED", "DISABLED"),
		"approved, no": a.account("nobind", "USER", "APPROVED", "UNPROVISIONED"),
	}
	for who, atk := range cases {
		for _, p := range builtProbes {
			t.Run(who+"/"+p.surface, func(t *testing.T) {
				mtd, path := v.fill(p.key, v.chatID, v.runID)
				w := atk.do(mtd, path, map[string]string{"Idempotency-Key": nextIdem()}, requestBody(mtd))
				if w.Code < 400 || w.Code >= 500 {
					t.Fatalf("SURFACE BROKEN [%s]: %s got %d: %s", p.surface, who, w.Code, w.Body.String())
				}
				if m := v.leaks(w); m != "" {
					t.Fatalf("SURFACE BROKEN [%s]: leaks %q", p.surface, m)
				}
				v.untouched(t, p.surface)
			})
		}
	}
	t.Run("anonymous", func(t *testing.T) {
		for _, p := range builtProbes {
			mtd, path := v.fill(p.key, v.chatID, v.runID)
			r := httptest.NewRequest(mtd, testOrigin+path, nil)
			if mtd != "GET" {
				r.Header.Set("Origin", testOrigin)
			}
			w := httptest.NewRecorder()
			a.h.ServeHTTP(w, r)
			if w.Code != 401 || v.leaks(w) != "" {
				t.Errorf("SURFACE BROKEN [%s]: anonymous got %d %s", p.surface, w.Code, w.Body.String())
			}
		}
		v.untouched(t, "anonymous")
	})
}

// A client-supplied id of the wrong kind in the right slot (a run id as a chat
// id, a chat id as a run id) is not found, for the owner and for others.
func TestIDKindConfusionIsNotFound(t *testing.T) {
	v := newVictim(t)
	bob := v.a.user("bobby")
	for _, who := range []*person{v.alice, bob} {
		for _, path := range []string{"/api/chats/" + v.runID, "/api/chats/" + v.runID + "/messages", "/api/runs/" + v.chatID, "/api/runs/" + v.chatID + "/events"} {
			if w := who.get(path); w.Code != 404 {
				t.Errorf("%s: GET %s = %d, want 404", who.sub, path, w.Code)
			}
		}
	}
}

// Every route in auth.ChatRoutes is classified. A route added without a threat
// test breaks this test, so the suite cannot silently fall behind the product.
func TestEveryChatRouteIsClassified(t *testing.T) {
	a := newApp(t)
	built := a.svc.Handlers()
	covered := map[string]string{}
	for _, p := range builtProbes {
		covered[p.key] = "probed: " + p.surface
	}
	for _, k := range idFree {
		covered[k] = "caller-scoped list/create"
	}
	for k := range built {
		if covered[k] == "" {
			t.Errorf("route %q is built but has no cross-user probe: add it to builtProbes (or idFree) with a test", k)
		}
	}
	for k := range covered {
		if _, ok := built[k]; !ok {
			t.Errorf("%q is probed as built but the service no longer serves it", k)
		}
	}
	// Everything else in ChatRoutes is a 501 stub for everyone. Prove it: a stub
	// that quietly started answering would be an unreviewed, untested surface.
	alice, bob := a.user("alice"), a.user("bobby")
	var stubs []string
	for _, rt := range auth.ChatRoutes {
		key := rt.Method + " " + rt.Path
		if _, ok := built[key]; ok {
			continue
		}
		stubs = append(stubs, key)
		path := strings.NewReplacer("{chatId}", store.NewID(), "{runId}", store.NewID(), "{assetId}", store.NewID(), "{jobId}", store.NewID()).Replace(rt.Path)
		for who, p := range map[string]*person{"alice": alice, "bob": bob} {
			w := p.do(rt.Method, path, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"x": "y"})
			if w.Code != http.StatusNotImplemented {
				t.Errorf("NOT-YET-BUILT route %s answered %d for %s: it is now live and needs a cross-user test (builtProbes) — %s", key, w.Code, who, w.Body.String())
			}
		}
	}
	sort.Strings(stubs)
	t.Logf("not yet built (%d, 501 for every caller): %s", len(stubs), strings.Join(stubs, ", "))
}

// The Phase 3 list, surface by surface. Each is unbuilt: no route, no content,
// and no Hermes traffic. When one is built, its path stops being a bare 404 for
// both users, this fails, and the author must add a real cross-user test.
func TestCrossUserThreatNotYetBuilt(t *testing.T) {
	v := newVictim(t)
	bob := v.a.user("bobby")
	for _, s := range notBuilt {
		t.Run(s.surface+" ["+s.ref+"]", func(t *testing.T) {
			t.Logf("NOT YET BUILT: %s", s.reason)
			for _, pat := range s.paths {
				path := pat
				if strings.Contains(pat, "%s") {
					path = fmt.Sprintf(pat, v.chatID)
				}
				for _, c := range []struct {
					who string
					p   *person
				}{{"alice", v.alice}, {"bob", bob}} {
					who, p := c.who, c.p
					if who == "bob" {
						v.snapshot(t) // an owner's own reads of her transcript are legitimate; only bob's probes must be silent
					}
					for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
						w := p.do(m, path+"?q="+v.output, map[string]string{"Idempotency-Key": nextIdem()}, requestBody(m))
						ok := w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed
						if s.stub && m == "POST" {
							ok = w.Code == http.StatusNotImplemented
						}
						if !ok {
							t.Errorf("SURFACE NOW EXISTS [%s]: %s %s answered %d for %s — write its cross-user test", s.surface, m, path, w.Code, who)
						}
						if mk := v.leaks(w); mk != "" {
							t.Errorf("SURFACE BROKEN [%s]: %s %s leaks %q", s.surface, m, path, mk)
						}
					}
				}
			}
			v.untouched(t, s.surface)
		})
	}
}
