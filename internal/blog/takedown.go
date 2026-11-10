package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/store"
)

// An administrator's takedown of a published post (specs/004-admin-full-control). It is a mark on the post, not a status:
// the post keeps its published version, so a restore returns it exactly as it was, and the one filter every public read
// shares (publicWhere) hides it everywhere at once. The administrator sees a post's published content only, never a draft,
// an unpublished version, a check or anything about who reads it.

var (
	ErrAlreadyTakenDown = errors.New("blog: the post is already taken down")
	ErrNotTakenDown     = errors.New("blog: the post is not taken down")
	ErrBadReason        = errors.New("blog: a takedown needs a reason of 1 to 500 characters")
)

// TakenDownError is the author's refusal to publish while the mark stands; it carries the reason the author may see.
type TakenDownError struct{ Reason string }

func (e TakenDownError) Error() string        { return ErrTakenDownMsg }
func (e TakenDownError) Is(target error) bool { return target == ErrTakenDown }

const ErrTakenDownMsg = "blog: an administrator took the post down"

const maxReason = 500

// TakedownView is what the author sees: when, and the reason. Never who.
type TakedownView struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

func loadTakedown(ctx context.Context, q querier, id string) (*TakedownView, error) {
	var at, why string
	err := q.QueryRowContext(ctx, `SELECT takedown_at, takedown_reason FROM posts WHERE id = ?`, id).Scan(&at, &why)
	if err != nil {
		return nil, err
	}
	if at == "" {
		return nil, nil
	}
	return &TakedownView{At: at, Reason: why}, nil
}

// cleanReason trims the reason and checks it: 1 to 500 characters, no control characters but a line break or tab.
func cleanReason(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < 1 || n > maxReason || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", false
		}
	}
	return s, true
}

// AdminTakedown is the administrator's view of who took a post down and why.
type AdminTakedown struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
	ByName string `json:"byName"`
}

// AdminPost is one published post as an administrator sees it: metadata and the state, never a body, draft or reader field.
// State is visible, hidden_account (its author's account is not approved) or taken_down.
type AdminPost struct {
	ID          string         `json:"id"`
	Slug        string         `json:"slug"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	AuthorName  string         `json:"authorName"`
	PublishedAt string         `json:"publishedAt"`
	State       string         `json:"state"`
	Takedown    *AdminTakedown `json:"takedown,omitempty"`
}

// AdminPostView adds the published content: the version readers see, in the shape the public page renders.
type AdminPostView struct {
	AdminPost
	CoverURL string          `json:"coverUrl"`
	Doc      json.RawMessage `json:"doc,omitempty"`
	Blocks   []PublicBlock   `json:"blocks"`
}

// adminSelect lists what the administrator may see. A post is in it when it is published, or when it carries a takedown mark
// (an author may unpublish a post that was taken down; the mark must stay reachable so it can be restored). The title of a
// post that is no longer published is the one saved when it was taken down: no draft text is ever read.
const adminSelect = `SELECT p.id, p.slug, CASE WHEN p.published_version > 0 THEN v.title ELSE p.takedown_title END,
		CASE WHEN p.published_version > 0 THEN v.summary ELSE '' END, COALESCE(u.display_name, ''), p.published_at,
		u.approval_status, p.takedown_at, p.takedown_reason, COALESCE(t.display_name, t.email, '')
	FROM posts p JOIN users u ON u.id = p.author_id
	LEFT JOIN post_versions v ON v.post_id = p.id AND v.version = p.published_version
	LEFT JOIN users t ON t.id = NULLIF(p.takedown_by, '')
	WHERE ((p.status = 'published' AND p.published_version > 0) OR p.takedown_at <> '')`

func scanAdmin(sc interface{ Scan(...any) error }) (AdminPost, error) {
	var x AdminPost
	var approval, downAt, why, by string
	if err := sc.Scan(&x.ID, &x.Slug, &x.Title, &x.Summary, &x.AuthorName, &x.PublishedAt, &approval, &downAt, &why, &by); err != nil {
		return AdminPost{}, err
	}
	switch {
	case downAt != "":
		x.State, x.Takedown = "taken_down", &AdminTakedown{At: downAt, Reason: why, ByName: by}
	case approval != "APPROVED":
		x.State = "hidden_account"
	default:
		x.State = "visible"
	}
	return x, nil
}

const adminPageSize = 20

// AdminList returns one page of posts, newest published first. state is "", visible, hidden_account or taken_down; q matches
// the title, the summary and the author's name; after is the opaque cursor a page returned.
func (s *Service) AdminList(ctx context.Context, state, q, after string) ([]AdminPost, string, error) {
	where, args := "", []any{}
	switch state {
	case "visible":
		where = ` AND p.takedown_at = '' AND u.approval_status = 'APPROVED'`
	case "hidden_account":
		where = ` AND p.takedown_at = '' AND u.approval_status <> 'APPROVED'`
	case "taken_down":
		where = ` AND p.takedown_at <> ''`
	case "":
	default:
		return nil, "", ErrInvalid
	}
	if pat, ok := searchPattern(q); ok && pat != "" {
		where += ` AND ((CASE WHEN p.published_version > 0 THEN v.title ELSE p.takedown_title END) LIKE ? ESCAPE '\' OR v.summary LIKE ? ESCAPE '\' OR u.display_name LIKE ? ESCAPE '\')`
		args = append(args, pat, pat, pat)
	} else if strings.TrimSpace(q) != "" {
		return nil, "", ErrInvalid
	}
	if after != "" {
		at, id, ok := strings.Cut(after, "|")
		if !ok || !idRE.MatchString(id) {
			return nil, "", ErrInvalid
		}
		where += ` AND (p.published_at < ? OR (p.published_at = ? AND p.id > ?))`
		args = append(args, at, at, id)
	}
	rows, err := s.db.QueryContext(ctx, adminSelect+where+` ORDER BY p.published_at DESC, p.id LIMIT ?`, append(args, adminPageSize+1)...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []AdminPost{}
	for rows.Next() {
		x, err := scanAdmin(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > adminPageSize {
		out = out[:adminPageSize]
		last := out[len(out)-1]
		next = last.PublishedAt + "|" + last.ID
	}
	return out, next, nil
}

func adminOne(ctx context.Context, q querier, id string) (AdminPost, error) {
	x, err := scanAdmin(q.QueryRowContext(ctx, adminSelect+` AND p.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminPost{}, ErrNotFound
	}
	return x, err
}

// AdminGet is one post with its published content. A post that is no longer published (taken down, then unpublished by its
// author) has no content to show. Anything else that is not published is the same ErrNotFound.
func (s *Service) AdminGet(ctx context.Context, id string) (AdminPostView, error) {
	if !idRE.MatchString(id) {
		return AdminPostView{}, ErrNotFound
	}
	x, err := adminOne(ctx, s.db, id)
	if err != nil {
		return AdminPostView{}, err
	}
	v := AdminPostView{AdminPost: x, Blocks: []PublicBlock{}}
	var cover, doc string
	err = s.db.QueryRowContext(ctx, `SELECT p.cover_asset_id, v.doc FROM posts p JOIN post_versions v ON v.post_id = p.id AND v.version = p.published_version
		WHERE p.id = ? AND p.published_version > 0`, id).Scan(&cover, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return AdminPostView{}, err
	}
	v.Blocks, err = s.publicBlocks(ctx, id)
	if err != nil {
		return AdminPostView{}, err
	}
	// Images point at the author's route; the reader's address is the only one an administrator may use.
	doc = strings.ReplaceAll(doc, "/api/blog/posts/"+id+"/assets/", "/api/public/blog/posts/"+x.Slug+"/assets/")
	v.CoverURL, v.Doc = publicAssetURL(x.Slug, cover), json.RawMessage(doc)
	return v, nil
}

func (s *Service) auditPost(ctx context.Context, tx *sql.Tx, actor, action, postID, detail, reqID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, outcome, request_id, created_at, detail)
		VALUES (?,?,?,?,?,?,?,?,?)`, store.NewID(), actor, action, "post", postID, "OK", nullStr(reqID), s.stamp(), detail)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TakeDown hides a published post from everyone but its author and records who did it and why, in one transaction. A post
// that is not published (or already taken down) is refused and nothing changes.
func (s *Service) TakeDown(ctx context.Context, actor, id, reason, reqID string) (AdminPost, error) {
	reason, ok := cleanReason(reason)
	if !ok {
		return AdminPost{}, ErrBadReason
	}
	if !idRE.MatchString(id) {
		return AdminPost{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminPost{}, err
	}
	defer tx.Rollback()
	cur, err := adminOne(ctx, tx, id)
	if err != nil {
		return AdminPost{}, err
	}
	if cur.Takedown != nil {
		return AdminPost{}, ErrAlreadyTakenDown
	}
	res, err := tx.ExecContext(ctx, `UPDATE posts SET takedown_at = ?, takedown_by = ?, takedown_reason = ?, takedown_title = ?, takedown_seen_at = ''
		WHERE id = ? AND takedown_at = '' AND status = 'published' AND published_version > 0`, s.stamp(), actor, reason, cur.Title, id)
	if err != nil {
		return AdminPost{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AdminPost{}, ErrAlreadyTakenDown
	}
	if err := s.auditPost(ctx, tx, actor, "post.take_down", id, reason, reqID); err != nil {
		return AdminPost{}, err
	}
	out, err := adminOne(ctx, tx, id)
	if err != nil {
		return AdminPost{}, err
	}
	return out, tx.Commit()
}

// Restore removes the mark. The post returns as it was if it is still published; if its author unpublished it meanwhile it
// stays unpublished (note is true), and then it is no longer an administrator's concern.
func (s *Service) Restore(ctx context.Context, actor, id, reqID string) (post AdminPost, note bool, err error) {
	if !idRE.MatchString(id) {
		return AdminPost{}, false, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminPost{}, false, err
	}
	defer tx.Rollback()
	cur, err := adminOne(ctx, tx, id)
	if err != nil {
		return AdminPost{}, false, err
	}
	if cur.Takedown == nil {
		return AdminPost{}, false, ErrNotTakenDown
	}
	res, err := tx.ExecContext(ctx, `UPDATE posts SET takedown_at = '', takedown_by = '', takedown_reason = '', takedown_title = '', takedown_seen_at = ''
		WHERE id = ? AND takedown_at <> ''`, id)
	if err != nil {
		return AdminPost{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AdminPost{}, false, ErrNotTakenDown
	}
	if err := s.auditPost(ctx, tx, actor, "post.restore", id, "", reqID); err != nil {
		return AdminPost{}, false, err
	}
	var published int
	if err := tx.QueryRowContext(ctx, `SELECT published_version FROM posts WHERE id = ? AND status = 'published'`, id).Scan(&published); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AdminPost{}, false, err
	}
	if published == 0 {
		return AdminPost{}, true, tx.Commit()
	}
	out, err := adminOne(ctx, tx, id)
	if err != nil {
		return AdminPost{}, false, err
	}
	return out, false, tx.Commit()
}

// Notice is a takedown the author has not yet seen.
type Notice struct {
	PostID string `json:"postId"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

// Notices are the author's unseen takedowns, newest first, at most five.
func (s *Service) Notices(ctx context.Context, author string) ([]Notice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, takedown_title, takedown_reason, takedown_at FROM posts
		WHERE author_id = ? AND takedown_at <> '' AND takedown_seen_at = '' ORDER BY takedown_at DESC LIMIT 5`, author)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.PostID, &n.Title, &n.Reason, &n.At); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkSeen records that the author has seen a post's takedown notice. Another author's post is ErrNotFound.
func (s *Service) MarkSeen(ctx context.Context, author, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE posts SET takedown_seen_at = ? WHERE id = ? AND author_id = ? AND takedown_at <> ''`, s.stamp(), id, author)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
