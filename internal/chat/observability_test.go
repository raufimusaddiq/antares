package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// #114: one access log line per /api request with the PRD §13.2 fields, the route pattern instead of the raw path,
// and nothing from the query, headers or body.
func TestAccessLogCarriesTheFieldSetAndNoRequestContent(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	before := len(logBuf.String())
	if w := u.get("/api/chats/" + chat + "/search?q=PRIVATE-QUERY-91"); w.Code != 200 {
		t.Fatalf("search: %d", w.Code)
	}
	bob := a.user("bobby")
	if w := bob.get("/api/chats/" + chat); w.Code != 404 {
		t.Fatalf("foreign read: %d", w.Code)
	}
	got := logBuf.String()[before:]
	want := []string{`method=GET route="GET /api/chats/{chatId}/search" status=200`, "user=" + u.id, `route="GET /api/chats/{chatId}" status=404`, "error=NOT_FOUND", "user=" + bob.id}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("access log lacks %q:\n%s", w, got)
		}
	}
	for _, leak := range []string{"PRIVATE-QUERY-91", chat, u.cookie} {
		if strings.Contains(got, leak) {
			t.Errorf("access log carries %q:\n%s", leak, got)
		}
	}
}

// A failed run and a disconnected run each log their terminal state with the right category; an unknown outcome is
// never logged as success (PRD §24.3).
func TestRunStatesAreLoggedTruthfully(t *testing.T) {
	a := newApp(t, appOpts{backoff: fastBackoff})
	u := a.user("alice")
	before := len(logBuf.String())
	run := u.send(u.newChat(), "will fail")
	_, fr := a.hx.runWithInput(t, "will fail")
	s := u.open(run, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.failed", `{}`)
	s.waitFor(t, "run.failed")
	eventually(t, func() bool { return strings.Contains(logBuf.String()[before:], "run="+run) }, "the failed run's log line")
	line := logLine(logBuf.String()[before:], "run="+run)
	if !strings.Contains(line, "state=FAILED outcome=failure error=RUN_FAILED") || !strings.Contains(line, "user="+u.id) {
		t.Errorf("failed run line: %s", line)
	}

	before = len(logBuf.String())
	run2 := u.send(u.newChat(), "will vanish")
	_, fr2 := a.hx.runWithInput(t, "will vanish")
	recv(t, fr2.opens, "stream")
	a.hx.setStatus(fr2, "weird-unknown-state", "")
	a.hx.dropStream(fr2)
	eventually(t, func() bool { return u.runStatus(run2) == store.RunDisconnected }, "the run to be disconnected")
	eventually(t, func() bool { return logLine(logBuf.String()[before:], "run="+run2) != "" }, "the disconnected run's log line")
	line = logLine(logBuf.String()[before:], "run="+run2)
	if !strings.Contains(line, "state=DISCONNECTED outcome=unknown") || strings.Contains(line, "success") {
		t.Errorf("disconnected run line: %q", line)
	}
}

func logLine(log, needle string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, needle) && strings.Contains(l, " state=") {
			return l
		}
	}
	return ""
}

// The admin metrics are aggregates for an admin only.
func TestAdminMetricsAreAggregateAndAdminOnly(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	if w := u.get("/api/admin/metrics"); w.Code != 403 {
		t.Fatalf("non-admin: %d", w.Code)
	}
	admin := a.user("carol")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	u.newChat()
	time.Sleep(10 * time.Millisecond)
	w := admin.get("/api/admin/metrics")
	if w.Code != 200 {
		t.Fatalf("admin: %d %s", w.Code, w.Body.String())
	}
	var m struct {
		Database struct{ Accounts, Tenants map[string]int }
		Process  struct{ Denials int64 }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Database.Accounts["APPROVED"] < 2 || m.Process.Denials < 1 {
		t.Errorf("metrics: %s", w.Body.String())
	}
	for _, leak := range []string{u.id, admin.id, "alice", "example.test"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("metrics carry %q: %s", leak, w.Body.String())
		}
	}
}
