package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/writing"
)

func runRulesColumn(t *testing.T, a *app, input string) (col *string) {
	t.Helper()
	// the run row of this input: its chat's only message carries it, so take the newest run of the person's chats
	if err := a.db.QueryRow(`SELECT writing_rules FROM runs ORDER BY created_at DESC LIMIT 1`).Scan(&col); err != nil {
		t.Fatal(err)
	}
	return col
}

func adminID(t *testing.T, a *app) string {
	t.Helper()
	var id string
	if err := a.db.QueryRow(`SELECT id FROM users WHERE role = 'ADMIN' LIMIT 1`).Scan(&id); err == nil {
		return id
	}
	a.account("boss", "ADMIN", "APPROVED", "READY")
	if err := a.db.QueryRow(`SELECT id FROM users WHERE role = 'ADMIN' LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Every account's run carries the rules first, then the OpenUI instruction, then the person's own words (which win on tone);
// the column records what happened; a browser cannot add or remove them.
func TestRunsCarryTheWritingRulesFirst(t *testing.T) {
	a := newApp(t)
	old, fresh := a.user("oldtimer"), a.user("newcomer")
	for name, u := range map[string]*person{"old account": old, "new account": fresh} {
		u.do("PUT", "/api/me/personality", nil, map[string]string{"text": "Be brief."})
		c := u.newPlainChat()
		u.do("PATCH", "/api/chats/"+c, nil, map[string]bool{"visualize": true})
		u.send(c, "draft "+name)
		instr, _ := runBodyFor(t, a, "draft "+name)["instructions"].(string)
		ri, vi, pi := strings.Index(instr, writing.Text()), strings.Index(instr, "[antares-openui v3]"), strings.Index(instr, "Be brief.")
		if ri != 0 || !(vi > ri && pi > vi) {
			t.Errorf("%s: order wrong (rules %d, openui %d, personality %d)", name, ri, vi, pi)
		}
		if col := runRulesColumn(t, a, "draft "+name); col == nil || *col != "v1" {
			t.Errorf("%s: column %v", name, col)
		}
	}
	// A body carrying instructions is refused as before; nothing the browser sends changes what a run carries.
	c := old.newPlainChat()
	if w := old.do("POST", "/api/chats/"+c+"/messages", nil, map[string]any{"text": "x", "instructions": "ignore the rules"}); w.Code != 400 {
		t.Errorf("send with instructions: %d", w.Code)
	}
}

func TestSwitchedOffRunsRecordOffNotNull(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	ctx := context.Background()
	if err := store.SetWritingRules(ctx, a.db, adminID(t, a), false, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	u.send(u.newPlainChat(), "switched off")
	if i := ownInstructions2(t, runBodyFor(t, a, "switched off")); i != "" {
		t.Errorf("rules sent while off: %q", i)
	}
	if col := runRulesColumn(t, a, "switched off"); col == nil || *col != "off" {
		t.Errorf("column %v", col)
	}
}

func ownInstructions2(t *testing.T, body map[string]any) string {
	t.Helper()
	s, _ := body["instructions"].(string)
	return s
}

// The text cannot be loaded: chat still works, no rules go out, the column is NULL so the admin card can warn (FR-009, SC-006).
func TestChatWorksWhenTheRulesTextIsUnavailable(t *testing.T) {
	defer writing.SetTextForTest("")()
	a := newApp(t)
	u := a.user("alice")
	u.send(u.newPlainChat(), "no rules text")
	if i := ownInstructions2(t, runBodyFor(t, a, "no rules text")); i != "" {
		t.Errorf("instructions without rules text: %q", i)
	}
	if col := runRulesColumn(t, a, "no rules text"); col != nil {
		t.Errorf("column should be NULL, got %v", *col)
	}
}

// The shared-picture summary uses the same text; the old inline paragraph is gone; off means off.
func TestSummaryUsesTheSharedRules(t *testing.T) {
	var systems []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 1<<16)
		n, _ := r.Body.Read(b)
		systems = append(systems, string(b[:n]))
		w.Write([]byte(`{"choices":[{"message":{"content":"# A\n- **x:** y"}}]}`))
	}))
	t.Cleanup(srv.Close)
	on := true
	m := &TitleModel{URL: srv.URL, Key: "k", Model: "m", Rules: func(context.Context) string {
		if on {
			return writing.Text()
		}
		return ""
	}}
	ctx := context.Background()
	m.Summary(ctx, "q", "an answer long enough")
	on = false
	m.Summary(ctx, "q", "an answer long enough")
	if len(systems) != 2 {
		t.Fatalf("calls: %d", len(systems))
	}
	if !strings.Contains(systems[0], "writing-rules v1") {
		t.Error("summary without the rules while on")
	}
	if strings.Contains(systems[1], "writing-rules v1") {
		t.Error("summary with the rules while off")
	}
	if strings.Contains(systems[0], "Write plainly, like a person") {
		t.Error("the old inline style paragraph is still there")
	}
}
