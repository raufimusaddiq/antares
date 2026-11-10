package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/modelpolicy"
	"github.com/raufimusaddiq/antares/internal/store"
)

// The administrator's default model for chat (specs/004-admin-full-control): chats with no model of their own follow it at their
// next message, a chat with its own keeps it, and a default that cannot run falls back to the profile default without failing.

func setDefault(t *testing.T, a *app, purpose, model string) {
	t.Helper()
	var admin string
	if err := a.db.QueryRow(`SELECT id FROM users WHERE role = 'ADMIN' AND approval_status = 'APPROVED' LIMIT 1`).Scan(&admin); err != nil {
		admin = a.account("boss", "ADMIN", "APPROVED", "READY").id
	}
	if err := store.SetModelDefaults(context.Background(), a.db, admin, map[string]string{purpose: model}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func (a *app) lastRunModel(t *testing.T, n int) any {
	t.Helper()
	eventually(t, func() bool { return len(a.runPosts(t)) >= n }, "the run request")
	return a.runPosts(t)[n-1]["model"]
}

func TestANewChatWithNoModelOfItsOwnUsesTheAdministratorsChatDefault(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	setDefault(t, a, "chat", kenariOnly)
	c := u.newChat()
	u.send(c, "hello")
	if got := a.lastRunModel(t, 1); got != kenariOnly {
		t.Fatalf("run named %v, want the administrator's default %s", got, kenariOnly)
	}
	body := a.runPosts(t)[0]
	if body["provider"] != "custom" {
		t.Errorf("provider %v: the server picks it from the caller's own profile", body["provider"])
	}
}

func TestAChatWithNoSavedModelFollowsAChangedDefaultAtItsNextMessageButAChatsOwnChoiceStays(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	setDefault(t, a, "chat", kenariOnly)
	plain, own := u.newChat(), u.newChat()
	if code, _ := u.putSettings(own, map[string]any{"model": kenariSixLevels}); code != 200 {
		t.Fatal("setup")
	}
	u.send(plain, "one")
	if got := a.lastRunModel(t, 1); got != kenariOnly {
		t.Fatalf("first message: %v", got)
	}
	if _, err := a.db.Exec(`UPDATE runs SET status = 'COMPLETED' WHERE chat_id = ?`, plain); err != nil { // the first reply is over
		t.Fatal(err)
	}
	setDefault(t, a, "chat", kenariNoLevels)
	u.send(plain, "two")
	if got := a.lastRunModel(t, 2); got != kenariNoLevels {
		t.Errorf("an existing chat did not follow the changed default: %v", got)
	}
	u.send(own, "three")
	if got := a.lastRunModel(t, 3); got != kenariSixLevels {
		t.Errorf("a chat's own model was replaced by the default: %v", got)
	}
}

func TestADefaultThatCannotRunFallsBackToTheProfileDefaultAndTheSendStillWorks(t *testing.T) {
	for name, model := range map[string]string{"an image model": kenariImageOnly, "a model Kenari does not list": "nope-model"} {
		a := mediaApp(t, visionModel)
		u := a.user("a")
		setDefault(t, a, "chat", model)
		c := u.newChat()
		w := u.do("POST", "/api/chats/"+c+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hello"})
		if w.Code != 200 && w.Code != 201 && w.Code != 202 {
			t.Fatalf("%s: the send was refused: %d %s", name, w.Code, w.Body)
		}
		eventually(t, func() bool { return len(a.runPosts(t)) == 1 }, "the run")
		if got := a.runPosts(t)[0]["model"]; got == model {
			t.Errorf("%s: the unusable default was sent", name)
		}
	}
}

func TestASwitchedOffChatDefaultIsNeverSent(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	setDefault(t, a, "chat", kenariOnly)
	var admin string
	_ = a.db.QueryRow(`SELECT id FROM users WHERE role = 'ADMIN' LIMIT 1`).Scan(&admin)
	if err := store.SetBlockedModels(context.Background(), a.db, admin, []string{kenariOnly}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	c := u.newChat()
	u.send(c, "hello")
	eventually(t, func() bool { return len(a.runPosts(t)) == 1 }, "the run")
	if got := a.runPosts(t)[0]["model"]; got == kenariOnly {
		t.Fatalf("a switched-off model was sent: %v", got)
	}
}

// Titles and summaries: the helper takes the administrator's default when it can, else its configured model, and makes no call at all when
// the model it would use is switched off.
func TestTitlesAndSummariesFollowTheirDefaultAndRespectTheSwitch(t *testing.T) {
	a := mediaApp(t, visionModel)
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 1<<14)
		n, _ := r.Body.Read(b)
		body := string(b[:n])
		i := strings.Index(body, `"model":"`)
		models = append(models, strings.SplitN(body[i+9:], `"`, 2)[0])
		w.Write([]byte(`{"choices":[{"message":{"content":"A fine title"}}]}`))
	}))
	t.Cleanup(srv.Close)
	m := &TitleModel{URL: srv.URL, Key: "k", Model: "configured-small",
		Pick: func(ctx context.Context, configured string) (string, bool) {
			return modelpolicy.Pick(ctx, a.db, a.svc.kenari, "summary", configured, time.Now())
		}}
	ctx := context.Background()
	if _, err := m.Title(ctx, "hello"); err != nil || len(models) != 1 || models[0] != "configured-small" {
		t.Fatalf("no default saved: %v %v", err, models)
	}
	setDefault(t, a, "summary", kenariOnly)
	if _, err := m.Title(ctx, "hello"); err != nil || models[len(models)-1] != kenariOnly {
		t.Fatalf("default saved: %v %v", err, models)
	}
	if _, err := m.Summary(ctx, "q", "a long enough answer to summarize"); err != nil && !strings.Contains(err.Error(), "unreadable") {
		t.Fatal(err)
	}
	if models[len(models)-1] != kenariOnly {
		t.Errorf("the summary did not use the default: %v", models)
	}
	var admin string
	_ = a.db.QueryRow(`SELECT id FROM users WHERE role = 'ADMIN' LIMIT 1`).Scan(&admin)
	// The configured model switched off, with no usable default: no call.
	if err := store.SetModelDefaults(ctx, a.db, admin, map[string]string{"summary": ""}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBlockedModels(ctx, a.db, admin, []string{"configured-small"}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	before := len(models)
	if _, err := m.Title(ctx, "hello"); err == nil || len(models) != before {
		t.Errorf("a switched-off helper model was called: %v %v", err, models)
	}
}
