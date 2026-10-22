package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func compressFixture() http.Handler {
	big := strings.Repeat("console.log('antares');\n", 200)
	return New(nil, "").WithAssets(fstest.MapFS{
		"index.html":         {Data: []byte(`<div id="root"></div>` + strings.Repeat(" ", 2000))},
		"assets/app-abc.js":  {Data: []byte(big)},
		"assets/tiny-abc.js": {Data: []byte("x=1")},
		"assets/logo.png":    {Data: []byte(strings.Repeat("\x89PNG", 2000))},
		"assets/font.woff2":  {Data: []byte(strings.Repeat("wOF2", 2000))},
	}).Routes()
}

func fetch(h http.Handler, method, path, ae string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if ae != "" {
		req.Header.Set("Accept-Encoding", ae)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGzipWhenAcceptedAndBodyDecodes(t *testing.T) {
	h := compressFixture()
	rec := fetch(h, "GET", "/assets/app-abc.js", "gzip, deflate")
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, Content-Encoding %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q", rec.Header().Get("Vary"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if string(b) != strings.Repeat("console.log('antares');\n", 200) {
		t.Fatalf("decoded body differs (%d bytes)", len(b))
	}
	if rec.Body.Len() >= len(b) {
		t.Errorf("gzip body %d not smaller than %d", rec.Body.Len(), len(b))
	}
	// the shell too, and HEAD reports the compressed length
	if fetch(h, "GET", "/", "gzip").Header().Get("Content-Encoding") != "gzip" {
		t.Error("index.html not compressed")
	}
	head := fetch(h, "HEAD", "/assets/app-abc.js", "gzip")
	if head.Header().Get("Content-Encoding") != "gzip" || head.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD headers %v", head.Header())
	}
}

func TestIdentityWithoutAcceptEncoding(t *testing.T) {
	h := compressFixture()
	for _, ae := range []string{"", "identity", "br", "gzip;q=0", "deflate, gzip;q=0, *"} {
		rec := fetch(h, "GET", "/assets/app-abc.js", ae)
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("Accept-Encoding %q got Content-Encoding %q", ae, rec.Header().Get("Content-Encoding"))
		}
		if rec.Body.Len() != len(strings.Repeat("console.log('antares');\n", 200)) {
			t.Errorf("Accept-Encoding %q: body %d bytes, want identity", ae, rec.Body.Len())
		}
		if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
			t.Errorf("identity response lacks Vary for %q", ae)
		}
	}
}

func TestAlreadyCompressedTypesAreNeverCompressed(t *testing.T) {
	h := compressFixture()
	for _, p := range []string{"/assets/logo.png", "/assets/font.woff2"} {
		rec := fetch(h, "GET", p, "gzip, br")
		if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: status %d, Content-Encoding %q", p, rec.Code, rec.Header().Get("Content-Encoding"))
		}
		if rec.Body.Len() != 8000 {
			t.Errorf("%s: body %d bytes, want the original 8000", p, rec.Body.Len())
		}
	}
	if ct := fetch(h, "GET", "/assets/logo.png", "gzip").Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("png Content-Type = %q", ct)
	}
}

func TestSmallFilesAndAPIAreNotCompressed(t *testing.T) {
	h := compressFixture()
	if rec := fetch(h, "GET", "/assets/tiny-abc.js", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Error("file under the threshold was compressed")
	}
	for _, p := range []string{"/healthz", "/api/nope"} {
		if rec := fetch(h, "GET", p, "gzip"); rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s was compressed", p)
		}
	}
}

func TestAcceptsGzip(t *testing.T) {
	for in, want := range map[string]bool{
		"gzip": true, "GZIP": true, "br, gzip;q=0.5": true, "*": true, "*;q=0": false,
		"gzip;q=0": false, "identity": false, "": false, "gzip;q=0, *": false, "x-gzip": true,
	} {
		if got := acceptsGzip(in); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", in, got, want)
		}
	}
}
