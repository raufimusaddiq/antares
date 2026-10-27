package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/obs"
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

// The usage line carries counts only: the model is an upstream string, so text Hermes put there never reaches the log.
func TestUsageLogCarriesNoUpstreamText(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	before := len(logBuf.String())
	frame := strings.Replace(usageFrame, "deepseek-v4-1-flash", "PRIVATE-PROMPT-77 password hunter2", 1)
	run := runWithUsage(t, a, u, u.newChat(), "q", frame)
	eventually(t, func() bool { return strings.Contains(logBuf.String()[before:], "run usage run="+run) }, "the usage line")
	got := logBuf.String()[before:]
	if !strings.Contains(got, "input_tokens=1000 output_tokens=200") {
		t.Errorf("usage line: %s", got)
	}
	for _, leak := range []string{"PRIVATE-PROMPT-77", "hunter2", "model="} {
		if strings.Contains(got, leak) {
			t.Errorf("log carries %q:\n%s", leak, got)
		}
	}
}

// Every stream that does not see its run to the end is counted once: the browser leaving, a lost upstream, and the
// server ending it on sign-out. A stream that saw its run finish is not.
func TestStreamDisconnectsAreCountedOnEveryPath(t *testing.T) {
	a := newApp(t, appOpts{backoff: fastBackoff})
	u := a.user("alice")
	disconnects := func() int64 { return obs.Snapshot().StreamsDisconnected }
	opened := func(s *stream) {
		eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return strings.Contains(s.buf.String(), "retry:") }, "the stream to open")
	}

	// Finished: not counted.
	n := disconnects()
	run := u.send(u.newChat(), "finishes")
	_, fr := a.hx.runWithInput(t, "finishes")
	s := u.open(run, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", `{"output":"ok"}`)
	s.waitFor(t, "run.completed")
	<-s.done
	if got := disconnects() - n; got != 0 {
		t.Fatalf("a finished stream counted %d", got)
	}

	// The browser leaves.
	n = disconnects()
	run = u.send(u.newChat(), "browser leaves")
	_, fr = a.hx.runWithInput(t, "browser leaves")
	s = u.open(run, "")
	recv(t, fr.opens, "stream")
	opened(s)
	s.close()
	if got := disconnects() - n; got != 1 {
		t.Fatalf("browser leaving counted %d, want 1", got)
	}

	// The upstream is lost: the stream carries run.disconnected and is counted once, also after the browser leaves.
	n = disconnects()
	run = u.send(u.newChat(), "upstream lost")
	_, fr = a.hx.runWithInput(t, "upstream lost")
	s = u.open(run, "")
	recv(t, fr.opens, "stream")
	a.hx.setStatus(fr, "weird-unknown-state", "")
	a.hx.dropStream(fr)
	s.waitFor(t, "run.disconnected")
	s.close()
	if got := disconnects() - n; got != 1 {
		t.Fatalf("lost upstream counted %d, want 1", got)
	}

	// The server ends it: sign-out.
	n = disconnects()
	run = u.send(u.newChat(), "signed out")
	_, fr = a.hx.runWithInput(t, "signed out")
	s = u.open(run, "")
	recv(t, fr.opens, "stream")
	opened(s)
	if w := u.do("POST", "/api/auth/logout", nil, nil); w.Code >= 300 {
		t.Fatalf("logout: %d", w.Code)
	}
	s.waitFor(t, "run.disconnected")
	<-s.done
	if got := disconnects() - n; got != 1 {
		t.Fatalf("sign-out counted %d, want 1", got)
	}
}
