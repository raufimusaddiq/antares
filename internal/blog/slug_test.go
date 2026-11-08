package blog

import "testing"

func TestSlugRules(t *testing.T) {
	for s, want := range map[string]bool{
		"hello-world": true, "abc": true, "a1b": true, "x-y-z": true,
		"ab": false, "": false, "-abc": false, "abc-": false, "a--b": false, "Hello": false, "a b": false, "a_b": false,
		"admin": false, "api": false, "new": false, "é-è-ç": false,
	} {
		if got := ValidSlug(s); got != want {
			t.Errorf("ValidSlug(%q) = %v, want %v", s, got, want)
		}
	}
	long := ""
	for i := 0; i < 81; i++ {
		long += "a"
	}
	if ValidSlug(long) || !ValidSlug(long[:80]) {
		t.Error("the length limit is 80")
	}
}
