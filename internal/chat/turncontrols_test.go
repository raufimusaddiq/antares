package chat

import (
	"net/http"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Hermes has no regenerate / edit / rewind route (docs/runbooks/turn-controls.md).
var noRouteOps = []string{"regenerate", "edit-turn", "rewind"}

func steerCalls(a *app) int {
	n := 0
	for _, r := range a.hx.requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/steer") {
			n++
		}
	}
	return n
}

func TestSteerLandsOnOwnRunningRun(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "long task")
	hid, fr := a.hx.runWithInput(t, "long task")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "assistant.delta", `{"delta":"working "}`)
	eventually(t, func() bool { return s.text() == "working " }, "first delta")

	w := u.do("POST", "/api/runs/"+run+"/steer", nil, map[string]string{"input": "use the short form"})
	if w.Code != 200 || !decodeBody[struct{ Accepted bool }](t, w).Accepted {
		t.Fatalf("steer: %d %s", w.Code, w.Body.String())
	}
	var got rec
	for _, r := range a.hx.requests() {
		if strings.HasSuffix(r.Path, "/v1/runs/"+hid+"/steer") {
			got = r
		}
	}
	if got.Method != "POST" || !strings.Contains(got.Body, "use the short form") {
		t.Errorf("Hermes steer request: %+v", got)
	}
	s.waitFor(t, "run.steered")
	if st := u.runStatus(run); st != store.RunRunning {
		t.Errorf("steer changed the run to %s", st)
	}
	if s.has("run.completed") || strings.Contains(s.text(), "short form") {
		t.Errorf("steer invented output: %v %q", s.names(), s.text())
	}
	a.hx.push(fr, "run.completed", `{"output":"working "}`)
	s.waitFor(t, "run.completed")
}

func TestSteerRefusedWhenRunIsOverOrHermesDeclines(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "task one")
	_, fr := a.hx.runWithInput(t, "task one")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")

	// Hermes says the run is not accepting steer text: STEER_REFUSED, nothing announced.
	a.hx.setStatus(fr, "stopping", "")
	w := u.do("POST", "/api/runs/"+run+"/steer", nil, map[string]string{"input": "late"})
	if w.Code != http.StatusConflict || errCode(t, w) != "STEER_REFUSED" {
		t.Fatalf("declined steer: %d %s", w.Code, w.Body.String())
	}
	if s.has("run.steered") {
		t.Error("a declined steer was announced as landed")
	}
	a.hx.setStatus(fr, "running", "")
	a.hx.push(fr, "run.completed", `{"output":"done"}`)
	s.waitFor(t, "run.completed")

	// A finished run is refused before Hermes is asked.
	before := steerCalls(a)
	w = u.do("POST", "/api/runs/"+run+"/steer", nil, map[string]string{"input": "too late"})
	if w.Code != http.StatusConflict || errCode(t, w) != "STEER_REFUSED" || steerCalls(a) != before {
		t.Errorf("steer after completion: %d %s (steer calls %d -> %d)", w.Code, w.Body.String(), before, steerCalls(a))
	}

	// Server-owned fields and empty text never get through.
	run2 := u.send(u.newChat(), "task two")
	for _, body := range []map[string]string{{"input": " "}, {"input": "x", "profile": "other"}, {"input": "x", "run_id": "run_1"}} {
		if w := u.do("POST", "/api/runs/"+run2+"/steer", nil, body); w.Code != 400 {
			t.Errorf("steer body %v: %d, want 400", body, w.Code)
		}
	}
}

func TestSteerAndTurnControlsRefuseAnotherUsersChat(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chat := alice.newChat()
	run := alice.send(chat, "alice private task")
	_, fr := a.hx.runWithInput(t, "alice private task")
	s := alice.open(run, "")
	recv(t, fr.opens, "alice's stream")

	check := func(op, path string) {
		t.Helper()
		before := len(a.hx.requests())
		got := bob.do("POST", path, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hijack"})
		_, absent := fill("POST "+strings.NewReplacer(chat, "{chatId}", run, "{runId}").Replace(path), store.NewID(), store.NewID())
		want := bob.do("POST", absent, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hijack"})
		if got.Code != http.StatusNotFound || !sameAnswer(got, want) {
			t.Errorf("%s: foreign caller got %d %s; unknown id got %d %s", op, got.Code, got.Body.String(), want.Code, want.Body.String())
		}
		if n := len(a.hx.requests()); n != before {
			t.Errorf("%s: a refused request made %d call(s) to Hermes", op, n-before)
		}
	}
	check("steer", "/api/runs/"+run+"/steer")
	for _, op := range noRouteOps {
		check(op, "/api/chats/"+chat+"/"+op)
	}
	if s.has("run.steered") || u2status(alice, run) != store.RunRunning {
		t.Errorf("bob's attempts touched alice's run: %v", s.names())
	}
	// Alice herself is told the truth about the three operations Hermes cannot do.
	for _, op := range noRouteOps {
		w := alice.do("POST", "/api/chats/"+chat+"/"+op, nil, map[string]string{})
		if w.Code != http.StatusNotImplemented || errCode(t, w) != "CAPABILITY_UNAVAILABLE" {
			t.Errorf("owner %s: %d %s, want 501 CAPABILITY_UNAVAILABLE", op, w.Code, w.Body.String())
		}
	}
	a.hx.push(fr, "run.completed", `{"output":"ok"}`)
	s.waitFor(t, "run.completed")
}

func u2status(p *person, run string) string { return p.runStatus(run) }

// A rewind request (which Hermes cannot perform, so it does nothing) must not
// close, strand or corrupt the chat's live stream, and a client that reconnects
// afterwards resumes without loss or duplicates.
func TestRewindDoesNotOrphanSSEStreams(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "stream while rewinding")
	_, fr := a.hx.runWithInput(t, "stream while rewinding")
	s1 := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "assistant.delta", `{"delta":"one "}`)
	eventually(t, func() bool { return s1.text() == "one " }, "first delta")

	rows := func() (n int) {
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE user_id = ?`, u.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	runsBefore, hermesBefore := rows(), len(a.hx.requests())
	if w := u.do("POST", "/api/chats/"+chat+"/rewind", nil, map[string]string{}); w.Code != http.StatusNotImplemented {
		t.Fatalf("rewind: %d %s", w.Code, w.Body.String())
	}
	if rows() != runsBefore || len(a.hx.requests()) != hermesBefore || u.runStatus(run) != store.RunRunning {
		t.Fatal("the refused rewind changed a run or reached Hermes")
	}

	// The open stream is still attached and still receives.
	a.hx.push(fr, "assistant.delta", `{"delta":"two "}`)
	eventually(t, func() bool { return s1.text() == "one two " }, "delta after the rewind request")

	lastID := s1.events()[len(s1.events())-1].ID

	// The client drops, reconnects with Last-Event-ID and recovers the rest exactly once.
	s1.close()
	a.hx.push(fr, "assistant.delta", `{"delta":"three"}`)
	a.hx.push(fr, "run.completed", `{"output":"one two three"}`)
	eventually(t, func() bool { return u.runStatus(run) == store.RunCompleted }, "run to complete")
	s2 := u.open(run, lastID)
	<-s2.done
	if got := s1.text() + s2.text(); got != "one two three" {
		t.Errorf("assembled %q (first %q, resumed %q)", got, s1.text(), s2.text())
	}
	seen := map[string]bool{}
	for _, e := range append(s1.events(), s2.events()...) {
		if seen[e.ID] {
			t.Errorf("event %s delivered twice", e.ID)
		}
		seen[e.ID] = true
	}
	if names := s2.names(); names[len(names)-1] != "run.completed" {
		t.Errorf("resumed stream: %v", names)
	}
}
