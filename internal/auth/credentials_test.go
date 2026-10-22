package auth

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

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
	if seen < 3 { // auth.go, oidc.go, routes.go
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

// The status screens are React now; the server's part is the state it reports.
// Every state a screen exists for must come out of /api/me exactly as the app expects.
func TestMeReportsTheStateEachScreenIsChosenFrom(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		sub, approval, tenant, want string
		bind                        bool
	}{
		{"s-pending", "PENDING", "UNPROVISIONED", "PENDING", false},
		{"s-rejected", "REJECTED", "UNPROVISIONED", "REJECTED", false},
		{"s-suspended", "SUSPENDED", "READY", "SUSPENDED", true},
		{"s-setup", "APPROVED", "CONFIGURING", "SETTING_UP", false},
		{"s-error", "APPROVED", "FAILED", "ERROR", false},
		{"s-active", "APPROVED", "READY", "ACTIVE", true},
	} {
		b := h.userIn(c.sub, c.approval, c.tenant, "USER", c.bind)
		var v AccountView
		w := b.get("/api/me")
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || v.State != c.want || v.Email != c.sub+"@example.test" {
			t.Errorf("%s: %d %s", c.sub, w.Code, w.Body.String())
		}
	}
}
