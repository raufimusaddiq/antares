package server

import (
	"bytes"
	"context"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

// The blog's pages are the app shell with the page's own metadata written into it, so a search engine or a link preview
// sees the post's title and summary without running a script. Everything written in is escaped text; nothing is
// executable, and the shell's strict Content-Security-Policy is unchanged.

// PageMeta is the metadata of one published post (or of the blog index).
type PageMeta struct{ Title, Description, Image string }

// PostMeta looks up a published post's metadata by address; false for anything a reader may not see.
type PostMeta func(ctx context.Context, slug string) (PageMeta, bool)

// Limit wraps a handler in a request allowance.
type Limit func(http.Handler) http.Handler

// WithBlog serves /b and /b/{slug} from the shell with metadata written in. Each page costs a database read for a caller
// with no session, so limit (the same allowance as the public JSON reads) bounds them; nil means unlimited.
func (s *Server) WithBlog(m PostMeta, limit Limit) *Server {
	s.post = m
	s.postLimit = limit
	return s
}

var (
	reTitle    = regexp.MustCompile(`<title>[^<]*</title>`)
	reRobots   = regexp.MustCompile(`[ \t]*<meta name="robots" content="noindex" />\r?\n?`)
	reDesc     = regexp.MustCompile(`(<meta name="description" content=")[^"]*(")`)
	reOGTitle  = regexp.MustCompile(`(<meta (?:property="og:title"|name="twitter:title") content=")[^"]*(")`)
	reOGDesc   = regexp.MustCompile(`(<meta (?:property="og:description"|name="twitter:description") content=")[^"]*(")`)
	reOGURL    = regexp.MustCompile(`(<meta property="og:url" content=")[^"]*(")`)
	reCanon    = regexp.MustCompile(`(<link rel="canonical" href=")([^"]*)(")`)
	reCanon3   = regexp.MustCompile(`(<link rel="canonical" href=")[^"]*(")`)
	reOGImage  = regexp.MustCompile(`(<meta (?:property="og:image"|name="twitter:image") content=")[^"]*(")`)
	reSlugPath = regexp.MustCompile(`^/b/([a-z0-9-]{3,80})/?$`)
)

func (s *Server) blogShell(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var meta PageMeta
	status := http.StatusOK
	switch {
	case r.URL.Path == "/b" || r.URL.Path == "/b/":
		meta = PageMeta{Title: "Blog · Antares", Description: "Posts written on Antares."}
	default:
		m := reSlugPath.FindStringSubmatch(r.URL.Path)
		var ok bool
		if m != nil {
			meta, ok = s.post(r.Context(), m[1])
		}
		if !ok { // the same answer for every post a reader may not see
			status = http.StatusNotFound
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		}
	}
	if status == http.StatusOK {
		raw = withMeta(raw, r.URL.Path, meta)
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	shellHeaders(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}

// withMeta writes the page's title, description, address and cover into the shell, and a text copy for readers
// without scripts. The shell's noindex is removed: a published post is meant to be found.
// setAttr puts value between the two captured halves of every match. The value is inserted as it is: a "$1" in a
// title must stay "$1" and must never be read as a reference to a captured group.
func setAttr(re *regexp.Regexp, src []byte, value string) []byte {
	return re.ReplaceAllFunc(src, func(m []byte) []byte {
		g := re.FindSubmatch(m)
		out := append([]byte{}, g[1]...)
		out = append(out, value...)
		return append(out, g[len(g)-1]...)
	})
}

// withMeta writes the page's title, description, address and cover into the shell, and a text copy for readers
// without scripts. The shell's noindex is removed: a published post is meant to be found.
func withMeta(shell []byte, path string, m PageMeta) []byte {
	esc := html.EscapeString
	title := esc(m.Title)
	desc := esc(m.Description)
	out := reRobots.ReplaceAll(shell, nil)
	out = reTitle.ReplaceAllLiteral(out, []byte("<title>"+title+"</title>"))
	out = setAttr(reDesc, out, desc)
	out = setAttr(reOGTitle, out, title)
	out = setAttr(reOGDesc, out, desc)
	if c := reCanon.FindSubmatch(out); c != nil {
		origin := strings.TrimRight(string(c[2]), "/")
		url := esc(origin + path)
		out = setAttr(reCanon3, out, url)
		out = setAttr(reOGURL, out, url)
		if strings.HasPrefix(m.Image, "/") {
			out = setAttr(reOGImage, out, esc(origin+m.Image))
		}
	}
	copyText := "<noscript><article><h1>" + title + "</h1>"
	if m.Description != "" {
		copyText += "<p>" + desc + "</p>"
	}
	copyText += "</article></noscript>"
	return bytes.Replace(out, []byte(`<div id="root"></div>`), []byte(`<div id="root"></div>`+copyText), 1)
}
