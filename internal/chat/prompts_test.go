package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// ---- fake Hermes: a run that is blocked on a decision ---------------------------------
//
// The routes and bodies follow the deployed Hermes 0.22.5 source (gateway/platforms/api_server_runs.py
// _handle_run_approval / _handle_run_clarify, api_server_authority_runs.respond_run): an answer for a
// request that is not pending is 409, and the tool only runs after the decision. The fake does the same:
// it emits nothing past the prompt until the right answer arrives. All values are fabricated.

type fakePrompt struct {
	kind string
	gen  *int64
}

type fakePrompts struct {
	pending map[string]fakePrompt // upstream request id -> what it waits for
	got     []string              // the raw body of every answer Hermes accepted
}

// block makes the run wait for a decision with this upstream request id.
func (f *fakeHermes) block(r *fakeRun, kind, reqID string, gen *int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.prompts == nil {
		r.prompts = &fakePrompts{pending: map[string]fakePrompt{}}
	}
	r.prompts.pending[reqID] = fakePrompt{kind: kind, gen: gen}
}

func (f *fakeHermes) accepted(r *fakeRun) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.prompts == nil {
		return nil
	}
	return append([]string(nil), r.prompts.got...)
}

func (f *fakeHermes) answerPrompt(w http.ResponseWriter, fr *fakeRun, route string, body []byte) {
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	reqID, _ := in["request_id"].(string)
	f.mu.Lock()
	var p fakePrompt
	var ok bool
	if fr.prompts != nil {
		p, ok = fr.prompts.pending[reqID]
	}
	kindOK := ok && ((route == "approval" && p.kind == "approval") || (route == "clarify" && p.kind == "clarify"))
	if !kindOK {
		f.mu.Unlock()
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"approval_not_pending"}}`)
		return
	}
	// Authority prompts take exactly {request_id, execution_generation, choice|answer}; the legacy
	// approval takes {choice, request_id}.
	field := map[string]string{"approval": "choice", "clarify": "answer"}[route]
	want := 2
	if p.gen != nil {
		want = 3
		if g, _ := in["execution_generation"].(float64); int64(g) != *p.gen {
			f.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"code":"stale_generation"}}`)
			return
		}
	}
	answer, _ := in[field].(string)
	if len(in) != want || answer == "" {
		f.mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"code":"invalid_params"}}`)
		return
	}
	delete(fr.prompts.pending, reqID)
	fr.prompts.got = append(fr.prompts.got, string(body))
	frames := fr.frames
	f.mu.Unlock()
	// The run resumes only now.
	switch {
	case route == "approval" && answer == "deny":
		frames <- "event: run.completed\ndata: {\"output\":\"not run: denied\"}\n\n"
	case route == "approval":
		frames <- "event: tool.completed\ndata: {\"tool_call_id\":\"t1\",\"summary\":\"ran\"}\n\n"
		frames <- "event: run.completed\ndata: {\"output\":\"ran after approval\"}\n\n"
	default:
		frames <- "event: run.completed\ndata: {\"output\":\"you chose " + answer + "\"}\n\n"
	}
	fmt.Fprintf(w, `{"object":"hermes.run.%s_response","resolved":1}`, route)
}

// serveRunControls makes /v1/capabilities advertise the approval route, and the clarify route when asked.
// The base is the real captured document, which advertises approvals and not clarification.
func serveRunControls(t *testing.T, a *app, clarify bool) {
	t.Helper()
	caps := readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json")
	if clarify {
		var d map[string]any
		if err := json.Unmarshal([]byte(caps), &d); err != nil {
			t.Fatal(err)
		}
		d["endpoints"].(map[string]any)["run_clarify"] = map[string]string{"method": "POST", "path": "/v1/runs/{run_id}/clarify"}
		b, _ := json.Marshal(d)
		caps = string(b)
	}
	a.hx.mu.Lock()
	a.hx.canned["/v1/capabilities"] = caps
	a.hx.mu.Unlock()
}

// promptRun starts a run, opens its stream and waits for the upstream subscription.
func promptRun(t *testing.T, a *app, u *person, input string) (runID string, fr *fakeRun, s *stream) {
	t.Helper()
	runID = u.send(u.newChat(), input)
	_, fr = a.hx.runWithInput(t, input)
	s = u.open(runID, "")
	recv(t, fr.opens, "upstream stream")
	return runID, fr, s
}

type runPrompts struct {
	Status  string
	Prompts []struct {
		PromptID, Kind, Text, Detail string
		Choices                      []string
	}
}

func getPrompts(t *testing.T, u *person, run string) runPrompts {
	t.Helper()
	w := u.get("/api/runs/" + run)
	if w.Code != 200 {
		t.Fatalf("get run: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[runPrompts](t, w)
}

const approvalFrame = `{"request_id":"req-1","command":"rm -rf /tmp/scratch","description":"delete scratch dir","choices":["once","session","always","deny"]}`

func respondCalls(a *app) int {
	n := 0
	for _, r := range a.hx.requests() {
		// The clarify route probe asks about a run that cannot exist (hermes.Client.proven): not an answer.
		if strings.Contains(r.Path, "/run_0000000000000000antaresprobe/") {
			continue
		}
		if r.Method == "POST" && (strings.HasSuffix(r.Path, "/approval") || strings.HasSuffix(r.Path, "/clarify")) {
			n++
		}
	}
	return n
}

// A run that needs a decision is surfaced, stays blocked, and proceeds only after the user approves.
func TestRunNeedingApprovalDoesNotProceedUntilApproved(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, false)
	u := a.user("alice")
	run, fr, s := promptRun(t, a, u, "clean up scratch")
	a.hx.block(fr, "approval", "req-1", nil)
	a.hx.push(fr, "approval.request", approvalFrame)
	s.waitFor(t, "run.prompt")

	// Surfaced from the database: kind, redacted text, and only the choices a user may send.
	got := getPrompts(t, u, run)
	if got.Status != store.RunRunning || len(got.Prompts) != 1 {
		t.Fatalf("run %s with %d prompts", got.Status, len(got.Prompts))
	}
	p := got.Prompts[0]
	if p.Kind != "approval" || p.Text != "rm -rf /tmp/scratch" || strings.Join(p.Choices, ",") != "once,deny" {
		t.Errorf("prompt: %+v", p)
	}
	if strings.Contains(a.users["alice"].seen[len(a.users["alice"].seen)-1], "req-1") {
		t.Error("the upstream request id reached the browser")
	}

	// Nothing proceeds unattended: no decision is sent, the run is not stopped or completed.
	time.Sleep(150 * time.Millisecond)
	if respondCalls(a) != 0 || s.has("run.completed") || s.has("run.failed") || a.hx.runStopped(fr) {
		t.Fatalf("the run moved without a decision: respond=%d events=%v", respondCalls(a), s.names())
	}
	if st := u.runStatus(run); st != store.RunRunning {
		t.Fatalf("status %s", st)
	}

	// The pending decision lives in the database: a lost stream and a lost journal do not lose it.
	s.close()
	a.svc.mu.Lock()
	delete(a.svc.live, run)
	a.svc.mu.Unlock()
	if again := getPrompts(t, u, run); len(again.Prompts) != 1 || again.Prompts[0].PromptID != p.PromptID {
		t.Fatalf("prompt lost across a reload: %+v", again.Prompts)
	}

	w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"})
	if w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	eventually(t, func() bool { return u.runStatus(run) == store.RunCompleted }, "COMPLETED after approval")
	acc := a.hx.accepted(fr)
	if len(acc) != 1 || !strings.Contains(acc[0], `"choice":"once"`) || !strings.Contains(acc[0], `"request_id":"req-1"`) {
		t.Errorf("Hermes was sent %v", acc)
	}
	if left := getPrompts(t, u, run); len(left.Prompts) != 0 {
		t.Errorf("an answered prompt is still open: %+v", left.Prompts)
	}
	// A second click is stale, not a second decision.
	w = u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"})
	if w.Code != http.StatusConflict || errCode(t, w) != "PROMPT_STALE" || respondCalls(a) != 1 {
		t.Errorf("repeat: %d %s, %d decisions sent", w.Code, w.Body.String(), respondCalls(a))
	}
}

func TestDenyIsSentAndRunStillEndsHonestly(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, false)
	u := a.user("alice")
	run, fr, s := promptRun(t, a, u, "wipe it")
	a.hx.block(fr, "approval", "req-1", nil)
	a.hx.push(fr, "approval.request", approvalFrame)
	s.waitFor(t, "run.prompt")
	p := getPrompts(t, u, run).Prompts[0]
	if w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "deny"}); w.Code != 200 {
		t.Fatalf("deny: %d %s", w.Code, w.Body.String())
	}
	s.waitFor(t, "run.completed")
	if acc := a.hx.accepted(fr); len(acc) != 1 || !strings.Contains(acc[0], `"choice":"deny"`) {
		t.Errorf("Hermes was sent %v", acc)
	}
}

// "session" and "always" are standing grants: not offered, and refused if sent. The prompt stays open.
func TestStandingGrantsAndBadBodiesAreRefusedAndLeavePromptOpen(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, false)
	u := a.user("alice")
	run, fr, s := promptRun(t, a, u, "grant me")
	a.hx.block(fr, "approval", "req-1", nil)
	a.hx.push(fr, "approval.request", approvalFrame)
	s.waitFor(t, "run.prompt")
	p := getPrompts(t, u, run).Prompts[0]
	for name, body := range map[string]any{
		"always":        map[string]string{"promptId": p.PromptID, "choice": "always"},
		"session":       map[string]string{"promptId": p.PromptID, "choice": "session"},
		"empty":         map[string]string{"promptId": p.PromptID, "choice": ""},
		"request id":    map[string]string{"promptId": p.PromptID, "choice": "once", "request_id": "req-1"},
		"profile":       map[string]string{"promptId": p.PromptID, "choice": "once", "profile": "default"},
		"generation":    map[string]any{"promptId": p.PromptID, "choice": "once", "execution_generation": 9},
		"upstream id":   map[string]string{"promptId": "req-1", "choice": "once"},
		"missing":       map[string]string{"choice": "once"},
		"clarify field": map[string]string{"promptId": p.PromptID, "answer": "yes"},
	} {
		if w := u.do("POST", "/api/runs/"+run+"/approve", nil, body); w.Code != 400 && w.Code != 404 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if respondCalls(a) != 0 {
		t.Fatalf("a refused body reached Hermes (%d calls)", respondCalls(a))
	}
	if still := getPrompts(t, u, run); len(still.Prompts) != 1 {
		t.Fatalf("a refused answer closed the prompt: %+v", still.Prompts)
	}
	if w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"}); w.Code != 200 {
		t.Errorf("the prompt could no longer be approved: %d %s", w.Code, w.Body.String())
	}
}

// Acceptance: a foreign or unauthenticated caller can neither approve nor answer another user's run.
func TestForeignAndUnauthenticatedCannotAnswerAnotherUsersPrompt(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, true)
	alice, bob := a.user("alice"), a.user("bobby")
	admin := a.account("root", "ADMIN", "APPROVED", "READY")
	run, fr, s := promptRun(t, a, alice, "alice work")
	a.hx.block(fr, "approval", "req-1", nil)
	var gen int64 = 4
	a.hx.block(fr, "clarify", "ask-1", &gen)
	a.hx.push(fr, "approval.request", approvalFrame)
	a.hx.push(fr, "clarify.request", `{"prompt_id":"ask-1","execution_generation":4,"question":"which one?","choices":[]}`)
	eventually(t, func() bool { return len(getPrompts(t, alice, run).Prompts) == 2 }, "both prompts")
	var apr, clr string
	for _, p := range getPrompts(t, alice, run).Prompts {
		if p.Kind == "approval" {
			apr = p.PromptID
		} else {
			clr = p.PromptID
		}
	}

	attackers := map[string]func(method, path string, body any) *httptest.ResponseRecorder{
		"approved user B": func(m, p string, b any) *httptest.ResponseRecorder { return bob.do(m, p, nil, b) },
		"admin":           func(m, p string, b any) *httptest.ResponseRecorder { return admin.do(m, p, nil, b) },
		"no session": func(m, p string, b any) *httptest.ResponseRecorder {
			raw, _ := json.Marshal(b)
			r := httptest.NewRequest(m, testOrigin+p, strings.NewReader(string(raw)))
			r.Header.Set("Origin", testOrigin)
			w := httptest.NewRecorder()
			a.h.ServeHTTP(w, r)
			return w
		},
	}
	for who, do := range attackers {
		for _, c := range []struct {
			path string
			body map[string]string
		}{
			{"/api/runs/" + run + "/approve", map[string]string{"promptId": apr, "choice": "once"}},
			{"/api/runs/" + run + "/approve", map[string]string{"promptId": apr, "choice": "deny"}},
			{"/api/runs/" + run + "/clarify", map[string]string{"promptId": clr, "answer": "the first"}},
		} {
			w := do("POST", c.path, c.body)
			// An admin has no chat workspace: the Active guard refuses it (409) before ownership is asked.
			want := map[string]int{"approved user B": http.StatusNotFound, "admin": http.StatusConflict, "no session": http.StatusUnauthorized}[who]
			if w.Code != want {
				t.Errorf("%s %s: %d %s", who, c.path, w.Code, w.Body.String())
			}
		}
		if w := do("GET", "/api/runs/"+run, nil); w.Code == 200 || strings.Contains(w.Body.String(), "rm -rf") {
			t.Errorf("%s read the prompt: %d %s", who, w.Code, w.Body.String())
		}
	}
	// B answering with B's own run id and A's prompt id is just as foreign.
	run2 := bob.send(bob.newChat(), "bob work")
	a.hx.runWithInput(t, "bob work")
	if w := bob.do("POST", "/api/runs/"+run2+"/approve", nil, map[string]string{"promptId": apr, "choice": "once"}); w.Code != 404 {
		t.Errorf("cross-run prompt id: %d %s", w.Code, w.Body.String())
	}
	// An approval prompt is not answerable as a clarification, and the reverse.
	if w := alice.do("POST", "/api/runs/"+run+"/clarify", nil, map[string]string{"promptId": apr, "answer": "x"}); w.Code != 404 {
		t.Errorf("kind confusion: %d %s", w.Code, w.Body.String())
	}
	if respondCalls(a) != 0 || len(a.hx.accepted(fr)) != 0 {
		t.Fatalf("an unauthorised answer reached Hermes (%d calls)", respondCalls(a))
	}
	if left := getPrompts(t, alice, run); len(left.Prompts) != 2 || s.has("run.completed") {
		t.Fatalf("the owner's prompts were disturbed: %+v", left.Prompts)
	}
	// The owner still can.
	if w := alice.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": apr, "choice": "once"}); w.Code != 200 {
		t.Errorf("owner: %d %s", w.Code, w.Body.String())
	}
}

// Acceptance: ask, answer, resume.
func TestClarificationRoundTrip(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, true)
	u := a.user("alice")
	run, fr, s := promptRun(t, a, u, "pick a file")
	var gen int64 = 7
	a.hx.block(fr, "clarify", "ask-1", &gen)
	a.hx.push(fr, "clarify.request", `{"event":"clarify.request","kind":"clarify","prompt_id":"ask-1","execution_generation":7,"question":"Which file should I use?","choices":["a.txt","b.txt"],"multi_select":false}`)
	s.waitFor(t, "run.prompt")

	got := getPrompts(t, u, run)
	if len(got.Prompts) != 1 || got.Prompts[0].Kind != "clarify" || got.Prompts[0].Text != "Which file should I use?" || strings.Join(got.Prompts[0].Choices, ",") != "a.txt,b.txt" {
		t.Fatalf("asked: %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if s.has("run.completed") || got.Status != store.RunRunning {
		t.Fatal("the run moved on before the question was answered")
	}

	w := u.do("POST", "/api/runs/"+run+"/clarify", nil, map[string]string{"promptId": got.Prompts[0].PromptID, "answer": "b.txt"})
	if w.Code != 200 {
		t.Fatalf("answer: %d %s", w.Code, w.Body.String())
	}
	acc := a.hx.accepted(fr)
	if len(acc) != 1 {
		t.Fatalf("Hermes was sent %v", acc)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(acc[0]), &sent)
	if len(sent) != 3 || sent["request_id"] != "ask-1" || sent["execution_generation"] != float64(7) || sent["answer"] != "b.txt" {
		t.Errorf("clarify body: %v", sent)
	}
	s.waitFor(t, "run.completed")
	if !strings.Contains(strings.Join(s.names(), " "), "run.prompt.settled") {
		t.Errorf("events %v", s.names())
	}
	if st := u.runStatus(run); st != store.RunCompleted {
		t.Errorf("status %s", st)
	}
	if left := getPrompts(t, u, run); len(left.Prompts) != 0 {
		t.Errorf("answered question still open: %+v", left.Prompts)
	}
	// The answer text is not kept anywhere in Antares' database.
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM run_prompts WHERE text || detail || decision LIKE '%b.txt%' AND decision != ''`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the answer was stored (%d, %v)", n, err)
	}
}

// What Antares cannot safely collect still fails closed: nothing is approved on the user's behalf.
func TestPromptsItCannotCollectFailClosed(t *testing.T) {
	cases := map[string]struct {
		clarifyRoute bool
		event, frame string
	}{
		"clarify route not advertised":   {false, "clarify.request", `{"prompt_id":"ask-1","execution_generation":1,"question":"q"}`},
		"clarify without a generation":   {true, "clarify.request", `{"prompt_id":"ask-1","question":"q"}`},
		"approval without a request id":  {true, "approval.request", `{"command":"x","choices":["once","deny"]}`},
		"approval that cannot be denied": {true, "approval.request", `{"request_id":"r1","command":"x","choices":["once"]}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := newApp(t)
			serveRunControls(t, a, c.clarifyRoute)
			u := a.user("alice")
			run, fr, s := promptRun(t, a, u, "do it")
			a.hx.push(fr, c.event, c.frame)
			s.waitFor(t, "run.failed")
			if st := u.runStatus(run); st != store.RunFailed || !a.hx.runStopped(fr) || respondCalls(a) != 0 {
				t.Errorf("status %s stopped=%v respond=%d", st, a.hx.runStopped(fr), respondCalls(a))
			}
			if got := getPrompts(t, u, run); len(got.Prompts) != 0 {
				t.Errorf("an unanswerable prompt was offered: %+v", got.Prompts)
			}
			if code := decodeBody[struct{ ErrorCode string }](t, u.get("/api/runs/"+run)).ErrorCode; code != hermes.CodeCapabilityNA {
				t.Errorf("error code %q", code)
			}
		})
	}
}

// A decision Hermes no longer holds, a finished run and a cancelled run are stale, never "approved".
func TestStalePromptsAreRefusedAndNeverReportedAsAnswered(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, false)
	u := a.user("alice")

	// Hermes forgot the request (its own timeout, a restart): 409 upstream -> PROMPT_STALE, prompt settled.
	run, fr, s := promptRun(t, a, u, "stale one")
	a.hx.push(fr, "approval.request", approvalFrame) // not registered with the fake: it will say 409
	s.waitFor(t, "run.prompt")
	p := getPrompts(t, u, run).Prompts[0]
	w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"})
	if w.Code != http.StatusConflict || errCode(t, w) != "PROMPT_STALE" {
		t.Fatalf("upstream-stale: %d %s", w.Code, w.Body.String())
	}
	if got := getPrompts(t, u, run); len(got.Prompts) != 0 {
		t.Errorf("a prompt Hermes no longer holds is still offered: %+v", got.Prompts)
	}

	// Cancelling the run settles what it was waiting for.
	run2, fr2, s2 := promptRun(t, a, u, "cancel me")
	a.hx.block(fr2, "approval", "req-1", nil)
	a.hx.push(fr2, "approval.request", approvalFrame)
	s2.waitFor(t, "run.prompt")
	p2 := getPrompts(t, u, run2).Prompts[0]
	if w := u.do("POST", "/api/runs/"+run2+"/cancel", nil, nil); w.Code != 200 {
		t.Fatalf("cancel: %d", w.Code)
	}
	before := respondCalls(a)
	w = u.do("POST", "/api/runs/"+run2+"/approve", nil, map[string]string{"promptId": p2.PromptID, "choice": "once"})
	if w.Code != http.StatusConflict || respondCalls(a) != before {
		t.Errorf("approve after cancel: %d %s (%d calls)", w.Code, w.Body.String(), respondCalls(a)-before)
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM run_prompts WHERE id = ?`, p2.PromptID).Scan(&status); err != nil || status != store.PromptSettled {
		t.Errorf("prompt of a cancelled run is %q (%v)", status, err)
	}
}

// A suspended account loses the ability to decide immediately, and nothing is sent.
func TestSuspendedAccountCannotApprove(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, false)
	u := a.user("alice")
	run, fr, s := promptRun(t, a, u, "suspend me")
	a.hx.block(fr, "approval", "req-1", nil)
	a.hx.push(fr, "approval.request", approvalFrame)
	s.waitFor(t, "run.prompt")
	p := getPrompts(t, u, run).Prompts[0]
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"})
	if w.Code == 200 || respondCalls(a) != 0 {
		t.Errorf("suspended: %d %s (%d calls)", w.Code, w.Body.String(), respondCalls(a))
	}
}

// A delivery failure leaves the prompt open so the user can retry; it is never reported answered.
func TestUndeliveredAnswerLeavesPromptOpen(t *testing.T) {
	a := newApp(t)
	serveRunControls(t, a, false)
	u := a.user("alice")
	run, fr, s := promptRun(t, a, u, "flaky")
	a.hx.block(fr, "approval", "req-1", nil)
	a.hx.push(fr, "approval.request", approvalFrame)
	s.waitFor(t, "run.prompt")
	p := getPrompts(t, u, run).Prompts[0]
	hid := a.hx.hidFor(fr)
	a.hx.mu.Lock()
	a.hx.failNext["POST /v1/runs/"+hid+"/approval"] = 503
	a.hx.mu.Unlock()
	if w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("undelivered: %d %s", w.Code, w.Body.String())
	}
	if got := getPrompts(t, u, run); len(got.Prompts) != 1 || s.has("run.completed") {
		t.Fatalf("after a failed delivery: %+v", got.Prompts)
	}
	if w := u.do("POST", "/api/runs/"+run+"/approve", nil, map[string]string{"promptId": p.PromptID, "choice": "once"}); w.Code != 200 {
		t.Errorf("retry: %d %s", w.Code, w.Body.String())
	}
}

func (f *fakeHermes) runStopped(r *fakeRun) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return r.stopped
}

func (f *fakeHermes) hidFor(r *fakeRun) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, x := range f.runs {
		if x == r {
			return id
		}
	}
	return ""
}
