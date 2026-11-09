package blog

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// The AI check: one run of the author's own assistant (their own binding, chosen by the backend, never by the browser) over
// a pinned version of the draft. Antares only asks and validates; Hermes does the work (constitution II). A check has a
// Hermes session of its own and no Antares chat, so it appears in no list. Its state is kept here, not in the chats' run
// table, and a check that cannot be shown to have finished is never shown as finished.

//go:embed blog_prompt.txt
var checkPrompt string

const (
	checkTimeout     = 6 * time.Minute
	maxChecksPerUser = 2
	maxCheckInput    = 120 << 10
)

var (
	ErrNoCheck      = errors.New("blog: no check")
	ErrCheckRunning = errors.New("blog: a check is already running")
	ErrUnavailable  = errors.New("blog: checking is not available")
	ErrStale        = errors.New("blog: the draft changed after the check")
	ErrNeedsAck     = errors.New("blog: this block needs the author's acknowledgement")
	ErrNoReadyCheck = errors.New("blog: there is no finished check of this version")
)

// Runner is the part of the Hermes client a check uses.
type Runner interface {
	CreateSession(ctx context.Context, b hermes.Binding) (string, error)
	CreateRun(ctx context.Context, b hermes.Binding, r hermes.RunRequest) (string, error)
	StreamEvents(ctx context.Context, b hermes.Binding, runID string, fn func(hermes.Event) error) error
	GetRun(ctx context.Context, b hermes.Binding, runID string) (hermes.RunStatus, error)
	StopRun(ctx context.Context, b hermes.Binding, runID string) error
	Discover(ctx context.Context, b hermes.Binding) (hermes.Inventory, error)
}

// CheckView is a check as the author sees it.
type CheckView struct {
	ID      string      `json:"id"`
	State   string      `json:"state"`
	Reason  string      `json:"reason"`
	Notes   []Note      `json:"notes"`
	Notices []string    `json:"notices"`
	Blocks  []BlockView `json:"blocks"`
}

// BlockView is one proposed block.
type BlockView struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	AfterNode int      `json:"afterNode"`
	Markup    string   `json:"markup"`
	Fallback  string   `json:"fallback"`
	Decision  string   `json:"decision"`
	NeedsAck  bool     `json:"needsAck"`
	Sources   []Source `json:"sources"`
}

type liveCheck struct {
	postID string
	cancel context.CancelFunc
}

type checkRuntime struct {
	mu     sync.Mutex
	cancel map[string]liveCheck
	wg     sync.WaitGroup
	base   context.Context
	stop   context.CancelFunc
}

func (s *Service) initChecks() {
	ctx, cancel := context.WithCancel(context.Background())
	s.rt = &checkRuntime{cancel: map[string]liveCheck{}, base: ctx, stop: cancel}
	// A check that was running when the last process stopped has no worker now. It is not finished and not replayed.
	now := s.stamp()
	_, err := s.db.Exec(`UPDATE posts SET status = (SELECT prev_status FROM post_checks c WHERE c.post_id = posts.id AND c.state = 'running')
		WHERE status = 'checking' AND EXISTS (SELECT 1 FROM post_checks c WHERE c.post_id = posts.id AND c.state = 'running')`)
	if err == nil {
		_, err = s.db.Exec(`UPDATE post_checks SET state = 'failed', reason = 'disconnected', finished_at = ? WHERE state = 'running'`, now)
	}
	if err != nil {
		log.Printf("blog: recover checks: %v", err)
	}
}

// Close stops every running check's worker and waits for them. Their rows are left running and recovered on the next start.
func (s *Service) Close() {
	if s.rt == nil {
		return
	}
	s.rt.stop()
	s.rt.wg.Wait()
}

func (s *Service) checksConfigured() bool { return s.hx != nil && s.box != nil }

// binding is the author's own credential, read from the database now: no cache, and no account that is not eligible.
func (s *Service) binding(ctx context.Context, author string) (hermes.Binding, error) {
	if !s.checksConfigured() {
		return hermes.Binding{}, ErrUnavailable
	}
	cred, err := store.ActiveBinding(ctx, s.db, author)
	if err != nil {
		return hermes.Binding{}, ErrUnavailable
	}
	key, err := s.box.Open(cred.Ciphertext, cred.BindingID)
	if err != nil {
		return hermes.Binding{}, ErrUnavailable
	}
	return hermes.Binding{Profile: cred.Profile, Key: key}, nil
}

// Capabilities says what this author may do right now. Checking needs the assistant to be able to search the web, and it is
// reported available only when the assistant confirms that under the author's own binding: nothing is assumed.
//
// The probe is a call to Hermes, and the page asks on every load, so the answer is kept per author for a short while (longer
// when it says yes than when it says no, so a recovering assistant is noticed soon).
func (s *Service) Capabilities(ctx context.Context, author string) (writing, check, webSearch bool) {
	now := s.now()
	s.capMu.Lock()
	e, ok := s.capCache[author]
	s.capMu.Unlock()
	if ok && now.Before(e.until) {
		return true, e.web, e.web
	}
	web := s.probeWebSearch(ctx, author)
	ttl := 10 * time.Second
	if web {
		ttl = 45 * time.Second
	}
	s.capMu.Lock()
	if s.capCache == nil {
		s.capCache = map[string]capEntry{}
	}
	if len(s.capCache) > 5000 {
		s.capCache = map[string]capEntry{}
	}
	s.capCache[author] = capEntry{web: web, until: now.Add(ttl)}
	s.capMu.Unlock()
	return true, web, web
}

type capEntry struct {
	web   bool
	until time.Time
}

func (s *Service) probeWebSearch(ctx context.Context, author string) bool {
	b, err := s.binding(ctx, author)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	inv, err := s.hx.Discover(ctx, b)
	if err != nil {
		return false
	}
	for _, t := range inv.Toolsets {
		if t.Name == "web" && t.Status == hermes.StatusAvailable {
			return true
		}
	}
	return false
}

// stopStale ends the workers of a post's checks that an edit made out of date: their answer would be dropped anyway.
func (s *Service) stopStale(ctx context.Context, postID string) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM post_checks WHERE post_id = ? AND state = 'out_of_date' AND hermes_run != ''`, postID)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	for _, id := range ids {
		if l, ok := s.rt.cancel[id]; ok {
			l.cancel()
		}
	}
}

// ---- reading ----

func latestCheck(ctx context.Context, q querier, author, postID string) (*CheckView, error) {
	return readCheck(ctx, q, `SELECT id, state, reason, notes, notices FROM post_checks WHERE post_id = ? AND author_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1`, postID, author)
}

func readCheck(ctx context.Context, q querier, query string, args ...any) (*CheckView, error) {
	var c CheckView
	var notes, notices string
	err := q.QueryRowContext(ctx, query, args...).Scan(&c.ID, &c.State, &c.Reason, &notes, &notices)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoCheck
	}
	if err != nil {
		return nil, err
	}
	c.Notes, c.Notices, c.Blocks = []Note{}, []string{}, []BlockView{}
	_ = json.Unmarshal([]byte(notes), &c.Notes)
	_ = json.Unmarshal([]byte(notices), &c.Notices)
	if c.Notes == nil {
		c.Notes = []Note{}
	}
	if c.Notices == nil {
		c.Notices = []string{}
	}
	rows, err := q.QueryContext(ctx, `SELECT id, kind, anchor, markup, fallback, decision, needs_ack FROM post_blocks WHERE check_id = ? ORDER BY position`, c.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b BlockView
		var ack int
		if err := rows.Scan(&b.ID, &b.Kind, &b.AfterNode, &b.Markup, &b.Fallback, &b.Decision, &ack); err != nil {
			rows.Close()
			return nil, err
		}
		b.NeedsAck, b.Sources = ack == 1, []Source{}
		c.Blocks = append(c.Blocks, b)
	}
	rows.Close()
	for i := range c.Blocks {
		srows, err := q.QueryContext(ctx, `SELECT url, title, retrieved_at, verified FROM block_sources WHERE block_id = ? ORDER BY position`, c.Blocks[i].ID)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var s Source
			var v int
			if err := srows.Scan(&s.URL, &s.Title, &s.RetrievedAt, &v); err != nil {
				srows.Close()
				return nil, err
			}
			s.Verified = v == 1
			c.Blocks[i].Sources = append(c.Blocks[i].Sources, s)
		}
		srows.Close()
	}
	return &c, nil
}

// GetCheck is the post's latest check.
func (s *Service) GetCheck(ctx context.Context, author, postID string) (*CheckView, error) {
	if _, err := loadPost(ctx, s.db, author, postID); err != nil {
		return nil, err
	}
	return latestCheck(ctx, s.db, author, postID)
}

// ---- starting ----

// StartCheck pins the author's head version and starts a run over it.
func (s *Service) StartCheck(ctx context.Context, author, postID string) (*CheckView, error) {
	b, err := s.binding(ctx, author)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	cur, err := loadPost(ctx, tx, author, postID)
	if err != nil {
		return nil, err
	}
	input, topNodes, err := checkInput(cur.Title, cur.Doc)
	if err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_checks WHERE post_id = ? AND author_id = ? AND state = 'running'`, postID, author).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrCheckRunning
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_checks WHERE author_id = ? AND state = 'running'`, author).Scan(&n); err != nil {
		return nil, err
	}
	if n >= maxChecksPerUser {
		return nil, ErrTooMany
	}
	id, now := store.NewID(), s.stamp()
	if _, err := tx.ExecContext(ctx, `UPDATE post_versions SET pinned = 1 WHERE post_id = ? AND version = ?`, postID, cur.LatestVersion); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO post_checks (id, post_id, author_id, version, state, prev_status, started_at) VALUES (?,?,?,?, 'running', ?, ?)`,
		id, postID, author, cur.LatestVersion, cur.Status, now); err != nil {
		if isUnique(err) { // another start won the race for this post
			return nil, ErrCheckRunning
		}
		return nil, err
	}
	if cur.Status == "draft" || cur.Status == "unpublished" {
		if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = 'checking' WHERE id = ? AND author_id = ?`, postID, author); err != nil {
			return nil, err
		}
	}
	view, err := readCheck(ctx, tx, `SELECT id, state, reason, notes, notices FROM post_checks WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.rt.wg.Add(1)
	wctx, cancel := context.WithTimeout(s.rt.base, checkTimeout)
	s.rt.mu.Lock()
	s.rt.cancel[id] = liveCheck{postID: postID, cancel: cancel}
	s.rt.mu.Unlock()
	go func() {
		defer s.rt.wg.Done()
		defer func() {
			cancel()
			s.rt.mu.Lock()
			delete(s.rt.cancel, id)
			s.rt.mu.Unlock()
		}()
		s.runCheck(wctx, id, author, postID, input, topNodes, b)
	}()
	return view, nil
}

// checkInput numbers the draft's top-level parts so the assistant can point at them, and counts them.
func checkInput(title string, doc json.RawMessage) (string, int, error) {
	var root node
	if err := json.Unmarshal(doc, &root); err != nil {
		return "", 0, ErrInvalid
	}
	var b strings.Builder
	b.WriteString("TITLE: " + strings.TrimSpace(title) + "\n\n")
	words := 0
	for i, n := range root.Content {
		t := strings.TrimSpace(nodeText(n))
		words += len(strings.Fields(t))
		fmt.Fprintf(&b, "[%d] %s\n\n", i, t)
	}
	if words == 0 || len(root.Content) == 0 {
		return "", 0, ErrInvalid // nothing to check
	}
	if b.Len() > maxCheckInput {
		return "", 0, ErrInvalid
	}
	return b.String(), len(root.Content), nil
}

func nodeText(n node) string {
	switch n.Type {
	case "text":
		return n.Text
	case "hardBreak":
		return "\n"
	case "image":
		if alt, _ := n.Attrs["alt"].(string); alt != "" {
			return "(image: " + alt + ")"
		}
		return "(image)"
	case "horizontalRule":
		return "---"
	}
	var parts []string
	for _, c := range n.Content {
		parts = append(parts, nodeText(c))
	}
	sep := ""
	switch n.Type {
	case "bulletList", "orderedList", "blockquote", "listItem":
		sep = "\n"
	}
	t := strings.Join(parts, sep)
	switch n.Type {
	case "heading":
		return "## " + t
	case "listItem":
		return "- " + t
	}
	return t
}

// ---- running ----

var (
	errDone   = errors.New("done")
	errPrompt = errors.New("the run asked a question nobody can answer")
)

type outcome struct {
	state, reason string
	result        *Result
	// skip: the service is shutting down. Nothing is recorded: the row stays running and the next start marks it failed
	// (disconnected), which is the truth: it was not finished and the author did not cancel it.
	skip bool
}

func (s *Service) runCheck(ctx context.Context, id, author, postID, input string, topNodes int, b hermes.Binding) {
	out := s.driveRun(ctx, id, input, topNodes, b)
	if out.skip {
		return
	}
	if err := s.finishCheck(context.WithoutCancel(ctx), id, author, postID, out); err != nil {
		log.Printf("blog: finish check: %v", err)
	}
}

func (s *Service) driveRun(ctx context.Context, id, input string, topNodes int, b hermes.Binding) outcome {
	failed := func(reason string) outcome { return outcome{state: "failed", reason: reason} }
	stop := func(runID string) {
		if runID != "" {
			_ = s.hx.StopRun(context.WithoutCancel(ctx), b, runID)
		}
	}
	// ctx is cancelled for two reasons: the author cancelled (the row already says so, the answer is ignored) or the time ran out.
	ended := func(runID string) outcome {
		stop(runID)
		if s.rt.base.Err() != nil {
			return outcome{skip: true}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return outcome{state: "timed_out"}
		}
		return outcome{state: "cancelled"}
	}

	sid, err := s.hx.CreateSession(ctx, b)
	if err != nil {
		if ctx.Err() != nil {
			return ended("")
		}
		return failed("error")
	}
	req := hermes.RunRequest{SessionID: sid, Input: input, SessionKey: "chatui:blog:" + id, IdempotencyKey: "antares-blog-" + id, Instructions: checkPrompt}
	runID, err := s.hx.CreateRun(ctx, b, req)
	if err != nil {
		if ctx.Err() != nil {
			return ended("")
		}
		if hermes.CodeOf(err) == hermes.CodeUpstreamTimeout {
			return outcome{state: "timed_out"} // it may or may not have started
		}
		return failed("error")
	}
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE post_checks SET hermes_session = ?, hermes_run = ? WHERE id = ? AND state = 'running'`, sid, runID, id)

	var terminal *hermes.Normalized
	err = s.hx.StreamEvents(ctx, b, runID, func(ev hermes.Event) error {
		n := hermes.Normalize(ev)
		switch n.Kind {
		case hermes.KindApproval, hermes.KindClarify:
			return errPrompt
		case hermes.KindTerminal:
			terminal = &n
			return errDone
		}
		return nil
	})
	switch {
	case errors.Is(err, errPrompt):
		stop(runID)
		return failed("error")
	case ctx.Err() != nil:
		return ended(runID)
	}
	if terminal == nil {
		// The stream ended without saying how the run ended, which proves nothing: ask Hermes.
		if err != nil && hermes.CodeOf(err) == hermes.CodeUpstreamTimeout {
			stop(runID)
			return outcome{state: "timed_out"}
		}
		st, gerr := s.hx.GetRun(ctx, b, runID)
		if gerr != nil || !st.Terminal() {
			stop(runID)
			return failed("disconnected")
		}
		terminal = &hermes.Normalized{Kind: hermes.KindTerminal, State: st.State, Output: st.Output, Intr: st.Interrupted}
	}
	switch terminal.State {
	case hermes.StateCompleted:
	case hermes.StateCancelled:
		return outcome{state: "cancelled"}
	default:
		return failed("error")
	}
	output := terminal.Output
	if output == "" {
		if st, gerr := s.hx.GetRun(ctx, b, runID); gerr == nil {
			output = st.Output
		}
	}
	res, err := ParseEnvelope([]byte(output), topNodes, s.now())
	if err != nil {
		return failed("unusable")
	}
	return outcome{state: "ready", result: &res}
}

// finishCheck records the outcome, but only if the check is still the running one: an author who cancelled, or a draft that
// changed, has already decided and the late answer is dropped.
func (s *Service) finishCheck(ctx context.Context, id, author, postID string, out outcome) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.stamp()
	var notes, notices []byte = []byte("[]"), []byte("[]")
	if out.result != nil {
		notes, _ = json.Marshal(out.result.Notes)
		notices, _ = json.Marshal(out.result.Notices)
	}
	res, err := tx.ExecContext(ctx, `UPDATE post_checks SET state = ?, reason = ?, notes = ?, notices = ?, finished_at = ? WHERE id = ? AND author_id = ? AND state = 'running'`,
		out.state, out.reason, string(notes), string(notices), now, id, author)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if out.result != nil {
		for i, bl := range out.result.Blocks {
			bid := store.NewID()
			ack := 0
			if bl.NeedsAck {
				ack = 1
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO post_blocks (id, check_id, post_id, author_id, position, anchor, kind, markup, fallback, needs_ack) VALUES (?,?,?,?,?,?,?,?,?,?)`,
				bid, id, postID, author, i, bl.AfterNode, bl.Kind, bl.Markup, bl.Fallback, ack); err != nil {
				return err
			}
			for j, src := range bl.Sources {
				if _, err := tx.ExecContext(ctx, `INSERT INTO block_sources (block_id, position, url, title, retrieved_at, verified) VALUES (?,?,?,?,?,0)`, bid, j, src.URL, src.Title, src.RetrievedAt); err != nil {
					return err
				}
			}
		}
	}
	// A draft or unpublished post shows the check's progress as its status; a failed check puts it back as it was.
	if out.state == "ready" {
		_, err = tx.ExecContext(ctx, `UPDATE posts SET status = 'ready' WHERE id = ? AND author_id = ? AND status = 'checking'`, postID, author)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE posts SET status = (SELECT prev_status FROM post_checks WHERE id = ?) WHERE id = ? AND author_id = ? AND status = 'checking'`, id, postID, author)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// outOfDate is called inside a save: a check of an older version, running or finished, no longer describes the draft.
func outOfDate(ctx context.Context, tx *sql.Tx, author, postID, now string) error {
	res, err := tx.ExecContext(ctx, `UPDATE post_checks SET state = 'out_of_date', finished_at = ? WHERE post_id = ? AND author_id = ? AND state IN ('running','ready')
		AND version < (SELECT latest_version FROM posts WHERE id = ?)`, now, postID, author, postID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE posts SET status = COALESCE((SELECT prev_status FROM post_checks WHERE post_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1), 'draft')
			WHERE id = ? AND author_id = ? AND status IN ('checking','ready')`, postID, postID, author)
	}
	return err
}

// ---- cancelling and deciding ----

// CancelCheck stops the post's running check. The row says cancelled at once; the run is stopped as well, best effort.
func (s *Service) CancelCheck(ctx context.Context, author, postID string) (*CheckView, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := loadPost(ctx, tx, author, postID); err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM post_checks WHERE post_id = ? AND author_id = ? AND state = 'running'`, postID, author).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoCheck
	} else if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE post_checks SET state = 'cancelled', finished_at = ? WHERE id = ? AND state = 'running'`, s.stamp(), id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = (SELECT prev_status FROM post_checks WHERE id = ?) WHERE id = ? AND author_id = ? AND status = 'checking'`, id, postID, author); err != nil {
		return nil, err
	}
	view, err := readCheck(ctx, tx, `SELECT id, state, reason, notes, notices FROM post_checks WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.rt.mu.Lock()
	live, ok := s.rt.cancel[id]
	s.rt.mu.Unlock()
	if ok {
		live.cancel() // the worker stops the upstream run
	}
	return view, nil
}

// DecideBlock records the author's choice on one block of the current, finished check.
func (s *Service) DecideBlock(ctx context.Context, author, postID, blockID, decision string, ack bool) (*BlockView, error) {
	if decision != "accepted" && decision != "rejected" {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	cur, err := loadPost(ctx, tx, author, postID)
	if err != nil {
		return nil, err
	}
	var state, checkID string
	var version, needsAck int
	err = tx.QueryRowContext(ctx, `SELECT c.id, c.state, c.version, b.needs_ack FROM post_blocks b JOIN post_checks c ON c.id = b.check_id
		WHERE b.id = ? AND b.post_id = ? AND b.author_id = ? AND c.author_id = ?`, blockID, postID, author, author).Scan(&checkID, &state, &version, &needsAck)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if state != "ready" || version != cur.LatestVersion {
		return nil, ErrStale
	}
	// Only the post's latest check can be decided on: what the author sees, and what is published, is that one.
	var latestID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM post_checks WHERE post_id = ? AND author_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1`, postID, author).Scan(&latestID); err != nil {
		return nil, err
	}
	if latestID != checkID {
		return nil, ErrStale
	}
	if decision == "accepted" && needsAck == 1 && !ack {
		return nil, ErrNeedsAck
	}
	if _, err := tx.ExecContext(ctx, `UPDATE post_blocks SET decision = ? WHERE id = ? AND author_id = ?`, decision, blockID, author); err != nil {
		return nil, err
	}
	view, err := latestCheck(ctx, tx, author, postID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i := range view.Blocks {
		if view.Blocks[i].ID == blockID {
			return &view.Blocks[i], nil
		}
	}
	return nil, ErrNotFound
}

// PublishEnriched publishes the head version with the blocks the author accepted. It needs a finished check of exactly that
// version; a block still undecided is left out, never published by default.
func (s *Service) PublishEnriched(ctx context.Context, author, postID string) (Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	cur, err := loadPost(ctx, tx, author, postID)
	if err != nil {
		return Post{}, err
	}
	if strings.TrimSpace(cur.Title) == "" || !ValidSlug(cur.Slug) {
		return Post{}, ErrInvalid
	}
	var checkID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM post_checks WHERE post_id = ? AND author_id = ? AND version = ? AND state = 'ready' ORDER BY started_at DESC, rowid DESC LIMIT 1`,
		postID, author, cur.LatestVersion).Scan(&checkID)
	if errors.Is(err, sql.ErrNoRows) {
		return Post{}, ErrNoReadyCheck
	}
	if err != nil {
		return Post{}, err
	}
	now := s.stamp()
	if _, err := tx.ExecContext(ctx, `UPDATE post_versions SET pinned = (version = ?) WHERE post_id = ?`, cur.LatestVersion, postID); err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = 'published', published_version = latest_version,
		published_at = CASE WHEN published_at = '' THEN ? ELSE published_at END, updated_at = ? WHERE id = ? AND author_id = ?`, now, now, postID, author); err != nil {
		if isUnique(err) {
			return Post{}, ErrSlugTaken
		}
		return Post{}, err
	}
	for _, q := range []string{`DELETE FROM published_block_sources WHERE post_id = ?`, `DELETE FROM published_blocks WHERE post_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, postID); err != nil {
			return Post{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, anchor, kind, markup, fallback FROM post_blocks WHERE check_id = ? AND post_id = ? AND author_id = ? AND decision = 'accepted' ORDER BY position`, checkID, postID, author)
	if err != nil {
		return Post{}, err
	}
	type acc struct {
		id, kind, markup, fallback string
		anchor                     int
	}
	var accepted []acc
	for rows.Next() {
		var a acc
		if err := rows.Scan(&a.id, &a.anchor, &a.kind, &a.markup, &a.fallback); err != nil {
			rows.Close()
			return Post{}, err
		}
		accepted = append(accepted, a)
	}
	rows.Close()
	for i, a := range accepted {
		if _, err := tx.ExecContext(ctx, `INSERT INTO published_blocks (post_id, version, position, anchor, kind, markup, fallback) VALUES (?,?,?,?,?,?,?)`,
			postID, cur.LatestVersion, i, a.anchor, a.kind, a.markup, a.fallback); err != nil {
			return Post{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO published_block_sources (post_id, version, block, position, url, title, retrieved_at)
			SELECT ?, ?, ?, position, url, title, retrieved_at FROM block_sources WHERE block_id = ?`, postID, cur.LatestVersion, i, a.id); err != nil {
			return Post{}, err
		}
	}
	p, err := loadPost(ctx, tx, author, postID)
	if err != nil {
		return Post{}, err
	}
	return p, tx.Commit()
}
