package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// The public read: the only part of the blog that answers without a session. It serves published posts of approved
// authors and nothing else, makes no Hermes call, and says nothing of the author but a display name. Every miss
// (unknown, draft, unpublished, author suspended) is the same 404.

const (
	publicPageSize = 20
	maxPublicPage  = 500
)

// publicWhere selects exactly the rows a reader may see. approval_status is read in the same statement, so a suspended
// or rejected author's posts vanish and a reinstated author's return with no other change. The author's workspace state
// (tenant status) is deliberately not part of it: re-verifying an assistant profile must not take writing offline.
const publicWhere = `p.status = 'published' AND p.published_version > 0 AND u.approval_status = 'APPROVED'`

type PublicSummary struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	CoverURL    string `json:"coverUrl"`
	AuthorName  string `json:"authorName"`
	PublishedAt string `json:"publishedAt"`
}

type PublicPost struct {
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	CoverURL    string          `json:"coverUrl"`
	Doc         json.RawMessage `json:"doc"`
	Blocks      []PublicBlock   `json:"blocks"`
	AuthorName  string          `json:"authorName"`
	PublishedAt string          `json:"publishedAt"`
}

// PublicBlock is an accepted block (its sources carry verified: always false today, so a client can say they were not checked) as readers get it: where it goes, its markup, its plain-text version and its sources.
type PublicBlock struct {
	Anchor   int      `json:"anchor"`
	Markup   string   `json:"markup"`
	Fallback string   `json:"fallback"`
	Sources  []Source `json:"sources"`
}

// publicBlocks are the blocks published with the post's published version (none for a post published as written).
func (s *Service) publicBlocks(ctx context.Context, postID string) ([]PublicBlock, error) {
	out := []PublicBlock{}
	rows, err := s.db.QueryContext(ctx, `SELECT b.position, b.anchor, b.markup, b.fallback, s.url, s.title, s.retrieved_at
		FROM published_blocks b JOIN posts p ON p.id = b.post_id AND p.published_version = b.version
		LEFT JOIN published_block_sources s ON s.post_id = b.post_id AND s.version = b.version AND s.block = b.position
		WHERE b.post_id = ? ORDER BY b.position, s.position`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	last := -1
	for rows.Next() {
		var pos int
		var b PublicBlock
		var url, title, at sql.NullString
		if err := rows.Scan(&pos, &b.Anchor, &b.Markup, &b.Fallback, &url, &title, &at); err != nil {
			return nil, err
		}
		if pos != last {
			b.Sources = []Source{}
			out, last = append(out, b), pos
		}
		if url.Valid {
			out[len(out)-1].Sources = append(out[len(out)-1].Sources, Source{URL: url.String, Title: title.String, RetrievedAt: at.String})
		}
	}
	return out, rows.Err()
}

func publicAssetURL(slug, assetID string) string {
	if assetID == "" {
		return ""
	}
	return "/api/public/blog/posts/" + slug + "/assets/" + assetID
}

func authorName(n sql.NullString) string {
	if s := strings.TrimSpace(n.String); s != "" {
		return s
	}
	return "Author"
}

func (s *Service) PublicList(ctx context.Context, page int) ([]PublicSummary, error) {
	if page < 1 {
		page = 1
	}
	if page > maxPublicPage { // an unauthenticated caller must not make the database walk a huge offset
		return []PublicSummary{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.slug, v.title, v.summary, p.cover_asset_id, u.display_name, p.published_at
		FROM posts p JOIN users u ON u.id = p.author_id JOIN post_versions v ON v.post_id = p.id AND v.version = p.published_version
		WHERE `+publicWhere+` ORDER BY p.published_at DESC, p.id LIMIT ? OFFSET ?`, publicPageSize, (page-1)*publicPageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicSummary{}
	for rows.Next() {
		var x PublicSummary
		var cover string
		var name sql.NullString
		if err := rows.Scan(&x.Slug, &x.Title, &x.Summary, &cover, &name, &x.PublishedAt); err != nil {
			return nil, err
		}
		x.CoverURL, x.AuthorName = publicAssetURL(x.Slug, cover), authorName(name)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) PublicGet(ctx context.Context, slug string) (PublicPost, error) {
	if !wellFormedSlug(slug) {
		return PublicPost{}, ErrNotFound
	}
	var x PublicPost
	var cover, doc, postID string
	var name sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT v.title, v.summary, p.cover_asset_id, v.doc, u.display_name, p.published_at, p.id
		FROM posts p JOIN users u ON u.id = p.author_id JOIN post_versions v ON v.post_id = p.id AND v.version = p.published_version
		WHERE p.slug = ? AND `+publicWhere, slug).Scan(&x.Title, &x.Summary, &cover, &doc, &name, &x.PublishedAt, &postID)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicPost{}, ErrNotFound
	}
	if err != nil {
		return PublicPost{}, err
	}
	// The stored images point at the author's route (and carry the internal post id); readers get their own address.
	doc = strings.ReplaceAll(doc, "/api/blog/posts/"+postID+"/assets/", "/api/public/blog/posts/"+slug+"/assets/")
	blocks, err := s.publicBlocks(ctx, postID)
	if err != nil {
		return PublicPost{}, err
	}
	x.CoverURL, x.AuthorName, x.Doc, x.Blocks = publicAssetURL(slug, cover), authorName(name), json.RawMessage(doc), blocks
	return x, nil
}

// PublicAsset reads an image of a published post, by the post's address. It does not check that the published version
// uses the image: one uploaded for a later edit of a live post is readable too (its id is 128 random bits, so it cannot be
// guessed). When the check stage lands, tie this to the images the published document names.
func (s *Service) PublicAsset(ctx context.Context, slug, assetID string) (Asset, error) {
	if !wellFormedSlug(slug) || !assetRE.MatchString(assetID) {
		return Asset{}, ErrNotFound
	}
	var a Asset
	err := s.db.QueryRowContext(ctx, `SELECT a.content_type, a.bytes FROM post_assets a JOIN posts p ON p.id = a.post_id JOIN users u ON u.id = p.author_id
		WHERE p.slug = ? AND a.id = ? AND `+publicWhere, slug, assetID).Scan(&a.Type, &a.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	return a, err
}

// PageMeta is what the HTML shell of a published post carries for search engines and link previews.
type PageMeta struct{ Title, Description, Image string }

// Meta returns the metadata for a published post.
// ErrNotFound for anything a reader may not see; any other error is ours (a failing database) and is not a 404.
func (s *Service) Meta(ctx context.Context, slug string) (PageMeta, error) {
	if !wellFormedSlug(slug) {
		return PageMeta{}, ErrNotFound
	}
	var m PageMeta
	var cover string
	err := s.db.QueryRowContext(ctx, `SELECT v.title, v.summary, p.cover_asset_id
		FROM posts p JOIN users u ON u.id = p.author_id JOIN post_versions v ON v.post_id = p.id AND v.version = p.published_version
		WHERE p.slug = ? AND `+publicWhere, slug).Scan(&m.Title, &m.Description, &cover)
	if errors.Is(err, sql.ErrNoRows) {
		return PageMeta{}, ErrNotFound
	}
	if err != nil {
		return PageMeta{}, err
	}
	m.Image = publicAssetURL(slug, cover)
	return m, nil
}

// PublicHandlers are the routes that answer without a session, keyed as in auth.PublicBlogRoutes.
func (s *Service) PublicHandlers() map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/public/blog/posts": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			l, err := s.PublicList(r.Context(), page)
			if err != nil {
				fail(w, "public list", err)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=60")
			writeJSON(w, http.StatusOK, l)
		}),
		"GET /api/public/blog/posts/{slug}": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := s.PublicGet(r.Context(), r.PathValue("slug"))
			if errors.Is(err, ErrNotFound) {
				notFound(w)
				return
			} else if err != nil {
				fail(w, "public get", err)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=60")
			writeJSON(w, http.StatusOK, p)
		}),
		"GET /api/public/blog/posts/{slug}/assets/{assetId}": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, err := s.PublicAsset(r.Context(), r.PathValue("slug"), r.PathValue("assetId"))
			if errors.Is(err, ErrNotFound) {
				notFound(w)
				return
			} else if err != nil {
				fail(w, "public asset", err)
				return
			}
			writeAsset(w, a, "public, max-age=300")
		}),
	}
}
