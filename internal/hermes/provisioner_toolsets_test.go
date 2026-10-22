package hermes

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// The provisioner is a separate module and keeps its own list. Every floor
// toolset must be in it, and no default-allowed toolset may be.
func TestProvisionerDisablesTheWholeFloor(t *testing.T) {
	lit, ok := valueOf(parseProvisioner(t, "toolsets.go"), "disabledToolsets").(*ast.CompositeLit)
	if !ok {
		t.Fatal("var disabledToolsets (a composite literal) not found in the provisioner")
	}
	have := map[string]bool{}
	for _, e := range lit.Elts {
		bl, ok := e.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			t.Fatalf("disabledToolsets element %T is not a string literal", e)
		}
		n, err := strconv.Unquote(bl.Value)
		if err != nil {
			t.Fatal(err)
		}
		have[n] = true
	}
	if len(have) == 0 {
		t.Fatal("disabledToolsets is empty")
	}
	for _, d := range DeniedToolsets {
		if !have[d] {
			t.Errorf("provisioner does not disable %s", d)
		}
	}
	for _, a := range DefaultAllowedToolsets {
		if have[a] {
			t.Errorf("provisioner disables allowed toolset %s", a)
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
