package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// A shell as the build ships it: noindex, the app's own title and description, its canonical address and card tags.
const testShell = `<!doctype html><html><head>
    <meta name="robots" content="noindex" />
    <title>Antares</title>
    <meta name="description" content="A private chat, by invitation." />
    <link rel="canonical" href="https://antares.example/" />
    <meta property="og:title" content="Antares" />
    <meta property="og:description" content="A private chat, by invitation." />
    <meta property="og:url" content="https://antares.example/" />
    <meta property="og:image" content="https://antares.example/og.png" />
    <meta name="twitter:title" content="Antares" />
    <meta name="twitter:description" content="A private chat, by invitation." />
    <meta name="twitter:image" content="https://antares.example/og.png" />
  </head><body><div id="root"></div></body></html>`

func blogServer(t *testing.T, m PostMeta, limit Limit) http.Handler {
	t.Helper()
	assets := fstest.MapFS{"index.html": {Data: []byte(testShell)}}
	return New(func(context.Context) error { return nil }, t.TempDir()).WithAssets(assets).WithBlog(m, limit).Routes()
}

func page(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func has(body, sub string) bool { return strings.Contains(body, sub) }

func TestTheBlogIndexIsTheShellWithItsOwnTitleAndIsFindable(t *testing.T) {
	h := blogServer(t, func(context.Context, string) (PageMeta, error) {
		t.Fatal("the index is not a post lookup")
		return PageMeta{}, nil
	}, nil)
	for _, path := range []string{"/b"} {
		rec := page(h, http.MethodGet, path)
		b := rec.Body.String()
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		for _, want := range []string{
			"<title>Blog · Antares</title>",
			`<meta name="description" content="Posts written on Antares." />`,
			`<meta property="og:title" content="Blog · Antares" />`,
			`<link rel="canonical" href="https://antares.example/b" />`,
			`<meta property="og:url" content="https://antares.example/b" />`,
			"<noscript><article><h1>Blog · Antares</h1><p>Posts written on Antares.</p></article></noscript>",
		} {
			if !has(b, want) {
				t.Errorf("%s lacks %s:\n%s", path, want, b)
			}
		}
		if has(b, "noindex") {
			t.Errorf("%s still says noindex: a blog is meant to be found", path)
		}
		if has(b, "A private chat") {
			t.Errorf("%s keeps the app's own description", path)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s headers: %q %q %q", path, ct, rec.Header().Get("Cache-Control"), rec.Header().Get("X-Content-Type-Options"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: the shell's Content-Security-Policy is missing", path)
		}
	}
}

// A title or summary is a stranger's text: it is escaped into the page, a "$1" stays "$1", and nothing in it can become markup.
func TestAPostsOwnWordsAreWrittenInAsTextAndNothingElse(t *testing.T) {
	meta := PageMeta{Title: `Cost "$1" <script>alert(1)</script> & more`, Description: `It says "hi" -> <b>bold</b> $2`, Image: "/api/public/blog/posts/my-post/assets/a1"}
	h := blogServer(t, func(_ context.Context, slug string) (PageMeta, error) {
		if slug != "my-post" {
			t.Errorf("looked up %q", slug)
		}
		return meta, nil
	}, nil)
	rec := page(h, http.MethodGet, "/b/my-post")
	b := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if has(b, "<script>") || has(b, "<b>bold</b>") {
		t.Fatalf("a stranger's markup reached the page:\n%s", b)
	}
	for _, want := range []string{
		"<title>Cost &#34;$1&#34; &lt;script&gt;alert(1)&lt;/script&gt; &amp; more</title>",
		`content="It says &#34;hi&#34; -&gt; &lt;b&gt;bold&lt;/b&gt; $2"`,
		`<link rel="canonical" href="https://antares.example/b/my-post" />`,
		`<meta property="og:image" content="https://antares.example/api/public/blog/posts/my-post/assets/a1" />`,
		`<meta name="twitter:image" content="https://antares.example/api/public/blog/posts/my-post/assets/a1" />`,
	} {
		if !has(b, want) {
			t.Errorf("lacks %s:\n%s", want, b)
		}
	}
	if has(b, "og.png") {
		t.Errorf("the app's own card image stayed when the post has a cover")
	}
}

func TestAPostWithoutACoverKeepsTheCardImageAndOnlyARelativeCoverIsUsed(t *testing.T) {
	for name, image := range map[string]string{"none": "", "a remote address": "https://evil.example/x.png"} {
		h := blogServer(t, func(context.Context, string) (PageMeta, error) { return PageMeta{Title: "T", Image: image}, nil }, nil)
		b := page(h, http.MethodGet, "/b/some-post").Body.String()
		if !has(b, `og:image" content="https://antares.example/og.png"`) || has(b, "evil.example") {
			t.Errorf("%s: %s", name, b)
		}
	}
}

// Everything a reader may not see is the same page: a 404 that says nothing about the post, whatever the reason.
func TestWhatAReaderMayNotSeeIsOneAnswerAndLooksNothingUp(t *testing.T) {
	lookups := 0
	h := blogServer(t, func(context.Context, string) (PageMeta, error) { lookups++; return PageMeta{}, ErrNoPage }, nil)
	var bodies []string
	for _, path := range []string{"/b/unpublished-post", "/b/Bad_Slug", "/b/ab", "/b/" + strings.Repeat("a", 81), "/b/a/b"} {
		rec := page(h, http.MethodGet, path)
		if rec.Code != 404 || rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Errorf("%s: %d robots %q", path, rec.Code, rec.Header().Get("X-Robots-Tag"))
		}
		b := rec.Body.String()
		if !has(b, `<meta name="robots" content="noindex" />`) || !has(b, "<title>Antares</title>") {
			t.Errorf("%s: the 404 is not the plain shell:\n%s", path, b)
		}
		bodies = append(bodies, b)
	}
	for i := range bodies {
		if bodies[i] != bodies[0] {
			t.Errorf("answer %d differs from the first", i)
		}
	}
	if lookups != 1 { // only the well-formed address was looked up
		t.Errorf("%d lookups: a malformed address must not reach the database", lookups)
	}
}

func TestALookupFailureIsOurFailureAndSaysNothingOfItsCause(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	h := blogServer(t, func(context.Context, string) (PageMeta, error) {
		return PageMeta{}, errors.New("sqlite: disk I/O error at /var/lib/secret")
	}, nil)
	rec := page(h, http.MethodGet, "/b/some-post")
	if rec.Code != 500 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if has(rec.Body.String(), "sqlite") || has(rec.Body.String(), "secret") || !has(rec.Body.String(), "Something went wrong") {
		t.Fatalf("the answer %q", rec.Body.String())
	}
	if !has(logs.String(), "disk I/O error") {
		t.Fatalf("the cause was not logged: %q", logs.String())
	}
}

func TestOneAddressPerPageAndHeadHasNoBody(t *testing.T) {
	h := blogServer(t, func(context.Context, string) (PageMeta, error) { return PageMeta{Title: "T"}, nil }, nil)
	for path, want := range map[string]string{"/b/some-post/": "/b/some-post", "/b/": "/b"} { // the index has one address too
		rec := page(h, http.MethodGet, path)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != want {
			t.Fatalf("%s: %d %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	head := page(h, http.MethodHead, "/b/some-post")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("HEAD: %d, %d body bytes, %q", head.Code, head.Body.Len(), head.Header().Get("Content-Type"))
	}
}

// A page costs a database read for someone with no session, so the allowance in front of it is applied to each.
func TestEveryBlogPageGoesThroughTheAllowance(t *testing.T) {
	lookups, limited := 0, 0
	limit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limited++
			http.Error(w, "slow down", http.StatusTooManyRequests)
		})
	}
	h := blogServer(t, func(context.Context, string) (PageMeta, error) { lookups++; return PageMeta{}, nil }, limit)
	for _, path := range []string{"/b", "/b/some-post"} {
		if rec := page(h, http.MethodGet, path); rec.Code != 429 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if limited != 2 || lookups != 0 {
		t.Fatalf("limited %d, looked up %d", limited, lookups)
	}
}

func TestWithoutABlogTheAddressesAreNotTheBlogsAndAMissingShellIsA404(t *testing.T) {
	plain := New(func(context.Context) error { return nil }, t.TempDir()).WithAssets(fstest.MapFS{"index.html": {Data: []byte(testShell)}}).Routes()
	if b := page(plain, http.MethodGet, "/b/some-post").Body.String(); has(b, "<noscript><article>") {
		t.Errorf("a server with no blog wrote a post's copy into a page")
	}
	empty := New(func(context.Context) error { return nil }, t.TempDir()).WithAssets(fstest.MapFS{}).WithBlog(func(context.Context, string) (PageMeta, error) { return PageMeta{}, nil }, nil).Routes()
	if rec := page(empty, http.MethodGet, "/b"); rec.Code != 404 {
		t.Errorf("no shell: %d", rec.Code)
	}
}

func TestSetAttrKeepsWhatItIsGivenLiterally(t *testing.T) {
	re := regexp.MustCompile(`(<meta name="x" content=")[^"]*(")`)
	src := []byte(`<meta name="x" content="old" /><meta name="x" content="older" />`)
	if got := string(setAttr(re, src, "$1 ${2} \\1 &amp;")); got != `<meta name="x" content="$1 ${2} \1 &amp;" /><meta name="x" content="$1 ${2} \1 &amp;" />` {
		t.Errorf("every match is set, and the value is not read as a group reference: %s", got)
	}
	if got := string(setAttr(re, []byte("no meta here"), "v")); got != "no meta here" {
		t.Errorf("no match changes nothing: %s", got)
	}
}
