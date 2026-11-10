package blog

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// The progress of a check (specs/006-ai-editor-blog, US2): the step is read from the events of the run, never estimated.

type stepScript struct {
	next chan func(emit func(string, map[string]any) error) error
}

func newStepScript() (*stepScript, func(ctx context.Context, emit func(string, map[string]any) error) error) {
	s := &stepScript{next: make(chan func(emit func(string, map[string]any) error) error, 64)}
	return s, func(ctx context.Context, emit func(string, map[string]any) error) error {
		for {
			select {
			case f := <-s.next:
				if err := f(emit); err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (s *stepScript) emit(name string, data map[string]any) {
	s.next <- func(emit func(string, map[string]any) error) error { return emit(name, data) }
}

func (u *person) progress(postID string) *Progress {
	u.a.t.Helper()
	w := u.do("GET", "/api/blog/posts/"+postID+"/check", nil)
	if w.Code != 200 {
		u.a.t.Fatalf("GET check: %d %s", w.Code, w.Body)
	}
	return into[CheckView](u.a.t, w).Progress
}

func (u *person) waitStage(postID, stage string, lookups int) *Progress {
	u.a.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		p := u.progress(postID)
		if p != nil && p.Stage == stage && p.Lookups == lookups {
			return p
		}
		if time.Now().After(deadline) {
			u.a.t.Fatalf("progress %+v, wanted %s with %d lookups", p, stage, lookups)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func TestAFreshCheckShowsItsStepFromTheEventsOfTheRun(t *testing.T) {
	fh := newFake()
	st, script := newStepScript()
	fh.script = script
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Steps")
	started := u.startCheck(post)
	if started.Progress == nil || started.Progress.Stage != "reading" || started.Progress.StageSince != started.Progress.StartedAt || started.Progress.Now == "" {
		t.Fatalf("a check starts at reading, with the server's time: %+v", started.Progress)
	}
	st.emit("reasoning.available", map[string]any{})
	time.Sleep(60 * time.Millisecond)
	if p := u.progress(post.ID); p.Stage != "reading" || p.Lookups != 0 {
		t.Fatalf("thinking is still reading: %+v", p)
	}
	st.emit("tool.started", map[string]any{"tool": "web_search", "tool_call_id": "c1"})
	p := u.waitStage(post.ID, "searching", 1)
	if p.StageSince == started.Progress.StartedAt {
		t.Error("the stage began later than the check")
	}
	st.emit("tool.completed", map[string]any{"tool": "web_search", "tool_call_id": "c1"})
	st.emit("tool.started", map[string]any{"tool": "web_extract", "tool_call_id": "c2"})
	u.waitStage(post.ID, "searching", 2)
	st.emit("assistant.delta", map[string]any{"delta": "{"})
	w := u.waitStage(post.ID, "writing", 2)
	if w.LastEventAt < w.StageSince {
		t.Errorf("the last event %s is before the stage began %s", w.LastEventAt, w.StageSince)
	}
	// A later lookup is a step back to searching: the screen shows what is happening now.
	st.emit("tool.started", map[string]any{"tool": "web_search", "tool_call_id": "c3"})
	u.waitStage(post.ID, "searching", 3)
	st.emit("run.completed", map[string]any{"output": goodReply})
	c := u.waitCheck(post.ID, "ready")
	if c.Progress == nil || c.Progress.StartedAt == "" {
		t.Errorf("a finished check keeps its progress for the elapsed time: %+v", c.Progress)
	}
}

// A burst of events does not turn into a burst of writes: the sign of life is written at most once every two seconds apart from a step change.
func TestSignsOfLifeAreWrittenAtMostOnceEveryTwoSeconds(t *testing.T) {
	fh := newFake()
	st, script := newStepScript()
	fh.script = script
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	post := u.draftToCheck("Burst")
	u.startCheck(post)
	st.emit("assistant.delta", map[string]any{"delta": "a"})
	first := u.waitStage(post.ID, "writing", 0)
	for i := 0; i < 50; i++ {
		st.emit("assistant.delta", map[string]any{"delta": "b"})
	}
	time.Sleep(250 * time.Millisecond)
	if p := u.progress(post.ID); p.LastEventAt != first.LastEventAt {
		t.Errorf("fifty deltas inside two seconds rewrote the sign of life: %s then %s", first.LastEventAt, p.LastEventAt)
	}
	st.emit("run.completed", map[string]any{"output": goodReply})
	u.waitCheck(post.ID, "ready")
}

func TestACancelledCheckKeepsItsElapsedTimeAndAnotherAuthorCannotReadIt(t *testing.T) {
	fh := newFake()
	st, script := newStepScript()
	fh.script = script
	a := newAppWithHermes(t, fh)
	u, other := a.active("alice"), a.active("bobby")
	post := u.draftToCheck("Cancelled")
	started := u.startCheck(post)
	st.emit("tool.started", map[string]any{"tool": "web_search", "tool_call_id": "c1"})
	u.waitStage(post.ID, "searching", 1)
	if w := other.do("GET", "/api/blog/posts/"+post.ID+"/check", nil); w.Code != 404 {
		t.Fatalf("another author reads the progress: %d", w.Code)
	}
	if w := u.do("POST", "/api/blog/posts/"+post.ID+"/check/cancel", nil); w.Code != 200 {
		t.Fatalf("cancel: %d", w.Code)
	}
	c := u.waitCheck(post.ID, "cancelled")
	if c.Progress == nil || c.Progress.StartedAt != started.Progress.StartedAt || c.Progress.Lookups != 1 {
		t.Errorf("a cancelled check keeps what it had: %+v", c.Progress)
	}
}

func TestTheMigrationGivesEveryExistingCheckTheReadingStage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "antares.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	all := store.Migrations()
	before := fstest.MapFS{}
	ents, _ := fs.ReadDir(all, ".")
	for _, e := range ents {
		if e.Name() < "0030" {
			b, _ := fs.ReadFile(all, e.Name())
			before[e.Name()] = &fstest.MapFile{Data: b}
		}
	}
	if _, err := store.Migrate(ctx, db, before); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO post_checks (id, post_id, author_id, version, state, prev_status, started_at) VALUES ('c1','p1','u1',1,'ready','draft','2026-01-01T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, all); err != nil {
		t.Fatal(err)
	}
	var stage string
	var lookups int
	if err := conn.QueryRowContext(ctx, `SELECT stage, lookups FROM post_checks WHERE id = 'c1'`).Scan(&stage, &lookups); err != nil || stage != "reading" || lookups != 0 {
		t.Fatalf("%v %q %d", err, stage, lookups)
	}
}
