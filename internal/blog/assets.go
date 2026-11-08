package blog

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/raufimusaddiq/antares/internal/store"
)

const (
	maxAssetBytes    = 5 << 20
	maxAssetsPerPost = 30
	// maxAssetBytesPerAuthor bounds everything one account may keep as post images, across all its posts.
	maxAssetBytesPerAuthor = 200 << 20
)

// assetBytes is what the author's posts already hold in images.
func assetBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, author string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(bytes)), 0) FROM post_assets WHERE author_id = ?`, author).Scan(&n)
	return n, err
}

// The only image types a post may carry. The type is read from the bytes, never from what the browser said.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

var ErrNotImage = errors.New("blog: not a supported image")

// AddAsset stores an image for the author's own post and returns its id.
func (s *Service) AddAsset(ctx context.Context, author, postID string, data []byte) (string, error) {
	ct := http.DetectContentType(data)
	if !imageTypes[ct] {
		return "", ErrNotImage
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM posts WHERE id = ? AND author_id = ?`, postID, author).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_assets WHERE post_id = ?`, postID).Scan(&n); err != nil {
		return "", err
	}
	if n >= maxAssetsPerPost {
		return "", ErrTooMany
	}
	used, err := assetBytes(ctx, tx, author)
	if err != nil {
		return "", err
	}
	if used+int64(len(data)) > maxAssetBytesPerAuthor {
		return "", ErrTooMany
	}
	id := store.NewID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO post_assets (id, post_id, author_id, content_type, bytes, created_at) VALUES (?,?,?,?,?,?)`, id, postID, author, ct, data, s.stamp()); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// Asset is an image and its type.
type Asset struct {
	Type string
	Data []byte
}

// OwnAsset reads an image of the author's own post (drafts included).
func (s *Service) OwnAsset(ctx context.Context, author, postID, assetID string) (Asset, error) {
	var a Asset
	err := s.db.QueryRowContext(ctx, `SELECT a.content_type, a.bytes FROM post_assets a JOIN posts p ON p.id = a.post_id
		WHERE a.id = ? AND a.post_id = ? AND p.author_id = ? AND a.author_id = ?`, assetID, postID, author, author).Scan(&a.Type, &a.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	return a, err
}

// Duplicate copies the author's post as a new draft, images included, so the copy does not depend on the original.
func (s *Service) Duplicate(ctx context.Context, author, id string) (Post, error) {
	src, err := loadPost(ctx, s.db, author, id)
	if err != nil {
		return Post{}, err
	}
	title := src.Title
	if title != "" {
		if r := []rune(title); len(r) > maxTitle-7 {
			title = string(r[:maxTitle-7])
		}
		title += " (copy)"
	}
	var copyBytes int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(bytes)), 0) FROM post_assets WHERE post_id = ? AND author_id = ?`, id, author).Scan(&copyBytes)
	if used, err := assetBytes(ctx, s.db, author); err != nil {
		return Post{}, err
	} else if used+copyBytes > maxAssetBytesPerAuthor {
		return Post{}, ErrTooMany
	}
	cp, err := s.insertPost(ctx, author, title, src.Summary, nil2empty(src.Doc))
	if err != nil {
		return Post{}, err
	}
	done := false
	defer func() {
		if !done { // never leave a half-made copy behind
			_ = s.Delete(context.WithoutCancel(ctx), author, cp.ID)
		}
	}()
	doc := string(src.Doc)
	// One image at a time: a post can hold 150 MiB of them, and none of that needs to be in memory at once.
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM post_assets WHERE post_id = ? AND author_id = ?`, id, author)
	if err != nil {
		return Post{}, err
	}
	var ids []string
	for rows.Next() {
		var oid string
		if err := rows.Scan(&oid); err != nil {
			rows.Close()
			return Post{}, err
		}
		ids = append(ids, oid)
	}
	rows.Close()
	cover := ""
	for _, oid := range ids {
		nid := store.NewID()
		if _, err := s.db.ExecContext(ctx, `INSERT INTO post_assets (id, post_id, author_id, content_type, bytes, created_at)
			SELECT ?, ?, author_id, content_type, bytes, ? FROM post_assets WHERE id = ? AND post_id = ? AND author_id = ?`, nid, cp.ID, s.stamp(), oid, id, author); err != nil {
			return Post{}, err
		}
		doc = strings.ReplaceAll(doc, assetURL(id, oid), assetURL(cp.ID, nid))
		if assetURL(id, oid) == src.CoverURL {
			cover = nid
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE post_versions SET doc = ? WHERE post_id = ? AND version = 1`, doc, cp.ID); err != nil {
		return Post{}, err
	}
	if cover != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE posts SET cover_asset_id = ? WHERE id = ? AND author_id = ?`, cover, cp.ID, author); err != nil {
			return Post{}, err
		}
	}
	p, err := loadPost(ctx, s.db, author, cp.ID)
	done = err == nil
	return p, err
}

func nil2empty(b []byte) []byte {
	if len(b) == 0 {
		return []byte(emptyDoc)
	}
	return b
}
