package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestBundleIsEmbeddedAndSelfConsistent(t *testing.T) {
	html, err := fs.ReadFile(FS(), "index.html")
	if err != nil || len(html) == 0 {
		t.Fatalf("no built index.html in the embedded bundle (run `npm run build` in web/): %v", err)
	}
	s := string(html)
	if !strings.Contains(s, `<div id="root">`) || strings.Contains(s, "{{") {
		t.Fatalf("index.html is not the React shell: %s", s)
	}
	refs := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(s, -1)
	if len(refs) < 2 {
		t.Fatalf("shell references %d assets, want script and stylesheet", len(refs))
	}
	for _, m := range refs {
		if b, err := fs.ReadFile(FS(), strings.TrimPrefix(m[1], "/")); err != nil || len(b) == 0 {
			t.Errorf("shell references %s which is not in the bundle: %v", m[1], err)
		}
	}
}

// The same algorithm as web/scripts/stamp.mjs: sha256 over, for each source file
// in sorted path order, path NUL content NUL. Test files are not part of the bundle.
func TestBundleIsNotStale(t *testing.T) {
	web := filepath.Join("..", "..", "web")
	var files []string
	// src/ and public/ (fonts, icons, the OG card) both ship in the bundle.
	for _, root := range []string{"src", "public"} {
		err := filepath.WalkDir(filepath.Join(web, root), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || regexp.MustCompile(`\.test\.tsx?$`).MatchString(d.Name()) {
				return err
			}
			rel, _ := filepath.Rel(web, p)
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	files = append(files, "index.html", "package.json", "package-lock.json", "vite.config.ts", "tsconfig.json", "scripts/stamp.mjs")
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(web, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	got := hex.EncodeToString(h.Sum(nil))
	want, err := fs.ReadFile(FS(), "source-hash.txt")
	if err != nil {
		t.Fatalf("bundle has no source-hash.txt; run `npm run build` in web/: %v", err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Fatalf("web/ sources changed since the committed bundle was built.\n  sources: %s\n  bundle:  %s\nRun `npm ci && npm run build` in web/ and commit internal/webui/dist.", got, strings.TrimSpace(string(want)))
	}
}

// Structural, not textual: the only backend paths the app can name, and nothing
// that looks like a credential or a profile slug, in the source or the bundle.
func TestBrowserCodeReachesOnlyTheIdentityAPI(t *testing.T) {
	// Slice 4 (#23) adds the chat routes, listed here one by one. The patterns match
	// up to a template-literal "${": /api/chats/ is /api/chats/{id}[/messages] and
	// /api/runs/ is /api/runs/{id}[/events|/cancel]. Nothing else was widened.
	allowed := map[string]bool{
		"/api/me": true, "/api/auth/logout": true, "/api/auth/logout-all": true, "/api/auth/google/start": true,
		"/api/chats": true, "/api/chats/": true, "/api/runs/": true,
		// #47 adds the admin screens, again one by one: the user list, /api/admin/users/{id}
		// and its action POSTs, and the audit feed. /api/admin/system is not here because
		// the route no longer exists.
		"/api/admin/users": true, "/api/admin/users/": true, "/api/admin/audit": true,
		// #146 the shared provider account's balance, quota and usage: one admin-only read, no body, no key.
		"/api/admin/kenari-usage": true,
		// #59 adds the opt-in notification routes, listed one by one: the VAPID public key,
		// device (un)registration (/api/push/subscriptions/{id} is the revoke), and the
		// per-account mode. None of them returns or accepts conversation content.
		"/api/push/public-key": true, "/api/push/subscriptions": true, "/api/push/subscriptions/": true,
		"/api/me/notifications": true,
		// #60 chat search: /api/search/chats; the per-chat form is /api/chats/{id}/search, covered above.
		"/api/search/chats": true,
		// #81 per-user opt-in token limit.
		"/api/me/budget": true, "/api/me/spending-limits": true,
		// #100 model picker inventory; the per-chat choice is /api/chats/{id}/model, covered above.
		"/api/models": true,
		// #115 the account's own model visibility; the per-chat settings are /api/chats/{id}/settings, covered above.
		"/api/me/model-visibility": true,
		// Phase 2b slice 1: live capability discovery (account-scoped, no conversation content).
		"/api/capabilities": true,
		// #107 server drafts and personal templates: the new-chat draft, the template list/create and
		// /api/me/templates/{id}; a chat's own draft is /api/chats/{id}/draft, covered above.
		// #101 staged image and audio assets: upload/preview/delete by the server-issued asset id. Owner-checked
		// on every call; the page never builds one from message text.
		"/api/assets": true, "/api/assets/": true,
		// #151 the caller's own usage over 7/30/90 days.
		"/api/me/usage": true,
		// #113 diagnostics: the running build's commit, public, nothing else. The admin "sign out everywhere" is
		// /api/admin/users/{id}/revoke-sessions, covered above.
		"/api/version":           true,
		"/api/me/new-chat-draft": true, "/api/me/templates": true, "/api/me/templates/": true,
	}
	pathRe := regexp.MustCompile(`/api/[A-Za-z0-9_/.-]*`)
	badRe := regexp.MustCompile(`(?i)ciphertext|fingerprint|tenant_binding|profile_name|profileSlug|memory_scope|client_secret|api_key|apikey|hermes|authorization`)
	// The bare word also appears in React DOM's own code (`use-credentials`), so it
	// is checked in our source only, where fetch's pinned option is the sole use.
	wordRe := regexp.MustCompile(`(?i)credential`)

	// The one legitimate use of the word: fetch's `credentials` option, pinned to same-origin.
	fetchOption := regexp.MustCompile(`credentials:\s*"same-origin"`)
	// The one legitimate use of the upstream's name: the API error code
	// HERMES_UNAVAILABLE (PRD §9.1), which the browser must recognise to explain a
	// failure. It is a public constant, not a route, key or profile. Any other
	// mention of the word is still refused.
	apiCode := regexp.MustCompile(`HERMES_UNAVAILABLE`)
	check := func(name string, b []byte, ours bool) {
		if ours && wordRe.Match(fetchOption.ReplaceAll(b, nil)) {
			t.Errorf("%s mentions credentials beyond fetch's same-origin option", name)
		}
		for _, p := range pathRe.FindAllString(string(b), -1) {
			if !allowed[p] {
				t.Errorf("%s names backend path %s", name, p)
			}
		}
		if m := badRe.FindString(string(apiCode.ReplaceAll(b, nil))); m != "" {
			t.Errorf("%s contains %q: nothing credential-shaped may reach the browser", name, m)
		}
	}
	n := 0
	_ = filepath.WalkDir(filepath.Join("..", "..", "web", "src"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(d.Name(), ".test.") {
			b, _ := os.ReadFile(p)
			check(p, b, true)
			n++
		}
		return nil
	})
	_ = fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".css")) {
			b, _ := fs.ReadFile(FS(), p)
			check("bundle:"+p, b, false)
			n++
		}
		return nil
	})
	if n < 6 {
		t.Fatalf("scanned only %d files", n)
	}
}

// PERF-001: the OpenUI runtime and the chart renderer are on-demand chunks. The
// shell must not load or preload them, and the entry script must not contain
// their code, so a chat with no rich content never pays for them.
func TestOpenUIChunksAreNotInTheInitialBundle(t *testing.T) {
	html, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	// Strings that exist only in the lazy modules: Block.tsx (renderer) and Chart.tsx.
	lazy := map[string]string{"Block-": "ou-block", "Chart-": "ou-chart-svg"}
	for prefix := range lazy {
		if strings.Contains(string(html), "/assets/"+prefix) {
			t.Errorf("index.html loads or preloads the %s chunk", prefix)
		}
	}
	entries := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindAllStringSubmatch(string(html), -1)
	if len(entries) == 0 {
		t.Fatal("no entry script in index.html")
	}
	for _, m := range entries {
		b, err := fs.ReadFile(FS(), strings.TrimPrefix(m[1], "/"))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range lazy {
			if strings.Contains(string(b), marker) {
				t.Errorf("entry script %s contains %q, which belongs to a lazy chunk", m[1], marker)
			}
		}
	}
	for prefix, marker := range lazy {
		files, _ := fs.Glob(FS(), "assets/"+prefix+"*.js")
		if len(files) != 1 {
			t.Fatalf("want exactly one %s chunk in the bundle, got %v", prefix, files)
		}
		if b, _ := fs.ReadFile(FS(), files[0]); !strings.Contains(string(b), marker) {
			t.Errorf("%s does not contain %q: the check above would pass vacuously", files[0], marker)
		}
	}
}

// The service worker precaches what the shell loads, not the on-demand OpenUI
// chunks: installing the PWA must not download them. They stay in the worker's
// allowlist so they are cached on first use and work offline afterwards.
func TestServiceWorkerPrecachesShellNotOptionalChunks(t *testing.T) {
	b, err := fs.ReadFile(FS(), "sw.js")
	if err != nil {
		t.Fatal(err)
	}
	list := func(name string) []string {
		m := regexp.MustCompile(`const ` + name + ` = (\[[^\]]*\]);`).FindSubmatch(b)
		if m == nil {
			t.Fatalf("sw.js has no %s list", name)
		}
		var out []string
		if err := json.Unmarshal(m[1], &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	precache, optional := strings.Join(list("PUBLIC"), " "), strings.Join(list("OPTIONAL"), " ")
	html, _ := fs.ReadFile(FS(), "index.html")
	for _, m := range regexp.MustCompile(`(?:src|href)="(/assets/[^"]+\.(?:js|css))"`).FindAllStringSubmatch(string(html), -1) {
		if !strings.Contains(precache, m[1]) {
			t.Errorf("shell asset %s is not precached", m[1])
		}
	}
	for _, want := range []string{"/offline.html", "/assets/rolldown-runtime-", "/assets/offline-"} {
		if !strings.Contains(precache, want) {
			t.Errorf("precache lacks %s", want)
		}
	}
	for _, prefix := range []string{"Block-", "Chart-"} {
		if strings.Contains(precache, "/assets/"+prefix) {
			t.Errorf("precache downloads the optional %s chunk at install", prefix)
		}
		if !strings.Contains(optional, "/assets/"+prefix) {
			t.Errorf("%s chunk is missing from the on-demand list, so it would not work offline", prefix)
		}
	}
}
