package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func runCLI(t *testing.T, args []string, e map[string]string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, env(e), &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsageErrorsExit3(t *testing.T) {
	cookie := map[string]string{"ANTARES_SMOKE_COOKIE_ADMIN": "fabricated-cookie-value-0123456789"}
	for name, c := range map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"no url":       {nil, cookie, "--url"},
		"path in url":  {[]string{"--url", "https://a.test/app"}, cookie, "bare http(s) origin"},
		"plain http":   {[]string{"--url", "http://antares.example.com"}, cookie, "refusing http://"},
		"no sessions":  {[]string{"--url", "https://a.test"}, nil, "no sessions supplied"},
		"unknown flag": {[]string{"--nope"}, cookie, ""},
	} {
		code, _, errs := runCLI(t, c.args, c.env)
		if code != 3 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d, stderr %q", name, code, errs)
		}
	}
}

// A target that is up but not ready fails at the first step, writes the JSON result,
// marks everything after it skipped and never prints the cookie.
func TestUnreachableProductFailsWithJSONAndNoCredentialInOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	const cookie = "fabricated-cookie-value-0123456789"
	jsonPath := filepath.Join(t.TempDir(), "result.json")
	code, out, errs := runCLI(t, []string{"--url", srv.URL, "--json", jsonPath},
		map[string]string{"ANTARES_SMOKE_COOKIE_ADMIN": "__Host-antares_session=" + cookie, "ANTARES_SMOKE_COOKIE_A": cookie})
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	if strings.Contains(out+errs, cookie) {
		t.Fatal("cookie printed")
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil || strings.Contains(string(raw), cookie) {
		t.Fatalf("json: %v / contains cookie", err)
	}
	var rep struct {
		Verdict      string
		FirstFailure string
		Skip         int
		Steps        []struct{ ID, Status string }
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != "FAIL" || rep.FirstFailure != "reach.health" || rep.Skip == 0 {
		t.Fatalf("%+v", rep)
	}
}
