package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

type mediaRunner struct {
	scripted
	body string
}

func (f *mediaRunner) Run(ctx context.Context, in string, args ...string) (string, string, error) {
	if args[0] == "python3" && args[2] == mediaScript {
		f.calls = append(f.calls, args)
		return f.body, "", nil
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
	f.body = ""
	if rec := do(t, s, "/media", "Bearer "+testToken, ok); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "PNG") {
		t.Errorf("refused in the container: %d", rec.Code)
	}
}
