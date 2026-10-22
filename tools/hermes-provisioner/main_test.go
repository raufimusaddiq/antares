package main

import (
	"context"
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
	for _, c := range f.calls {
		if c[0] == "python3" {
			t.Fatal("a key write was attempted after create failed")
		}
	}
}
