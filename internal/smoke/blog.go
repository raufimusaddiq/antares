package smoke

import (
	"context"
	"fmt"
	"strings"
)

type blogPost struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Slug          string `json:"slug"`
	LatestVersion int    `json:"latestVersion"`
}

// blog is the production check of the blog (specs/001-blog-cms-openui-publish): A's whole lifecycle through the real
// routes, with B and an anonymous reader probing what they must not see. The post it makes is deleted at the end, and on
// any failure on the way out (the fixture cleanup removes the accounts and so anything left).
func (s *scn) blog(ctx context.Context) (string, error) {
	a, b := s.cfg.A, s.cfg.B
	title := "Smoke post " + s.cfg.RunID
	slug := "smoke-" + s.cfg.RunID
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Written by the smoke test."}}}}}

	step := func(want int, as *Actor, method, path string, body any) (*Response, error) {
		r, err := s.c.Do(ctx, as.Session, method, path, nil, body)
		if err != nil {
			return nil, err
		}
		if r.Status != want {
			return r, fmt.Errorf("%s %s: wanted %d, got %s", method, path, want, r.Brief())
		}
		return r, nil
	}
	anon := func(want int, path string) (*Response, error) {
		r, err := s.c.Do(ctx, nil, "GET", path, nil, nil)
		if err != nil {
			return nil, err
		}
		if r.Status != want {
			return r, fmt.Errorf("anonymous GET %s: wanted %d, got %s", path, want, r.Brief())
		}
		return r, nil
	}

	r, err := step(200, a, "GET", "/api/blog/capabilities", nil)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(r.Body), `"writing":true`) {
		return "", fmt.Errorf("capabilities do not offer writing: %s", r.Brief())
	}
	r, err = step(200, a, "POST", "/api/blog/posts", map[string]any{})
	if err != nil {
		return "", err
	}
	var p blogPost
	if r.JSON(&p) != nil || p.ID == "" {
		return "", fmt.Errorf("create: unreadable answer")
	}
	path := "/api/blog/posts/" + p.ID
	defer func() { _, _ = s.c.Do(context.WithoutCancel(ctx), a.Session, "DELETE", path, nil, nil) }()

	if _, err := step(200, a, "PUT", path, map[string]any{"baseVersion": p.LatestVersion, "title": title, "doc": doc}); err != nil {
		return "", err
	}
	if _, err := step(409, a, "PUT", path, map[string]any{"baseVersion": p.LatestVersion, "title": "stale"}); err != nil {
		return "", fmt.Errorf("a save from a stale version must be refused: %w", err)
	}
	if _, err := step(200, a, "PATCH", path+"/settings", map[string]any{"slug": slug, "summary": "A post the smoke test made."}); err != nil {
		return "", err
	}
	// B is a stranger to A's draft: the same 404 as for a post that does not exist.
	for _, probe := range []struct{ method, path string }{{"GET", path}, {"DELETE", path}} {
		if _, err := step(404, b, probe.method, probe.path, nil); err != nil {
			return "", fmt.Errorf("another user: %w", err)
		}
	}
	if _, err := anon(404, "/api/public/blog/posts/"+slug); err != nil {
		return "", fmt.Errorf("a draft must not be public: %w", err)
	}
	if _, err := step(200, a, "POST", path+"/publish", map[string]string{"mode": "original"}); err != nil {
		return "", err
	}
	pub, err := anon(200, "/api/public/blog/posts/"+slug)
	if err != nil {
		return "", err
	}
	me, _, merr := s.me(ctx, a)
	if merr != nil {
		return "", merr
	}
	for _, leak := range []string{p.ID, me.ID, "@", "tenant", "profile"} {
		if leak != "" && strings.Contains(string(pub.Body), leak) {
			return "", fmt.Errorf("the public post contains %q", leak)
		}
	}
	if !strings.Contains(string(pub.Body), title) {
		return "", fmt.Errorf("the public post does not carry its title")
	}
	page, err := anon(200, "/b/"+slug)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(page.Body), "<title>"+title+"</title>") || strings.Contains(string(page.Body), `content="noindex"`) {
		return "", fmt.Errorf("the page /b/%s has no title of its own or is marked noindex", slug)
	}
	if list, err := anon(200, "/api/public/blog/posts"); err != nil || !strings.Contains(string(list.Body), slug) {
		return "", fmt.Errorf("the published post is not in the public list")
	}
	if _, err := step(200, a, "POST", path+"/unpublish", map[string]any{}); err != nil {
		return "", err
	}
	if _, err := anon(404, "/api/public/blog/posts/"+slug); err != nil {
		return "", fmt.Errorf("an unpublished post must not be public: %w", err)
	}
	if _, err := step(200, a, "DELETE", path, nil); err != nil {
		return "", err
	}
	if _, err := step(404, a, "GET", path, nil); err != nil {
		return "", err
	}
	return "draft private, 409 on a stale save, stranger got 404, published post readable signed out (no ids), page carries its own title, unpublished and deleted posts gone", nil
}
