package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

var (
	ErrNotFound     = errors.New("blog: no such post") // missing and foreign are the same
	ErrConflict     = errors.New("blog: the post changed")
	ErrInvalid      = errors.New("blog: invalid")
	ErrSlugTaken    = errors.New("blog: that address is used by another published post")
	ErrTooMany      = errors.New("blog: too many")
	ErrNotPublished = errors.New("blog: the post is not published")
)

const (
	maxTitle   = 200
	maxSummary = 500
	maxPosts   = 500
)

// Service is the blog's store and handlers.
type Service struct {
	db  *sql.DB
	now func() time.Time
}

// Options configure New.
type Options struct {
	DB  *sql.DB
	Now func() time.Time
}

func New(o Options) (*Service, error) {
	if o.DB == nil {
		return nil, errors.New("blog: database required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{db: o.DB, now: o.Now}, nil
}

func (s *Service) stamp() string { return s.now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

// Post is what the author sees of a post: the head version.
type Post struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Summary          string          `json:"summary"`
	Slug             string          `json:"slug"`
	CoverURL         string          `json:"coverUrl"`
	Status           string          `json:"status"`
	LatestVersion    int             `json:"latestVersion"`
	PublishedVersion int             `json:"publishedVersion"`
	Doc              json.RawMessage `json:"doc"`
	Check            any             `json:"check"`
	UpdatedAt        string          `json:"updatedAt"`
}

// Summary is one row of the author's list. Interactive is for posts that carry AI-added blocks; none can exist until the
// Hermes check does, so it is false for now.
type Summary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Slug        string `json:"slug"`
	UpdatedAt   string `json:"updatedAt"`
	PublishedAt string `json:"publishedAt"`
	Interactive bool   `json:"interactive"`
}

func assetURL(postID, assetID string) string {
	if assetID == "" {
		return ""
	}
	return "/api/blog/posts/" + postID + "/assets/" + assetID
}

const postCols = `p.id, p.title, p.summary, p.slug, p.cover_asset_id, p.status, p.latest_version, p.published_version, p.updated_at, v.doc`

// loadPost reads the author's own post at its head version. author_id is in the statement itself.
func loadPost(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, author, id string) (Post, error) {
	var p Post
	var cover, doc string
	err := q.QueryRowContext(ctx, `SELECT `+postCols+` FROM posts p JOIN post_versions v ON v.post_id = p.id AND v.version = p.latest_version
		WHERE p.id = ? AND p.author_id = ?`, id, author).Scan(&p.ID, &p.Title, &p.Summary, &p.Slug, &cover, &p.Status, &p.LatestVersion, &p.PublishedVersion, &p.UpdatedAt, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, err
	}
	p.CoverURL = assetURL(p.ID, cover)
	p.Doc = json.RawMessage(doc)
	return p, nil
}

func (s *Service) Get(ctx context.Context, author, id string) (Post, error) {
	return loadPost(ctx, s.db, author, id)
}

func (s *Service) List(ctx context.Context, author string) ([]Summary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, status, slug, updated_at, published_at FROM posts WHERE author_id = ? ORDER BY updated_at DESC LIMIT ?`, author, maxPosts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var x Summary
		if err := rows.Scan(&x.ID, &x.Title, &x.Status, &x.Slug, &x.UpdatedAt, &x.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, author string) (Post, error) {
	return s.insertPost(ctx, author, "", "", []byte(emptyDoc))
}

func (s *Service) insertPost(ctx context.Context, author, title, summary string, doc []byte) (Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE author_id = ?`, author).Scan(&n); err != nil {
		return Post{}, err
	}
	if n >= maxPosts {
		return Post{}, ErrTooMany
	}
	id, now := store.NewID(), s.stamp()
	if _, err := tx.ExecContext(ctx, `INSERT INTO posts (id, author_id, title, summary, status, latest_version, created_at, updated_at) VALUES (?,?,?,?,'draft',1,?,?)`, id, author, title, summary, now, now); err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO post_versions (post_id, version, title, summary, doc, created_at) VALUES (?,?,?,?,?,?)`, id, 1, title, summary, string(doc), now); err != nil {
		return Post{}, err
	}
	p, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	return p, tx.Commit()
}

// Change is an autosave: only the fields present are replaced.
type Change struct {
	Title   *string
	Summary *string
	Doc     []byte // already cleaned; nil keeps the document
}

// ConflictError carries the post as it now stands, for the 409 reply.
type ConflictError struct{ Latest Post }

func (ConflictError) Error() string { return ErrConflict.Error() }

// Autosave applies c on top of baseVersion. A stale base is refused with the latest post; nothing is overwritten.
func (s *Service) Autosave(ctx context.Context, author, id string, base int, c Change) (Post, error) {
	if c.Title != nil && len([]rune(*c.Title)) > maxTitle || c.Summary != nil && len([]rune(*c.Summary)) > maxSummary {
		return Post{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	cur, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	if base != cur.LatestVersion {
		return Post{}, ConflictError{Latest: cur}
	}
	title, summary, doc := cur.Title, cur.Summary, string(cur.Doc)
	if c.Title != nil {
		title = strings.TrimSpace(*c.Title)
	}
	if c.Summary != nil {
		summary = strings.TrimSpace(*c.Summary)
	}
	if c.Doc != nil {
		doc = string(c.Doc)
	}
	next, now := cur.LatestVersion+1, s.stamp()
	var pinned int
	if err := tx.QueryRowContext(ctx, `SELECT pinned FROM post_versions WHERE post_id = ? AND version = ?`, id, cur.LatestVersion).Scan(&pinned); err != nil {
		return Post{}, err
	}
	if pinned == 1 { // a published version is kept as it was published; this save starts the next one
		_, err = tx.ExecContext(ctx, `INSERT INTO post_versions (post_id, version, title, summary, doc, created_at) VALUES (?,?,?,?,?,?)`, id, next, title, summary, doc, now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE post_versions SET version = ?, title = ?, summary = ?, doc = ?, created_at = ? WHERE post_id = ? AND version = ?`, next, title, summary, doc, now, id, cur.LatestVersion)
	}
	if err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET title = ?, summary = ?, latest_version = ?, updated_at = ? WHERE id = ? AND author_id = ?`, title, summary, next, now, id, author); err != nil {
		return Post{}, err
	}
	p, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	return p, tx.Commit()
}

// Settings changes the address, summary and cover. A slug may be set on a draft; only a published post's slug must be
// unique, which the unique index enforces.
func (s *Service) Settings(ctx context.Context, author, id string, slug, summary, cover *string) (Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	cur, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	if slug != nil && *slug != "" && !ValidSlug(*slug) || summary != nil && len([]rune(*summary)) > maxSummary {
		return Post{}, ErrInvalid
	}
	if slug != nil {
		cur.Slug = *slug
	}
	if summary != nil {
		cur.Summary = strings.TrimSpace(*summary)
	}
	coverID := ""
	_ = tx.QueryRowContext(ctx, `SELECT cover_asset_id FROM posts WHERE id = ? AND author_id = ?`, id, author).Scan(&coverID)
	if cover != nil {
		if *cover != "" {
			var one int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM post_assets WHERE id = ? AND post_id = ? AND author_id = ?`, *cover, id, author).Scan(&one); err != nil {
				return Post{}, ErrInvalid
			}
		}
		coverID = *cover
	}
	if cur.Status == "published" && cur.Slug == "" {
		return Post{}, ErrInvalid
	}
	// The summary is settings, not body: it takes effect on the head and on the published version alike, without starting a
	// new version (which would make the open editor's next save a conflict).
	if summary != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE post_versions SET summary = ? WHERE post_id = ? AND version IN (?, ?)`, cur.Summary, id, cur.LatestVersion, cur.PublishedVersion); err != nil {
			return Post{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET slug = ?, summary = ?, cover_asset_id = ?, updated_at = ? WHERE id = ? AND author_id = ?`, cur.Slug, cur.Summary, coverID, s.stamp(), id, author); err != nil {
		if isUnique(err) {
			return Post{}, ErrSlugTaken
		}
		return Post{}, err
	}
	p, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	return p, tx.Commit()
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// PublishOriginal pins the head version and makes it the one readers see.
func (s *Service) PublishOriginal(ctx context.Context, author, id string) (Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	cur, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	if strings.TrimSpace(cur.Title) == "" || !ValidSlug(cur.Slug) {
		return Post{}, ErrInvalid
	}
	now := s.stamp()
	if _, err := tx.ExecContext(ctx, `UPDATE post_versions SET pinned = 1 WHERE post_id = ? AND version = ?`, id, cur.LatestVersion); err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = 'published', published_version = latest_version,
		published_at = CASE WHEN published_at = '' THEN ? ELSE published_at END, updated_at = ? WHERE id = ? AND author_id = ?`, now, now, id, author); err != nil {
		if isUnique(err) {
			return Post{}, ErrSlugTaken
		}
		return Post{}, err
	}
	p, err := loadPost(ctx, tx, author, id)
	if err != nil {
		return Post{}, err
	}
	return p, tx.Commit()
}

func (s *Service) Unpublish(ctx context.Context, author, id string) (Post, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE posts SET status = 'unpublished', published_version = 0, updated_at = ? WHERE id = ? AND author_id = ? AND status = 'published'`, s.stamp(), id, author)
	if err != nil {
		return Post{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := loadPost(ctx, s.db, author, id); err != nil {
			return Post{}, err
		}
		return Post{}, ErrNotPublished
	}
	return loadPost(ctx, s.db, author, id)
}

// Delete removes the post with its versions and images and frees its address.
func (s *Service) Delete(ctx context.Context, author, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM posts WHERE id = ? AND author_id = ?`, id, author).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM post_assets WHERE post_id = ?`, `DELETE FROM post_versions WHERE post_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE id = ? AND author_id = ?`, id, author); err != nil {
		return err
	}
	return tx.Commit()
}
