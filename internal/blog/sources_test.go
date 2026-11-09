package blog

import (
	"testing"
	"time"
)

func TestPublicHTTPS(t *testing.T) {
	for s, want := range map[string]bool{
		"https://example.org/a?b=c#d":     true,
		"https://sub.example.co.id/path":  true,
		"https://example.org:443/":        true,
		"http://example.org/":             false,
		"javascript:alert(1)":             false,
		"https://user:pw@example.org/":    false,
		"https://user@example.org/":       false,
		"https://example.org:8443/":       false,
		"https://localhost/":              false,
		"https://app.localhost/":          false,
		"https://intranet/":               false,
		"https://printer.local/":          false,
		"https://svc.internal/":           false,
		"https://127.0.0.1/":              false,
		"https://10.0.0.5/":               false,
		"https://192.168.1.1/":            false,
		"https://172.16.0.1/":             false,
		"https://169.254.169.254/latest":  false,
		"https://100.64.0.1/":             false,
		"https://0.0.0.0/":                false,
		"https://[::1]/":                  false,
		"https://[fe80::1]/":              false,
		"https://[::ffff:127.0.0.1]/":     false,
		"https://2130706433/":             false,
		"https://8.8.8.8/":                true,
		"https://[2606:4700:4700::1111]/": true,
		"":                                false,
		" https://example.org/":           false,
		"//example.org/":                  false,
	} {
		if got := PublicHTTPS(s); got != want {
			t.Errorf("PublicHTTPS(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestCleanSource(t *testing.T) {
	today := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	ok := Source{URL: "https://example.org/x", Title: "  A   title ", RetrievedAt: "2026-11-08"}
	got, good := cleanSource(ok, today)
	if !good || got.Title != "A title" || got.Verified {
		t.Fatalf("%+v %v", got, good)
	}
	for name, s := range map[string]Source{
		"http":         {URL: "http://example.org/", Title: "t", RetrievedAt: "2026-11-08"},
		"private":      {URL: "https://10.1.1.1/", Title: "t", RetrievedAt: "2026-11-08"},
		"no title":     {URL: "https://example.org/", Title: "  ", RetrievedAt: "2026-11-08"},
		"not a date":   {URL: "https://example.org/", Title: "t", RetrievedAt: "yesterday"},
		"future date":  {URL: "https://example.org/", Title: "t", RetrievedAt: "2027-01-01"},
		"ancient date": {URL: "https://example.org/", Title: "t", RetrievedAt: "2001-01-01"},
	} {
		if _, good := cleanSource(s, today); good {
			t.Errorf("%s was accepted", name)
		}
	}
	// Whatever the assistant claims, nothing here can mark a source verified.
	claimed := ok
	claimed.Verified = true
	if got, _ := cleanSource(claimed, today); got.Verified {
		t.Error("a source claimed verified came out verified")
	}
}
