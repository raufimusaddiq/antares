package hermes

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
	"time"
)

const provisionerDir = "../../tools/hermes-provisioner"

func parseProvisioner(t *testing.T, file string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), provisionerDir+"/"+file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// valueOf returns the expression bound to a package-level var or const name.
func valueOf(f *ast.File, name string) ast.Expr {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if id.Name == name && i < len(vs.Values) {
					return vs.Values[i]
				}
			}
		}
	}
	return nil
}

// stringsVar returns the elements of a package-level []string literal.
func stringsVar(t *testing.T, name string) []string {
	t.Helper()
	lit, ok := valueOf(parseProvisioner(t, "toolsets.go"), name).(*ast.CompositeLit)
	if !ok {
		t.Fatalf("var %s (a composite literal) not found in the provisioner", name)
	}
	var out []string
	for _, e := range lit.Elts {
		bl, ok := e.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			t.Fatalf("%s element %T is not a string literal", name, e)
		}
		n, err := strconv.Unquote(bl.Value)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		t.Fatalf("%s is empty", name)
	}
	return out
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// The provisioner is a separate module and mirrors three lists. They must equal
// Antares' own, and its known list must equal what the deployed Hermes reports.
func TestProvisionerToolsetListsMatchAntares(t *testing.T) {
	if got := stringsVar(t, "floorToolsets"); !sameSet(got, DeniedToolsets) {
		t.Errorf("floorToolsets %v != DeniedToolsets %v", got, DeniedToolsets)
	}
	if got := stringsVar(t, "defaultAllowed"); !sameSet(got, DefaultAllowedToolsets) {
		t.Errorf("defaultAllowed %v != DefaultAllowedToolsets %v", got, DefaultAllowedToolsets)
	}
	b, err := os.ReadFile("../../docs/hermes/fixtures/toolsets.body.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	var reported []string
	for _, d := range fx.Data {
		reported = append(reported, d.Name)
	}
	if got := stringsVar(t, "knownToolsets"); !sameSet(got, reported) {
		t.Errorf("knownToolsets %v != fixture %v", got, reported)
	}
}

// What the provisioner leaves enabled for an allow-list, as /v1/toolsets would
// report it: knownToolsets minus disabledFor(allowed). The derivation is stated
// here independently of the provisioner's code.
func fakeFor(t *testing.T, allowed []string) *fakeHermes {
	f := newFake(t)
	f.toolsets = nil
	for _, n := range stringsVar(t, "knownToolsets") {
		on := false
		for _, a := range allowed {
			on = on || a == n
		}
		f.toolsets = append(f.toolsets, tset{n, on})
	}
	return f
}

// Narrowing the allow-list (ANTARES_TENANT_ALLOWED_TOOLSETS and the
// provisioner's matching variable) must still produce a profile that verifies.
func TestNarrowedAllowListStillVerifies(t *testing.T) {
	for _, allowed := range [][]string{DefaultAllowedToolsets, {"web", "memory"}, {"memory"}} {
		f := fakeFor(t, allowed)
		v, err := NewVerifier(f.srv.URL, allowed, nil)
		if err != nil {
			t.Fatal(err)
		}
		if rep := v.Verify(context.Background(), target()); !rep.OK() {
			t.Errorf("allowed %v: %+v", allowed, rep)
		}
		// The default provisioner policy under a narrower Antares allow-list is
		// exactly the drift the verifier must catch.
		if len(allowed) < len(DefaultAllowedToolsets) {
			g := fakeFor(t, DefaultAllowedToolsets)
			vv, _ := NewVerifier(g.srv.URL, allowed, nil)
			if rep := vv.Verify(context.Background(), target()); rep.OK() {
				t.Errorf("allowed %v: a profile with the default set passed", allowed)
			}
		}
	}
}

// seconds evaluates a constant of the form `N * time.Second`.
func seconds(t *testing.T, f *ast.File, name string) time.Duration {
	t.Helper()
	be, ok := valueOf(f, name).(*ast.BinaryExpr)
	if !ok || be.Op != token.MUL {
		t.Fatalf("%s is not an `N * time.Second` constant", name)
	}
	n, ok := be.X.(*ast.BasicLit)
	sel, ok2 := be.Y.(*ast.SelectorExpr)
	if !ok || !ok2 || sel.Sel.Name != "Second" {
		t.Fatalf("%s is not an `N * time.Second` constant", name)
	}
	v, err := strconv.Atoi(n.Value)
	if err != nil {
		t.Fatal(err)
	}
	return time.Duration(v) * time.Second
}

// The caller must outlast the helper: if HelperTimeout fired first, Antares
// would report the helper unreachable while it went on to create the profile
// and write a key, and the retry would meet a 409.
func TestHelperTimeoutOutlastsProvisionerBudget(t *testing.T) {
	f := parseProvisioner(t, "hermes.go")
	budget := seconds(t, f, "opBudget") + seconds(t, f, "writeSlack")
	if HelperTimeout <= budget {
		t.Fatalf("HelperTimeout %v must exceed the provisioner's opBudget+writeSlack %v", HelperTimeout, budget)
	}
}
