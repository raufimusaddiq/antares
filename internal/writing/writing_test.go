package writing

import (
	"strings"
	"testing"
)

func TestTextInvariants(t *testing.T) {
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	s := Text()
	t.Logf("writing-rules %s: %d characters", Version, len([]rune(s)))
	for _, want := range []string{"writing-rules v1", "MIT", "Miqdad Badjuber (antislop)", "em dashes", "Never invent numbers", "answer a question", "tone", "say so plainly"} {
		if !strings.Contains(s, want) {
			t.Errorf("rules text lacks %q", want)
		}
	}
	if n := len([]rune(s)); n > MaxChars {
		t.Errorf("%d characters, at most %d", n, MaxChars)
	}
}

func TestCheckRefusesBadText(t *testing.T) {
	orig := text
	defer func() { text = orig }()
	for name, bad := range map[string]string{
		"empty":      "",
		"no version": "be nice. MIT Miqdad Badjuber (antislop)",
		"no credit":  "writing-rules v1\nbe nice",
		"too long":   "writing-rules v1 MIT Miqdad Badjuber (antislop)\n" + strings.Repeat("x", MaxChars),
	} {
		text = bad
		if Check() == nil {
			t.Errorf("%s: Check passed", name)
		}
		if Text() != "" {
			t.Errorf("%s: Text returned the text", name)
		}
	}
}

// SetForTest swaps the text and returns a restore function (the seam for tests of the callers).
func TestSeam(t *testing.T) {
	restore := SetTextForTest("")
	if Text() != "" {
		t.Fatal("seam did not hide the text")
	}
	restore()
	if Text() == "" {
		t.Fatal("restore did not bring the text back")
	}
}
