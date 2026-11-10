package blog

import (
	"encoding/json"
	"os"
	"testing"
)

// The same fixture is read by web/src/blog/anchors.test.ts: the two implementations of the rule must agree.
type anchorFixture struct {
	Documents []struct {
		Name    string          `json:"name"`
		Doc     json.RawMessage `json:"doc"`
		Anchors []string        `json:"anchors"`
	} `json:"documents"`
	Relocate []struct {
		Name    string   `json:"name"`
		Anchors []string `json:"anchors"`
		Anchor  string   `json:"anchor"`
		Hint    int      `json:"hint"`
		Want    int      `json:"want"`
	} `json:"relocate"`
}

func loadAnchorFixture(t *testing.T) anchorFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/anchors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f anchorFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTheAnchorOfEachPartIsTheTextOfItsFirstTextBlock(t *testing.T) {
	for _, d := range loadAnchorFixture(t).Documents {
		got, err := partAnchors(d.Doc)
		if err != nil || len(got) != len(d.Anchors) {
			t.Fatalf("%s: %v %v", d.Name, err, got)
		}
		for i := range got {
			if got[i] != d.Anchors[i] {
				t.Errorf("%s: part %d anchor %q, want %q", d.Name, i, got[i], d.Anchors[i])
			}
		}
	}
}

func TestAPartIsFoundAgainByItsAnchorNearestTheOldPlace(t *testing.T) {
	for _, c := range loadAnchorFixture(t).Relocate {
		if got := relocate(c.Anchors, c.Anchor, c.Hint); got != c.Want {
			t.Errorf("%s: %d, want %d", c.Name, got, c.Want)
		}
	}
}
