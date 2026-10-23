package smoke

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const fakeCookie = "fabricated-session-cookie-value-0123456789"

func TestRedactorRemovesKnownSecretsAndCredentialShapes(t *testing.T) {
	r := &Redactor{}
	r.Add(fakeCookie)
	r.Add("short") // too short to register; must not wreck the output
	jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl"
	in := strings.Join([]string{
		"cookie " + fakeCookie + " seen",
		"Set-Cookie: __Host-antares_session=abcdef0123456789; Path=/",
		"Authorization: Bearer abcdefghijklmnop0123",
		"id_token " + jwt,
		`{"api_key":"sk-notrealnotreal","other":"kept"}`,
		"password=hunter2hunter2",
		"short stays short",
	}, "\n")
	out := r.Clean(in)
	for _, leak := range []string{fakeCookie, "abcdef0123456789", "abcdefghijklmnop0123", jwt, "sk-notrealnotreal", "hunter2hunter2"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"__Host-antares_session=", "Bearer ", "kept", "short stays short"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("over-redacted, lost %q in:\n%s", keep, out)
		}
	}
	var nilRed *Redactor
	if nilRed.Clean("x") != "x" {
		t.Fatal("nil redactor must pass through")
	}
}

func TestRunnerRecordsSkipsAndBlocksDependents(t *testing.T) {
	red := &Redactor{}
	red.Add(fakeCookie)
	var live strings.Builder
	r := &Runner{Red: red, Log: &live}
	r.Run("one", "passes", nil, func() (string, error) { return "fine", nil })
	r.Run("two", "fails", []string{"one"}, func() (string, error) { return "", errors.New("boom with " + fakeCookie) })
	called := false
	r.Run("three", "depends on the failure", []string{"two"}, func() (string, error) { called = true; return "no", nil })
	r.Run("four", "no precondition", nil, func() (string, error) { return "", Skipped("nothing supplied") })
	r.Run("five", "depends on a skip", []string{"four"}, func() (string, error) { called = true; return "no", nil })
	r.Run("six", "crashes", nil, func() (string, error) { var m map[string]int; m["x"] = 1; return "", nil })
	if called {
		t.Fatal("a step ran although its prerequisite did not pass")
	}
	rep := r.Report("https://x.test", "id", time.Now())
	want := map[string]Status{"one": Pass, "two": Fail, "three": Skip, "four": Skip, "five": Skip, "six": Fail}
	for id, st := range want {
		if got := rep.step(id).Status; got != st {
			t.Errorf("%s = %s, want %s", id, got, st)
		}
	}
	if rep.step("three").BlockedBy != "two" {
		t.Errorf("blockedBy = %q", rep.step("three").BlockedBy)
	}
	if rep.Verdict != VerdictFail || rep.FirstFailure != "two" || rep.ExitCode() != 1 {
		t.Fatalf("%+v", rep)
	}
	raw, _ := rep.JSON()
	if strings.Contains(string(raw), fakeCookie) || strings.Contains(live.String(), fakeCookie) {
		t.Fatal("a credential reached the report or the live log")
	}
}

func TestVerdictNeverCountsASkipAsAPass(t *testing.T) {
	r := &Runner{}
	r.Run("a", "ok", nil, func() (string, error) { return "", nil })
	r.Run("b", "skipped", nil, func() (string, error) { return "", Skipped("no credentials") })
	rep := r.Report("t", "i", time.Now())
	if rep.Verdict != VerdictIncomplete || rep.ExitCode() != 2 || rep.Pass != 1 || rep.Skip != 1 {
		t.Fatalf("%+v", rep)
	}
	empty := (&Runner{}).Report("t", "i", time.Now())
	if empty.Verdict == VerdictPass {
		t.Fatal("a run in which nothing ran must not pass")
	}
}
