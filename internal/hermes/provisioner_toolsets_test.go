package hermes

import (
	"os"
	"strings"
	"testing"
)

// The provisioner is a separate module and keeps its own list. Every floor
// toolset must be in it, and no default-allowed toolset may be.
func TestProvisionerDisablesTheWholeFloor(t *testing.T) {
	b, err := os.ReadFile("../../tools/hermes-provisioner/toolsets.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "var disabledToolsets")
	end := strings.Index(src[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("cannot find disabledToolsets in the provisioner")
	}
	list := src[start : start+end]
	for _, d := range DeniedToolsets {
		if !strings.Contains(list, `"`+d+`"`) {
			t.Errorf("provisioner does not disable %s", d)
		}
	}
	for _, a := range DefaultAllowedToolsets {
		if strings.Contains(list, `"`+a+`"`) {
			t.Errorf("provisioner disables allowed toolset %s", a)
		}
	}
}
