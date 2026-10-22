package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-token-test-token-test-token-0000"

type fakeRunner struct{ calls [][]string }

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	return "", "", nil
}

func TestValidateSlug(t *testing.T) {
	good := []string{"u_abcdefgh", "u_0123456789", "u_" + strings.Repeat("a", 32)}
	for _, s := range good {
		if err := validateSlug(s); err != nil {
			t.Errorf("%q should be accepted: %v", s, err)
		}
	}
	bad := []string{
		"", "u_", "u_short", "u_" + strings.Repeat("a", 33), "U_abcdefgh", "x_abcdefgh",
		"u_abcdefg-", "u_abcdefg_", "../etc", "u_abcdefgh/..", "u_abcdefgh\n", " u_abcdefgh",
		"u_abcdefgh;ls", "u_abcdefgh\x00", "u_ABCDEFGH", "u_abcdéfgh",
	}
	for _, s := range bad {
		if err := validateSlug(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestDefaultRefusedWithOwnError(t *testing.T) {
	if err := validateSlug("default"); err != errDefaultProfile {
		t.Fatalf("want errDefaultProfile, got %v", err)
	}
	if errDefaultProfile == errBadSlug {
		t.Fatal("default must have its own error")
	}
}

func do(t *testing.T, s *server, path, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func TestRefusedRequestsNeverReachDocker(t *testing.T) {
	bodies := map[string]string{
		"bad slug":      `{"slug":"../etc"}`,
		"default":       `{"slug":"default"}`,
		"extra profile": `{"slug":"u_abcdefgh","profile":"default"}`,
		"extra key":     `{"slug":"u_abcdefgh","key":"x"}`,
		"not json":      `nope`,
		"empty":         ``,
	}
	for _, path := range []string{"/provision", "/deprovision", "/rotate"} {
		for name, body := range bodies {
			f := &fakeRunner{}
			rec := do(t, newServer(f, testToken), path, "Bearer "+testToken, body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s: want 400, got %d", path, name, rec.Code)
			}
			if len(f.calls) != 0 {
				t.Errorf("%s %s: docker reached: %v", path, name, f.calls)
			}
		}
	}
}

func TestAuthRequired(t *testing.T) {
	for _, auth := range []string{"", "Bearer wrong", "Basic " + testToken, testToken, "Bearer "} {
		f := &fakeRunner{}
		rec := do(t, newServer(f, testToken), "/provision", auth, `{"slug":"u_abcdefgh"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: want 401, got %d", auth, rec.Code)
		}
		if len(f.calls) != 0 {
			t.Errorf("auth %q: docker reached", auth)
		}
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	newServer(&fakeRunner{}, testToken).routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// create failing must return an error and never attempt to write a key.
type failCreate struct{ calls [][]string }

func (f *failCreate) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	if len(args) > 2 && args[2] == "list" {
		return listFixture, "", nil
	}
	if len(args) > 2 && args[2] == "create" {
		return "", "boom", context.DeadlineExceeded
	}
	return "", "", nil
}

func TestProvisionFailsClosedWhenCreateFails(t *testing.T) {
	f := &failCreate{}
	rec := do(t, newServer(f, testToken), "/provision", "Bearer "+testToken, `{"slug":"u_abcdefgh"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"key"`) {
		t.Fatal("response carries a key")
	}
	created := false
	for _, c := range f.calls {
		created = created || (len(c) > 2 && c[2] == "create")
		if c[0] == "python3" {
			t.Fatal("a key write was attempted after create failed")
		}
	}
	if !created {
		t.Fatal("create was never attempted; test proves nothing")
	}
}

func TestProfileDirIsStrictChildOfRoot(t *testing.T) {
	for _, s := range []string{"u_abcdefgh", "u_" + strings.Repeat("z", 32)} {
		dir, err := profileDir(s)
		if err != nil || dir != profilesDir+"/"+s {
			t.Errorf("%q: got %q, %v", s, dir, err)
		}
	}
	for _, s := range []string{"default", "..", ".", "", "u_../../x", "u_abcdefgh/../../etc", "/opt/data"} {
		if dir, err := profileDir(s); err == nil {
			t.Errorf("%q should be refused, got %q", s, dir)
		}
	}
}

type residueRunner struct{ calls [][]string }

func (f *residueRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	if args[0] == "rmdir" {
		return "", "not empty", context.DeadlineExceeded
	}
	return "", "", nil
}

func TestDeprovisionRemovesDirAndReportsNonEmpty(t *testing.T) {
	f := &residueRunner{}
	rec := do(t, newServer(f, testToken), "/deprovision", "Bearer "+testToken, `{"slug":"u_abcdefgh"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), profilesDir+"/u_abcdefgh") {
		t.Fatalf("want 502 naming the path, got %d %s", rec.Code, rec.Body)
	}
	last := f.calls[len(f.calls)-1]
	if last[0] != "rmdir" || last[2] != profilesDir+"/u_abcdefgh" {
		t.Fatalf("unexpected final call %v", last)
	}
	for _, c := range f.calls {
		if c[0] == "rm" {
			t.Fatal("recursive removal must never be used")
		}
	}
}

const listFixture = ` Profile          Model                        Gateway      Alias        Distribution
 ───────────────    ───────────────────────────    ───────────    ───────────    ────────────────────
 ◆default         model-x          running      —            —
  u_existing1    model-x          stopped      —            —
`

type exitErr int

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return int(e) }

// scripted answers `profile list` with list and fails python3 with writeErr.
type scripted struct {
	list     string
	writeErr error
	onCreate func()
	calls    [][]string
}

func (f *scripted) Run(ctx context.Context, _ string, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	if ctx.Err() != nil { // like exec.CommandContext: a cancelled context kills the call
		return "", "", ctx.Err()
	}
	switch {
	case len(args) > 2 && args[2] == "list":
		return f.list, "", nil
	case len(args) > 2 && args[2] == "create" && f.onCreate != nil:
		f.onCreate()
	case args[0] == "python3":
		return "", "", f.writeErr
	}
	return "", "", nil
}

func (f *scripted) called(name string) bool {
	for _, c := range f.calls {
		if c[0] == name || (len(c) > 2 && c[2] == name) {
			return true
		}
	}
	return false
}

func TestExistsParsesRealListOutput(t *testing.T) {
	h := hermes{&scripted{list: listFixture}}
	for slug, want := range map[string]bool{"u_existing1": true, "u_missing01": false, "default": true} {
		got, err := h.exists(context.Background(), slug)
		if err != nil || got != want {
			t.Errorf("%s: got %v, %v", slug, got, err)
		}
	}
	// unrecognised output must be an error, never "absent"
	if _, err := (hermes{&scripted{list: "something else entirely"}}).exists(context.Background(), "u_x"); err == nil {
		t.Error("unrecognised list output must fail closed")
	}
}

func TestProvisionHappyPathReturnsKey(t *testing.T) {
	f := &scripted{list: listFixture}
	rec := do(t, newServer(f, testToken), "/provision", "Bearer "+testToken, `{"slug":"u_newuser01"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["profile"] != "u_newuser01" || len(out["key"]) != 64 || out["created_at"] == "" {
		t.Fatalf("bad body %v", out)
	}
	if !f.called("create") {
		t.Fatal("profile was not created")
	}
}

func TestProvisionExistingKeyIs409(t *testing.T) {
	f := &scripted{list: listFixture, writeErr: exitErr(3)}
	rec := do(t, newServer(f, testToken), "/provision", "Bearer "+testToken, `{"slug":"u_existing1"}`)
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), `"key"`) {
		t.Fatalf("want 409 without key, got %d %s", rec.Code, rec.Body)
	}
	if f.called("create") {
		t.Fatal("existing profile must not be re-created")
	}
}

// The server must finish a request whose client has gone away: cancelling the
// request context after create must not stop the key write.
func TestClientCancelDoesNotAbandonRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &scripted{list: listFixture, onCreate: cancel}
	req := httptest.NewRequest(http.MethodPost, "/provision", strings.NewReader(`{"slug":"u_newuser01"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	newServer(f, testToken).routes().ServeHTTP(rec, req)
	if !f.called("python3") || rec.Code != http.StatusOK {
		t.Fatalf("request abandoned after cancel: wrote key=%v code=%d", f.called("python3"), rec.Code)
	}
}

func TestTimeoutBudgetsAreOrdered(t *testing.T) {
	if maxCalls*callTimeout >= opBudget {
		t.Fatalf("opBudget %v must exceed %d calls of %v", opBudget, maxCalls, callTimeout)
	}
	if writeSlack <= 0 {
		t.Fatal("write deadline must exceed opBudget")
	}
}

type sweepRunner struct{ calls [][]string }

func (f *sweepRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	switch {
	case args[0] == "ls":
		return ".deleted\nprobe2\nu_existing1\nu_goneuser1\n../x\n", "", nil
	case len(args) > 2 && args[2] == "list":
		return listFixture, "", nil
	}
	return "", "", nil
}

func TestSweepRemovesOnlyDeadValidatedSlugs(t *testing.T) {
	f := &sweepRunner{}
	removed, err := (hermes{f}).sweep(context.Background())
	if err != nil || len(removed) != 1 || removed[0] != "u_goneuser1" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	for _, c := range f.calls {
		if c[0] == "rmdir" && c[2] != profilesDir+"/u_goneuser1" {
			t.Fatalf("rmdir on %v", c)
		}
	}
}

func TestProvisionDisablesToolsetsBeforeIssuingKey(t *testing.T) {
	f := &scripted{list: listFixture}
	rec := do(t, newServer(f, testToken), "/provision", "Bearer "+testToken, `{"slug":"u_newuser01"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	cfg, py := -1, -1
	for i, c := range f.calls {
		if len(c) == 7 && c[0] == "hermes" && c[1] == "-p" && c[2] == "u_newuser01" && c[3] == "config" && c[4] == "set" && c[5] == "agent.disabled_toolsets" {
			cfg = i
			var got []string
			if err := json.Unmarshal([]byte(c[6]), &got); err != nil {
				t.Fatalf("value is not a JSON array: %q", c[6])
			}
			have := map[string]bool{}
			for _, n := range got {
				have[n] = true
			}
			for _, n := range []string{"terminal", "file", "code_execution", "delegation", "cronjob", "session_search", "connections", "browser"} {
				if !have[n] {
					t.Errorf("%s is not disabled", n)
				}
			}
			for _, n := range []string{"web", "vision", "image_gen", "skills", "memory", "todo", "clarify"} {
				if have[n] {
					t.Errorf("allowed toolset %s is disabled", n)
				}
			}
		}
		if c[0] == "python3" {
			py = i
		}
	}
	if cfg < 0 {
		t.Fatal("toolsets were not disabled in the profile's own config")
	}
	if py < cfg {
		t.Fatal("key was written before the toolset policy")
	}
}

func TestProvisionFailsClosedWhenToolsetPolicyFails(t *testing.T) {
	f := &toolsetFail{scripted{list: listFixture}}
	rec := do(t, newServer(f, testToken), "/provision", "Bearer "+testToken, `{"slug":"u_newuser01"}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), `"key"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if f.called("python3") {
		t.Fatal("a key was written for a profile without the toolset policy")
	}
}

type toolsetFail struct{ scripted }

func (f *toolsetFail) Run(ctx context.Context, in string, args ...string) (string, string, error) {
	if len(args) > 4 && args[3] == "config" {
		f.calls = append(f.calls, args)
		return "", "boom", exitErr(1)
	}
	return f.scripted.Run(ctx, in, args...)
}
