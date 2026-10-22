package auth

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

func TestEachStateLandsOnItsOwnScreenWithNoChatControls(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name             string
		b                *browser
		path, mustSay    string
		approval, tenant string
		bind             bool
	}{
		{"pending", nil, "/pending", "waiting for administrator approval", "PENDING", "UNPROVISIONED", false},
		{"rejected", nil, "/rejected", "was not approved", "REJECTED", "UNPROVISIONED", false},
		{"suspended", nil, "/suspended", "is disabled", "SUSPENDED", "READY", true},
		{"setup", nil, "/setup", "being prepared", "APPROVED", "CONFIGURING", false},
		{"error", nil, "/error", "workspace is not working", "APPROVED", "FAILED", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := h.userIn("s-"+c.name, c.approval, c.tenant, "USER", c.bind)
			if w := b.get("/"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != c.path {
				t.Fatalf("/ -> %d %s, want %s", w.Code, w.Header().Get("Location"), c.path)
			}
			w := b.get(c.path)
			body := w.Body.String()
			if w.Code != 200 || !has(strings.ToLower(body), c.mustSay) {
				t.Fatalf("%s: %d %q", c.path, w.Code, body)
			}
			if !has(body, "s-"+c.name+"@example.test") || !has(body, `action="/api/auth/logout"`) {
				t.Error("screen must show who is signed in and offer sign-out")
			}
			for _, banned := range []string{"<textarea", "<input", "<script", "composer", "Send message", "<iframe"} {
				if has(strings.ToLower(body), strings.ToLower(banned)) {
					t.Errorf("%s contains %q: a non-eligible user must not be offered chat", c.path, banned)
				}
			}
			// Each screen names a next step.
			if !regexp.MustCompile(`(?i)contact|refresh|ask|sign out|try`).MatchString(body) {
				t.Error("screen gives no next step")
			}
			csp := w.Header().Get("Content-Security-Policy")
			if !has(csp, "default-src 'none'") || has(csp, "unsafe-inline") || has(csp, "script-src") {
				t.Errorf("csp = %q", csp)
			}
			// Other states' screens bounce to the right one.
			for _, o := range cases {
				if o.path != c.path {
					if w := b.get(o.path); w.Code != http.StatusSeeOther || w.Header().Get("Location") != c.path {
						t.Errorf("%s as %s -> %d %s", o.path, c.name, w.Code, w.Header().Get("Location"))
					}
				}
			}
		})
	}
}

func TestActiveUserIsLetThroughToTheShellAndAwayFromScreens(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-active", "APPROVED", "READY", "USER", true)
	if w := b.get("/"); w.Code == http.StatusSeeOther {
		t.Fatalf("active user redirected from /: %s", w.Header().Get("Location"))
	}
	if w := b.get("/pending"); w.Header().Get("Location") != "/" {
		t.Fatalf("active user on /pending -> %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestAnonymousSeesLoginOnly(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	if w := b.get("/"); w.Header().Get("Location") != "/login" {
		t.Fatalf("/ -> %s", w.Header().Get("Location"))
	}
	for _, p := range []string{"/pending", "/rejected", "/suspended", "/setup"} {
		if w := b.get(p); w.Header().Get("Location") != "/login" {
			t.Errorf("%s anonymous -> %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
	w := b.get("/login")
	if w.Code != 200 || !has(w.Body.String(), `href="/api/auth/google/start"`) || has(w.Body.String(), "password\"") {
		t.Fatalf("login page: %d", w.Code)
	}
	// Failure screen after a rejected callback says nothing happened.
	e := b.get("/error?code=login_failed")
	if !has(e.Body.String(), "not been signed in") {
		t.Errorf("error screen: %q", e.Body.String())
	}
	if w := b.get("/error?code=%3Cscript%3E"); has(w.Body.String(), "<script") {
		t.Error("error code reflected into the page")
	}
}

func TestScreensEscapeAccountData(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	h.g.claims = func(n string) map[string]any {
		c := h.g.goodClaims("s-x", `x"><img src=x onerror=alert(1)>@example.test`, n)
		c["name"] = `<script>alert(1)</script>`
		return c
	}
	state, code := b.begin()
	b.callback(state, code)
	body := b.get("/pending").Body.String()
	if has(body, "<script>alert") || has(body, "<img src=x") {
		t.Fatalf("unescaped account data: %s", body)
	}
}

func TestLogoutFormReturnsToLogin(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-p", "PENDING", "UNPROVISIONED", "USER", false)
	r := b.do("POST", "/api/auth/logout", map[string]string{"Origin": testOrigin, "Content-Type": "application/x-www-form-urlencoded"})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/login" {
		t.Fatalf("form logout = %d %s", r.Code, r.Header().Get("Location"))
	}
}

// ---- nothing in the browser's reach can carry a Hermes credential -------------

// The browser receives account data through exactly two closed structs and the
// server-rendered screens. Assert the structure, not a template.
func TestBrowserReadModelsCannotCarryCredentials(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)key|secret|token|credential|cipher|fingerprint|profile|binding|memory|endpoint|password`)
	for _, typ := range []reflect.Type{reflect.TypeOf(AccountView{}), reflect.TypeOf(StatusView{})} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if k := f.Type.Kind(); k != reflect.String && k != reflect.Bool {
				t.Errorf("%s.%s is %s: read models hold plain strings and bools only", typ.Name(), f.Name, k)
			}
			if forbidden.MatchString(f.Name) || forbidden.MatchString(f.Tag.Get("json")) {
				t.Errorf("%s.%s looks like it could carry a credential", typ.Name(), f.Name)
			}
		}
	}
	// And the row the identity layer loads has no credential column to leak from.
	ut := reflect.TypeOf(store.User{})
	for i := 0; i < ut.NumField(); i++ {
		if forbidden.MatchString(ut.Field(i).Name) && ut.Field(i).Name != "TenantBound" && ut.Field(i).Name != "TenantStatus" {
			t.Errorf("store.User.%s", ut.Field(i).Name)
		}
	}
}

func TestIdentityPackageNeverImportsCredentialMachineryOrReadsCredentialColumns(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		seen++
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasSuffix(path, "/internal/secrets") || strings.HasSuffix(path, "/internal/config") {
				t.Errorf("%s imports %s: the identity layer must not hold the master key or Hermes secrets", f, path)
			}
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s := strings.ToLower(lit.Value)
				for _, col := range []string{"credential_", "memory_scope_key", "tenant_bindings"} {
					if strings.Contains(s, col) {
						t.Errorf("%s mentions %s", f, col)
					}
				}
			}
			return true
		})
	}
	if seen < 4 {
		t.Fatal("did not scan the package")
	}
	// The one place a binding is consulted is the store, and only for existence.
	src, _ := os.ReadFile("../store/accounts.go")
	for _, col := range []string{"credential_ciphertext", "credential_fingerprint", "profile_name", "memory_scope_key"} {
		if strings.Contains(string(src), col) {
			t.Errorf("store/accounts.go reads %s", col)
		}
	}
}

// Seed a ready binding with recognisable values, then fetch every GET surface
// as the bound user and as an admin: none of it may appear.
func TestNoResponseCarriesBindingMaterial(t *testing.T) {
	h := newHarness(t)
	user := h.userIn("s-bound", "APPROVED", "READY", "USER", true)
	admin := h.userIn("s-admin", "APPROVED", "READY", "ADMIN", true)
	paths := []string{"/", "/login", "/pending", "/setup", "/error", "/api/me", "/api/account/status",
		"/api/auth/google/start", "/api/admin/users", "/api/chats", "/api/capabilities"}
	for _, b := range []*browser{user, admin} {
		for _, p := range paths {
			w := b.get(p)
			all := w.Body.String()
			for k, v := range w.Header() {
				all += k + ": " + strings.Join(v, ",") + "\n"
			}
			for _, secret := range []string{sentinelCiphertext, "profile-s-bound", "profile-s-admin", "fp-s-bound", "mem-s-bound", testSecret, "fabricated-access-token"} {
				if strings.Contains(all, secret) {
					t.Errorf("%s leaks %q", p, secret)
				}
			}
		}
	}
	var v map[string]any
	json.Unmarshal(user.get("/api/me").Body.Bytes(), &v)
	if len(v) != 11 {
		t.Errorf("/api/me has %d fields, expected the 11 of AccountView: %v", len(v), v)
	}
}
