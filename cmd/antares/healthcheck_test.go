package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		ok     bool
	}{{"ready", 200, true}, {"unavailable", 503, false}} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					t.Errorf("probed %s, want /readyz", r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			defer ts.Close()
			err := healthcheck(context.Background(), strings.TrimPrefix(ts.URL, "http://"))
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	if err := healthcheck(context.Background(), "127.0.0.1:1"); err == nil {
		t.Fatal("nothing listening must fail")
	}
}
