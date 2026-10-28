package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type mediaRunner struct {
	scripted
	body string
	err  error
}

func (f *mediaRunner) Run(ctx context.Context, in string, args ...string) (string, string, error) {
	if args[0] == "python3" && args[2] == mediaScript {
		f.calls = append(f.calls, args)
		return f.body, "", f.err
	}
	return f.scripted.Run(ctx, in, args...)
}

func TestMediaServesOnlyTheProfilesOwnCache(t *testing.T) {
	f := &mediaRunner{body: "\x89PNG-bytes"}
	s := newServer(f, testToken)
	ok := `{"slug":"u_abcdefgh","path":"/opt/data/profiles/u_abcdefgh/cache/generated/images/a.png"}`
	rec := do(t, s, "/media", "Bearer "+testToken, ok)
	if rec.Code != http.StatusOK || rec.Body.String() != "\x89PNG-bytes" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("own image: %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
	for name, path := range map[string]string{
		"another profile":   "/opt/data/profiles/u_otherone1/cache/generated/images/a.png",
		"outside the cache": "/opt/data/profiles/u_abcdefgh/.env",
		"traversal":         "/opt/data/profiles/u_abcdefgh/cache/../.env",
		"not media":         "/opt/data/profiles/u_abcdefgh/cache/notes.txt",
		"relative":          "cache/generated/images/a.png",
	} {
		before := len(f.calls)
		rec := do(t, s, "/media", "Bearer "+testToken, `{"slug":"u_abcdefgh","path":"`+path+`"}`)
		if rec.Code != http.StatusNotFound || len(f.calls) != before {
			t.Errorf("%s: %d, %d exec calls", name, rec.Code, len(f.calls)-before)
		}
	}
	if rec := do(t, s, "/media", "Bearer wrong", ok); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	f.body, f.err = "", exitErr(3)
	if rec := do(t, s, "/media", "Bearer "+testToken, ok); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "PNG") {
		t.Errorf("refused in the container: %d", rec.Code)
	}
	// A broken exec is not "no media": it is a 502, so the failure is visible.
	f.err = exitErr(1)
	if rec := do(t, s, "/media", "Bearer "+testToken, ok); rec.Code != http.StatusBadGateway {
		t.Errorf("broken exec: %d", rec.Code)
	}
}

// The media script itself, run by python3 against a temporary cache: a regular file inside is served; a symlink
// anywhere on the way (to a file outside, to a directory outside, or to a file inside) is refused, as is a missing file.
func TestMediaScriptRefusesSymlinksAnywhereOnThePath(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "profiles")
	root := filepath.Join(base, "u_abcdefgh", "cache") + "/"
	other := filepath.Join(dir, "profiles", "u_otherone1", "cache")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "generated", "images"), 0o755))
	must(os.MkdirAll(other, 0o755))
	must(os.WriteFile(filepath.Join(root, "generated", "images", "own.png"), []byte("OWN"), 0o644))
	must(os.WriteFile(filepath.Join(other, "secret.png"), []byte("SECRET"), 0o644))
	must(os.Symlink(filepath.Join(other, "secret.png"), filepath.Join(root, "generated", "images", "link.png")))
	must(os.Symlink(other, filepath.Join(root, "swapped")))
	must(os.Symlink(filepath.Join(root, "generated", "images", "own.png"), filepath.Join(root, "inner.png")))
	run := func(path string) (string, int) {
		cmd := exec.Command("python3", "-c", mediaScript, base, "u_abcdefgh", path, "1048576")
		out, err := cmd.Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}
	if out, code := run(root + "generated/images/own.png"); code != 0 || out != "OWN" {
		t.Fatalf("own file: %d %q", code, out)
	}
	for _, p := range []string{root + "generated/images/link.png", root + "swapped/secret.png", root + "inner.png", root + "generated/images/none.png", root + "../.env"} {
		if out, code := run(p); code != 3 || strings.Contains(out, "SECRET") || out != "" {
			t.Errorf("%s: exit %d, %q", p, code, out)
		}
	}
	// The cache directory itself replaced by a symlink to another profile's cache.
	must(os.RemoveAll(root))
	must(os.Symlink(other, filepath.Join(base, "u_abcdefgh", "cache")))
	if out, code := run(root + "secret.png"); code != 3 || out != "" {
		t.Errorf("symlinked cache: exit %d, %q", code, out)
	}
}
