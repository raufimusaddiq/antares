package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

func fastBackoff(int) time.Duration { return 5 * time.Millisecond }

// Two approved users chat privately; each request reaches Hermes under that
// user's own profile and key, and neither can touch the other's chat, run,
// messages, stream or cancel by id (404, never 403).
func TestTwoUsersChatPrivately(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chatA, chatB := alice.newChat(), bob.newChat()

	runA := alice.send(chatA, "alice secret question")
	_, frA := a.hx.runWithInput(t, "alice secret question")
	sA := alice.open(runA, "")
	recv(t, frA.opens, "alice's stream")
	a.hx.push(frA, "assistant.delta", `{"delta":"alice answer"}`)
	a.hx.push(frA, "run.completed", `{}`)
	sA.waitFor(t, "run.completed")

	runB := bob.send(chatB, "bob question")
	_, frB := a.hx.runWithInput(t, "bob question")
	recv(t, frB.opens, "no stream expected yet")

	// Bob probing Alice's resources by id.
	probes := []struct{ method, path string }{
		{"GET", "/api/chats/" + chatA},
		{"GET", "/api/chats/" + chatA + "/messages"},
		{"POST", "/api/chats/" + chatA + "/messages"},
		{"GET", "/api/runs/" + runA},
		{"GET", "/api/runs/" + runA + "/events"},
		{"POST", "/api/runs/" + runA + "/cancel"},
	}
	before := len(a.hx.requests())
	for _, p := range probes {
		hdr := map[string]string{"Idempotency-Key": nextIdem()}
		w := bob.do(p.method, p.path, hdr, map[string]string{"input": "x"})
		if w.Code != 404 || errCode(t, w) != "NOT_FOUND" {
			t.Errorf("bob %s %s = %d %s", p.method, p.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "alice") {
			t.Errorf("response mentions the other user: %s", w.Body.String())
		}
	}
	if n := len(a.hx.requests()); n != before {
		t.Errorf("probing a foreign id made %d Hermes request(s)", n-before)
	}
	// Alice's run is untouched by Bob's cancel attempt.
	if st := alice.runStatus(runA); st != store.RunCompleted {
		t.Errorf("alice's run is %s after bob's probes", st)
	}
	// Lists contain only the caller's chats.
	for who, p := range map[string]*person{"alice": alice, "bob": bob} {
		w := p.get("/api/chats")
		l := decodeBody[struct{ Chats []struct{ ID string } }](t, w)
		want := map[string]string{"alice": chatA, "bob": chatB}[who]
		if len(l.Chats) != 1 || l.Chats[0].ID != want {
			t.Errorf("%s's list: %s", who, w.Body.String())
		}
	}
	// Each request went to its own profile with its own key.
	for _, r := range a.hx.requests() {
		switch {
		case strings.HasPrefix(r.Path, "/p/"+alice.profile+"/"):
			if r.Auth != "Bearer "+alice.key {
				t.Errorf("alice's profile called with %q", r.Auth)
			}
		case strings.HasPrefix(r.Path, "/p/"+bob.profile+"/"):
			if r.Auth != "Bearer "+bob.key {
				t.Errorf("bob's profile called with %q", r.Auth)
			}
		default:
			t.Errorf("request outside any tenant route: %s", r.Path)
		}
	}
	_ = runB
	if !strings.Contains(sA.text(), "alice answer") {
		t.Errorf("alice's stream: %v", sA.names())
	}
}

// Rule 2: a profile, route prefix, key, header or session id from the client is
// refused, and none of it ever reaches Hermes.
func TestClientSuppliedProfileKeyAndSessionNeverReachHermes(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chatA := alice.newChat()
	idem := func() map[string]string { return map[string]string{"Idempotency-Key": nextIdem()} }
	with := func(h map[string]string, k, v string) map[string]string { h[k] = v; return h }

	msg := "/api/chats/" + chatA + "/messages"
	attempts := []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		body   any
	}{
		{"bearer header", "POST", msg, with(idem(), "Authorization", "Bearer "+bob.key), map[string]string{"input": "x"}},
		{"api key header", "POST", msg, with(idem(), "X-Api-Key", bob.key), map[string]string{"input": "x"}},
		{"profile header", "POST", msg, with(idem(), "X-Hermes-Profile", bob.profile), map[string]string{"input": "x"}},
		{"session key header", "POST", msg, with(idem(), "X-Hermes-Session-Key", "chatui:user:"+bob.id), map[string]string{"input": "x"}},
		{"session id header", "POST", msg, with(idem(), "X-Hermes-Session-Id", "sess_bob"), map[string]string{"input": "x"}},
		{"profile body field", "POST", msg, idem(), map[string]string{"input": "x", "profile": bob.profile}},
		{"key body field", "POST", msg, idem(), map[string]string{"input": "x", "api_key": bob.key}},
		{"session body field", "POST", msg, idem(), map[string]string{"input": "x", "session_id": "sess_bob"}},
		{"hermes session body field", "POST", msg, idem(), map[string]string{"input": "x", "hermes_session_id": "sess_bob"}},
		{"route prefix body field", "POST", msg, idem(), map[string]string{"input": "x", "route_prefix": "/p/" + bob.profile}},
		{"profile query", "POST", msg + "?profile=" + bob.profile, idem(), map[string]string{"input": "x"}},
		{"session query", "GET", msg + "?session_id=sess_bob", nil, nil},
		{"profile on create chat", "POST", "/api/chats", nil, map[string]string{"title": "t", "profile": bob.profile}},
		{"bearer on create chat", "POST", "/api/chats", map[string]string{"Authorization": "Bearer " + bob.key}, map[string]string{}},
	}
	before := len(a.hx.requests())
	for _, at := range attempts {
		w := alice.do(at.method, at.path, at.hdr, at.body)
		if w.Code != 400 || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", at.name, w.Code, w.Body.String())
		}
	}
	if n := len(a.hx.requests()); n != before {
		t.Errorf("a refused request still reached Hermes (%d requests)", n-before)
	}

	// The same message sent properly: Hermes sees Alice's own derived values only.
	alice.send(chatA, "plain message")
	for _, r := range a.hx.requests() {
		if strings.Contains(r.Path, bob.profile) || strings.Contains(r.Auth, bob.key) || strings.Contains(r.Body, bob.key) ||
			strings.Contains(r.Body, bob.profile) || strings.Contains(r.SessionKey, bob.id) || strings.Contains(r.Body, "sess_bob") {
			t.Errorf("client-supplied value reached Hermes: %+v", r)
		}
		if r.Method == "POST" && r.Path == "/p/"+alice.profile+"/v1/runs" {
			if r.SessionKey != "chatui:user:"+alice.id {
				t.Errorf("session key %q is not the server-derived one", r.SessionKey)
			}
			if !strings.HasPrefix(r.Idem, "antares-") {
				t.Errorf("upstream idempotency key %q is not server-generated", r.Idem)
			}
			var body map[string]string
			_ = json.Unmarshal([]byte(r.Body), &body)
			if len(body) != 2 || !strings.HasPrefix(body["session_id"], "sess_"+alice.profile) {
				t.Errorf("run body: %s", r.Body)
			}
		}
	}
}

func TestStreamingRunCompletesInOrderWithToolEvents(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "tell me")
	_, fr := a.hx.runWithInput(t, "tell me")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "tool.started", `{"tool_call_id":"t1","name":"Web search"}`)
	a.hx.push(fr, "assistant.delta", `{"delta":"Hel"}`)
	a.hx.push(fr, "unknown.noise", `{"x":1}`)
	a.hx.push(fr, "assistant.delta", `{"delta":"lo"}`)
	a.hx.push(fr, "tool.completed", `{"tool_call_id":"t1","summary":"3 results"}`)
	a.hx.push(fr, "run.completed", `{"output":"Hello"}`)
	s.waitFor(t, "run.completed")
	<-s.done

	want := []string{"run.started", "tool.started", "assistant.delta", "assistant.delta", "tool.completed", "run.completed"}
	got := s.names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events %v, want %v", got, want)
	}
	if s.text() != "Hello" {
		t.Errorf("text %q", s.text())
	}
	var seqs []string
	for _, e := range s.events() {
		seqs = append(seqs, e.ID)
	}
	for i, id := range seqs {
		if !strings.HasSuffix(id, "-"+string(rune('1'+i))) {
			t.Errorf("event ids not monotonic: %v", seqs)
			break
		}
	}
	if st := u.runStatus(run); st != store.RunCompleted {
		t.Errorf("status %s", st)
	}
	last := s.events()[len(s.events())-1]
	if !strings.Contains(last.Data, `"output":"Hello"`) || !strings.Contains(last.Data, `"status":"COMPLETED"`) {
		t.Errorf("terminal payload: %s", last.Data)
	}
	// A chat is free for the next turn once the run is terminal.
	if w := u.get("/api/chats/" + chat); decodeBody[struct{ ActiveRunID string }](t, w).ActiveRunID != "" {
		t.Errorf("chat still shows an active run: %s", w.Body.String())
	}
}

// Cancelling mid-stream marks the run cancelled, stops the upstream call (the
// stream connection closes and Hermes receives the stop), and nothing that
// arrives later can make it completed.
func TestCancelMidStreamStopsUpstreamAndStaysCancelled(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "long task")
	_, fr := a.hx.runWithInput(t, "long task")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "assistant.delta", `{"delta":"partial"}`)
	eventually(t, func() bool { return s.text() == "partial" }, "partial text")

	w := u.do("POST", "/api/runs/"+run+"/cancel", nil, nil)
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	v := decodeBody[struct {
		Status          string
		UpstreamStopped bool
	}](t, w)
	if v.Status != store.RunCancelled || !v.UpstreamStopped {
		t.Errorf("cancel answer: %s", w.Body.String())
	}
	recv(t, fr.ended, "upstream stream to be closed")
	a.hx.mu.Lock()
	stopped := fr.stopped
	a.hx.mu.Unlock()
	if !stopped {
		t.Error("Hermes never received the stop request")
	}
	s.waitFor(t, "run.cancelled")
	if s.has("run.completed") || s.has("run.failed") {
		t.Errorf("a cancelled run was reported as %v", s.names())
	}
	if st := u.runStatus(run); st != store.RunCancelled {
		t.Errorf("status %s", st)
	}
	// Cancelling again is harmless and reports the same state.
	if w := u.do("POST", "/api/runs/"+run+"/cancel", nil, nil); w.Code != 200 || decodeBody[struct{ Status string }](t, w).Status != store.RunCancelled {
		t.Errorf("second cancel: %d %s", w.Code, w.Body.String())
	}
	// A new connection to the cancelled run shows cancelled, never completed.
	s2 := u.open(run, "")
	<-s2.done
	if !s2.has("run.cancelled") || s2.has("run.completed") {
		t.Errorf("reopened stream: %v", s2.names())
	}
}

func TestFailedAndInterruptedRunsAreNeverShownAsCompleted(t *testing.T) {
	for _, tc := range []struct {
		frame, code string
		status      string
	}{
		{"run.failed", "RUN_FAILED", store.RunFailed},
		{"run.interrupted", "RUN_INTERRUPTED", store.RunFailed},
		{"run.cancelled", "", store.RunCancelled},
	} {
		a := newApp(t)
		u := a.user("alice")
		chat := u.newChat()
		run := u.send(chat, "go")
		_, fr := a.hx.runWithInput(t, "go")
		s := u.open(run, "")
		recv(t, fr.opens, "upstream stream")
		a.hx.push(fr, "assistant.delta", `{"delta":"half an ans"}`)
		a.hx.push(fr, tc.frame, `{}`)
		eventually(t, func() bool { return s.has("run.failed") || s.has("run.cancelled") }, "terminal event")
		if s.has("run.completed") {
			t.Errorf("%s reported completed", tc.frame)
		}
		if got := u.runStatus(run); got != tc.status {
			t.Errorf("%s: status %s, want %s", tc.frame, got, tc.status)
		}
		w := u.get("/api/runs/" + run)
		if ec := decodeBody[struct{ ErrorCode string }](t, w).ErrorCode; ec != tc.code {
			t.Errorf("%s: error code %q, want %q", tc.frame, ec, tc.code)
		}
	}
}

// A stream that ends with no terminal frame proves nothing: the state is read
// back from Hermes.
func TestDroppedStreamIsReconciledWithHermes(t *testing.T) {
	for _, tc := range []struct {
		upstream, output, want string
		event                  string
	}{
		{"completed", "the full answer", store.RunCompleted, "run.completed"},
		{"failed", "", store.RunFailed, "run.failed"},
		{"cancelled", "", store.RunCancelled, "run.cancelled"},
	} {
		a := newApp(t, appOpts{backoff: fastBackoff})
		u := a.user("alice")
		run := u.send(u.newChat(), "q")
		_, fr := a.hx.runWithInput(t, "q")
		s := u.open(run, "")
		recv(t, fr.opens, "upstream stream")
		a.hx.push(fr, "assistant.delta", `{"delta":"partial"}`)
		a.hx.setStatus(fr, tc.upstream, tc.output)
		a.hx.dropStream(fr)
		s.waitFor(t, tc.event)
		if u.runStatus(run) != tc.want {
			t.Errorf("%s: status %s", tc.upstream, u.runStatus(run))
		}
		if tc.output != "" && !strings.Contains(s.events()[len(s.events())-1].Data, tc.output) {
			t.Errorf("%s: terminal event lacks Hermes' output", tc.upstream)
		}
	}
}

func TestStreamDropWithRunStillGoingIsResumableNotCompleted(t *testing.T) {
	a := newApp(t, appOpts{backoff: fastBackoff})
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "q")
	_, fr := a.hx.runWithInput(t, "q")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "assistant.delta", `{"delta":"one "}`)
	eventually(t, func() bool { return s.text() == "one " }, "first delta")

	// The network breaks and keeps breaking: Hermes says the run is still going.
	for i := 0; i < reattachTries; i++ {
		a.hx.dropStream(fr)
		if i < reattachTries-1 {
			recv(t, fr.opens, "re-subscription")
		}
	}
	s.waitFor(t, "run.disconnected")
	<-s.done
	if s.has("run.completed") || s.has("run.failed") {
		t.Errorf("a run that may still be going was reported finished: %v", s.names())
	}
	if st := u.runStatus(run); st != store.RunDisconnected {
		t.Errorf("status %s, want DISCONNECTED", st)
	}
	// The chat stays occupied: resumable, not abandoned.
	w := u.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "another"})
	if w.Code != 409 || errCode(t, w) != "RUN_CONFLICT" {
		t.Errorf("send during a disconnected run: %d %s", w.Code, w.Body.String())
	}

	// Reconnecting reattaches, tells the client to replace its text, and finishes.
	s2 := u.open(run, "")
	recv(t, fr.opens, "reattached upstream stream")
	s2.waitFor(t, "run.resumed")
	a.hx.push(fr, "assistant.delta", `{"delta":"one two"}`)
	a.hx.push(fr, "run.completed", `{"output":"one two"}`)
	s2.waitFor(t, "run.completed")
	if s2.text() != "one two" {
		t.Errorf("resumed text %q", s2.text())
	}
	if st := u.runStatus(run); st != store.RunCompleted {
		t.Errorf("status %s", st)
	}
}

// Killing the browser's connection and reconnecting with Last-Event-ID resumes
// at the next event: every delta is seen exactly once.
func TestReconnectResumesWithoutDuplicating(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	s1 := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	for _, w := range []string{"alpha ", "beta ", "gamma "} {
		a.hx.push(fr, "assistant.delta", `{"delta":"`+w+`"}`)
	}
	eventually(t, func() bool { return s1.text() == "alpha beta gamma " }, "first three deltas")
	evs := s1.events()
	lastID := evs[len(evs)-1].ID
	s1.close() // the connection dies; the run goes on

	for _, w := range []string{"delta ", "epsilon"} {
		a.hx.push(fr, "assistant.delta", `{"delta":"`+w+`"}`)
	}
	a.hx.push(fr, "run.completed", `{}`)
	eventually(t, func() bool { return u.runStatus(run) == store.RunCompleted }, "run to complete with nobody listening")

	s2 := u.open(run, lastID)
	<-s2.done
	if got := s1.text() + s2.text(); got != "alpha beta gamma delta epsilon" {
		t.Errorf("assembled text %q (first %q, resumed %q)", got, s1.text(), s2.text())
	}
	seen := map[string]bool{}
	for _, e := range append(s1.events(), s2.events()...) {
		if seen[e.ID] {
			t.Errorf("event %s delivered twice", e.ID)
		}
		seen[e.ID] = true
	}
	if s2.names()[len(s2.names())-1] != "run.completed" {
		t.Errorf("resumed stream: %v", s2.names())
	}
}

// After a restart there is no in-memory journal. Recovery marks the run
// DISCONNECTED; a reconnect reconciles with Hermes and continues.
func TestRestartThenReconnectReattaches(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	s1 := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "assistant.delta", `{"delta":"before restart"}`)
	eventually(t, func() bool { return s1.text() == "before restart" }, "delta")
	oldID := s1.events()[len(s1.events())-1].ID
	s1.close()

	// "Restart": stop this service, recover, build a new one on the same DB and Hermes.
	a.svc.Close()
	if d, f, err := store.RecoverRuns(context.Background(), a.db, time.Now()); err != nil || d != 1 || f != 0 {
		t.Fatalf("recover: %d %d %v", d, f, err)
	}
	cl, _ := hermes.NewClient(a.hx.srv.URL, hermes.ClientOptions{})
	svc2, err := New(Options{DB: a.db, Box: a.box, Hermes: cl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc2.Close)
	a.svc = svc2
	a.h = rebuild(t, a, svc2)

	if st := u.runStatus(run); st != store.RunDisconnected {
		t.Fatalf("after recovery: %s", st)
	}
	a.hx.dropStream(fr) // Hermes' side: a fresh subscription is possible
	s2 := u.open(run, oldID)
	recv(t, fr.opens, "reattached upstream stream")
	s2.waitFor(t, "run.resumed")
	a.hx.push(fr, "assistant.delta", `{"delta":"after"}`)
	a.hx.push(fr, "run.completed", `{"output":"before restart after"}`)
	s2.waitFor(t, "run.completed")
	if s2.has("run.started") {
		t.Errorf("a restarted run was announced as new: %v", s2.names())
	}
	if st := u.runStatus(run); st != store.RunCompleted {
		t.Errorf("status %s", st)
	}

	// And a run Hermes has already finished is reconciled on reconnect.
	run2 := u.send(u.newChat(), "q2")
	_, fr2 := a.hx.runWithInput(t, "q2")
	recv(t, fr2.opens, "no") // worker subscribed
	a.svc.Close()
	store.RecoverRuns(context.Background(), a.db, time.Now())
	a.hx.setStatus(fr2, "completed", "finished while we were down")
	svc3, _ := New(Options{DB: a.db, Box: a.box, Hermes: cl})
	t.Cleanup(svc3.Close)
	a.h = rebuild(t, a, svc3)
	s3 := u.open(run2, "")
	<-s3.done
	if last := s3.events()[len(s3.events())-1]; last.Name != "run.completed" || !strings.Contains(last.Data, "finished while we were down") {
		t.Errorf("reconciled stream: %v", s3.names())
	}
}

func TestErrorMapping(t *testing.T) {
	type tc struct {
		name   string
		fail   map[string]int
		do     func(u *person, chat string) (*httpResult, string)
		status int
		code   string
	}
	send := func(u *person, chat string) (*httpResult, string) {
		w := u.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hi"})
		return &httpResult{w.Code, w.Body.String()}, ""
	}
	create := func(u *person, _ string) (*httpResult, string) {
		w := u.do("POST", "/api/chats", nil, map[string]string{})
		return &httpResult{w.Code, w.Body.String()}, ""
	}
	history := func(u *person, chat string) (*httpResult, string) {
		w := u.get("/api/chats/" + chat + "/messages")
		return &httpResult{w.Code, w.Body.String()}, ""
	}
	cases := []tc{
		{"create: upstream 503", map[string]int{"POST /api/sessions": 503}, create, 503, "HERMES_UNAVAILABLE"},
		{"create: upstream 500", map[string]int{"POST /api/sessions": 500}, create, 503, "HERMES_UNAVAILABLE"},
		{"create: upstream 429", map[string]int{"POST /api/sessions": 429}, create, 429, "RATE_LIMITED"},
		{"create: upstream 504", map[string]int{"POST /api/sessions": 504}, create, 504, "UPSTREAM_TIMEOUT"},
		{"create: upstream 401 (profile key refused)", map[string]int{"POST /api/sessions": 401}, create, 409, "TENANT_NOT_READY"},
		{"create: upstream 404 (no such route)", map[string]int{"POST /api/sessions": 404}, create, 501, "CAPABILITY_UNAVAILABLE"},
		{"history: upstream 503", map[string]int{"GET /api/sessions/": 503}, history, 503, "HERMES_UNAVAILABLE"},
		{"history: upstream 404", map[string]int{"GET /api/sessions/": 404}, history, 501, "CAPABILITY_UNAVAILABLE"},
	}
	_ = send
	for _, c := range cases {
		a := newApp(t)
		u := a.user("alice")
		chat := u.newChat()
		a.hx.mu.Lock()
		for k, v := range c.fail {
			a.hx.failNext[k] = v
		}
		a.hx.mu.Unlock()
		res, _ := c.do(u, chat)
		if res.code != c.status || !strings.Contains(res.body, `"code":"`+c.code+`"`) {
			t.Errorf("%s: %d %s", c.name, res.code, res.body)
		}
		for _, secret := range []string{u.key, a.hx.srv.URL, strings.TrimPrefix(a.hx.srv.URL, "http://"), u.profile, "/p/"} {
			if strings.Contains(res.body, secret) {
				t.Errorf("%s: response leaks %q: %s", c.name, secret, res.body)
			}
		}
	}
}

func TestUpstreamTimeoutAndUnreachableAreMapped(t *testing.T) {
	a := newApp(t, appOpts{client: hermes.ClientOptions{CallTimeout: 100 * time.Millisecond}})
	u := a.user("alice")
	chat := u.newChat()
	a.hx.mu.Lock()
	a.hx.hang["POST /api/sessions"] = true
	a.hx.mu.Unlock()
	w := u.do("POST", "/api/chats", nil, map[string]string{})
	if w.Code != 504 || errCode(t, w) != "UPSTREAM_TIMEOUT" {
		t.Errorf("hang: %d %s", w.Code, w.Body.String())
	}

	// A run whose creation times out may have started upstream: TIMED_OUT, not replayed.
	a.hx.mu.Lock()
	delete(a.hx.hang, "POST /api/sessions")
	a.hx.hang["POST /v1/runs"] = true
	a.hx.mu.Unlock()
	run := u.send(chat, "maybe started")
	eventually(t, func() bool { return u.runStatus(run) == store.RunTimedOut }, "TIMED_OUT")
	s := u.open(run, "")
	<-s.done
	if !s.has("run.failed") || s.has("run.completed") {
		t.Errorf("timed-out run stream: %v", s.names())
	}
	if n := countReqs(a.hx, "POST", "/v1/runs"); n != 1 {
		t.Errorf("run creation attempted %d times; a possibly-started run must not be replayed", n)
	}

	// Hermes down: connection refused.
	a.hx.srv.Close()
	w = u.do("POST", "/api/chats", nil, map[string]string{})
	if w.Code != 503 || errCode(t, w) != "HERMES_UNAVAILABLE" {
		t.Errorf("refused: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "127.0.0.1") {
		t.Errorf("address leaked: %s", w.Body.String())
	}
}

type httpResult struct {
	code int
	body string
}

func countReqs(f *fakeHermes, method, suffix string) int {
	n := 0
	for _, r := range f.requests() {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

func TestRunCreateFailureIsAVisibleFailedRun(t *testing.T) {
	for _, tc := range []struct {
		upstream int
		code     string
	}{{503, "HERMES_UNAVAILABLE"}, {401, "TENANT_NOT_READY"}, {429, "RATE_LIMITED"}, {404, "CAPABILITY_UNAVAILABLE"}} {
		a := newApp(t)
		u := a.user("alice")
		chat := u.newChat()
		a.hx.mu.Lock()
		a.hx.failNext["POST /v1/runs"] = tc.upstream
		a.hx.mu.Unlock()
		run := u.send(chat, "x")
		eventually(t, func() bool { return u.runStatus(run) == store.RunFailed }, "FAILED")
		s := u.open(run, "")
		<-s.done
		last := s.events()[len(s.events())-1]
		if last.Name != "run.failed" || !strings.Contains(last.Data, `"code":"`+tc.code+`"`) {
			t.Errorf("upstream %d: %v %s", tc.upstream, s.names(), last.Data)
		}
		if strings.Contains(last.Data, a.users["alice"].key) {
			t.Error("key in event")
		}
	}
}

// A consent Antares cannot collect is not granted and not left hanging.
func TestApprovalRequestFailsClosed(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "do something risky")
	_, fr := a.hx.runWithInput(t, "do something risky")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "approval.required", `{"request_id":"p1"}`)
	s.waitFor(t, "run.failed")
	if s.has("run.completed") {
		t.Error("completed")
	}
	if st := u.runStatus(run); st != store.RunFailed {
		t.Errorf("status %s", st)
	}
	recv(t, fr.ended, "stream closed")
	a.hx.mu.Lock()
	stopped := fr.stopped
	a.hx.mu.Unlock()
	if !stopped {
		t.Error("the run was left running upstream")
	}
	for _, r := range a.hx.requests() {
		if strings.HasSuffix(r.Path, "/approval") {
			t.Errorf("an approval decision was sent: %+v", r)
		}
	}
	if code := decodeBody[struct{ ErrorCode string }](t, u.get("/api/runs/"+run)).ErrorCode; code != "CAPABILITY_UNAVAILABLE" {
		t.Errorf("error code %q", code)
	}
}

func TestSendIsIdempotent(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	key := nextIdem()
	post := func() map[string]any {
		w := u.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": key}, map[string]string{"input": "once"})
		if w.Code != 202 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		return decodeBody[map[string]any](t, w)
	}
	r1, r2 := post(), post()
	if r1["runId"] != r2["runId"] {
		t.Errorf("a repeat created another run: %v %v", r1["runId"], r2["runId"])
	}
	_, fr := a.hx.runWithInput(t, "once")
	recv(t, fr.opens, "stream")
	if n := countReqs(a.hx, "POST", "/v1/runs"); n != 1 {
		t.Errorf("Hermes received %d run creations", n)
	}
	// Malformed or missing keys are refused before anything is admitted.
	for _, k := range []string{"", "short", strings.Repeat("a", 200), "has space in it"} {
		w := u.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": k}, map[string]string{"input": "x"})
		if w.Code != 400 {
			t.Errorf("key %q: %d", k, w.Code)
		}
	}
}

func TestSendValidationAndLimits(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	h := func() map[string]string { return map[string]string{"Idempotency-Key": nextIdem()} }
	for name, body := range map[string]any{
		"empty": map[string]string{"input": ""}, "blank": map[string]string{"input": "  \n "},
		"too long": map[string]string{"input": strings.Repeat("a", maxInput+1)},
		"not json": "{nope", "array": "[1]", "two objects": `{"input":"a"}{"input":"b"}`, "missing": map[string]string{},
		"wrong type": `{"input": 5}`,
	} {
		if w := u.do("POST", "/api/chats/"+chat+"/messages", h(), body); w.Code != 400 && w.Code != 413 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := u.do("POST", "/api/chats/"+chat+"/messages", h(), `{"input":"`+strings.Repeat("x", maxBodyBytes+10)+`"}`); w.Code != 413 {
		t.Errorf("huge body: %d", w.Code)
	}
	if n := countReqs(a.hx, "POST", "/v1/runs"); n != 0 {
		t.Errorf("%d invalid sends reached Hermes", n)
	}
	t.Run("CSRF preserves quotas", func(t *testing.T) {
		for _, tc := range []struct {
			name           string
			runs, messages int
		}{
			{"runs", 2, 3}, {"messages", 3, 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				limits := auth.DefaultLimits()
				limits.RunsPerMinute, limits.MessagesPerMinute = tc.runs, tc.messages
				a := newApp(t, appOpts{limits: &limits})
				victim := a.user("victim")
				path := "/api/chats/" + victim.newChat() + "/messages"
				body := map[string]string{"input": "quota probe"}
				key := nextIdem()
				before := len(a.hx.requests())
				w := victim.do("POST", path, map[string]string{"Idempotency-Key": key, "Origin": "https://evil.example"}, body)
				if w.Code != 403 {
					t.Fatalf("cross-origin send: %d %s", w.Code, w.Body.String())
				}
				if len(a.hx.requests()) != before {
					t.Fatal("cross-origin send reached Hermes")
				}
				for i := 0; i < min(limits.RunsPerMinute, limits.MessagesPerMinute); i++ {
					w = victim.do("POST", path, map[string]string{"Idempotency-Key": key}, body)
					if w.Code != 202 {
						t.Fatalf("quota after cross-origin refusal: send %d got %d %s", i+1, w.Code, w.Body.String())
					}
				}
				requireLimit(t, victim.do("POST", path, map[string]string{"Idempotency-Key": key}, body))
			})
		}
	})
	// A malformed id is a 404 like an unknown one.
	for _, id := range []string{"nope", "../x", strings.Repeat("0", 31), strings.ToUpper(strings.Repeat("a", 32))} {
		if w := u.get("/api/runs/" + id); w.Code != 404 {
			t.Errorf("run id %q: %d", id, w.Code)
		}
	}
}

func TestConcurrentRunCap(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	// Fill the concurrency cap with runs that never finish.
	var chats []string
	for i := 0; i < maxActiveRunsPerUser+1; i++ {
		chats = append(chats, u.newChat())
	}
	for i := 0; i < maxActiveRunsPerUser; i++ {
		u.send(chats[i], "stuck")
	}
	w := u.do("POST", "/api/chats/"+chats[maxActiveRunsPerUser]+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x"})
	if w.Code != 429 || errCode(t, w) != "RATE_LIMITED" {
		t.Errorf("over the concurrency cap: %d %s", w.Code, w.Body.String())
	}
	var limitBody map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &limitBody); err != nil {
		t.Fatal(err)
	}
	retry, exists := limitBody["error"]["retryAfterSeconds"]
	if !exists || retry != nil || w.Header().Get("Retry-After") != "" {
		t.Fatalf("unknown run wait must be null with no Retry-After: %v %s", w.Header(), w.Body.String())
	}
	// One chat, one run at a time.
	w = u.do("POST", "/api/chats/"+chats[0]+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x"})
	if w.Code != 409 || errCode(t, w) != "RUN_CONFLICT" {
		t.Errorf("second run on a busy chat: %d %s", w.Code, w.Body.String())
	}

}

// Default deny: when the database says no, no Hermes request is made — not a
// degraded one, not a cached one.
func TestGateDeniesBeforeAnyHermesRequest(t *testing.T) {
	cases := map[string]string{
		"binding never verified": `UPDATE tenant_bindings SET verified_at = NULL WHERE user_id = ?`,
		"binding failed":         `UPDATE tenant_bindings SET status = 'FAILED' WHERE user_id = ?`,
		"tenant disabled":        `UPDATE users SET tenant_status = 'DISABLED' WHERE id = ?`,
		"account suspended":      `UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`,
		"ciphertext unopenable":  `UPDATE tenant_bindings SET credential_ciphertext = x'00' WHERE user_id = ?`,
		"binding row removed":    `DELETE FROM chats WHERE user_id = ?; DELETE FROM tenant_bindings WHERE user_id = ?`,
	}
	for name, q := range cases {
		a := newApp(t)
		u := a.user("alice")
		chat := u.newChat()
		before := len(a.hx.requests())
		for _, stmt := range strings.Split(q, ";") {
			if _, err := a.db.Exec(stmt, u.id); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		for _, call := range []struct{ method, path string }{
			{"POST", "/api/chats"},
			{"GET", "/api/chats/" + chat + "/messages"},
			{"POST", "/api/chats/" + chat + "/messages"},
		} {
			w := u.do(call.method, call.path, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x"})
			if w.Code < 400 {
				t.Errorf("%s: %s %s = %d", name, call.method, call.path, w.Code)
			}
		}
		if n := len(a.hx.requests()); n != before {
			t.Errorf("%s: %d Hermes request(s) after the gate said no", name, n-before)
		}
	}
}

// Revocation mid-run: the account is suspended while a run streams. The stream
// ends, the cancel still records CANCELLED, and no credential is used for the
// stop (the gate says no) — the response says the upstream may still be running.
func TestSuspensionMidRunEndsTheStreamAndDeniesTheStop(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	recv(t, fr.opens, "worker's stream")
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	if w := u.get("/api/runs/" + run); w.Code != 403 {
		t.Errorf("suspended user reads a run: %d", w.Code)
	}
	if w := u.get("/api/runs/" + run + "/events"); w.Code != 403 {
		t.Errorf("suspended user opens the stream: %d", w.Code)
	}
	// An operator-side cancel through the service (the revocation path) cannot use the key.
	before := len(a.hx.requests())
	r, stopped, err := a.svc.Cancel(context.Background(), u.id, run)
	if err != nil || r.Status != store.RunCancelled || stopped {
		t.Errorf("cancel: %+v stopped=%v err=%v", r, stopped, err)
	}
	for _, rq := range a.hx.requests()[before:] {
		if rq.Method == "POST" {
			t.Errorf("a stop was sent for a suspended account: %+v", rq)
		}
	}
}

func TestOpenStreamClosesWhenTheAccountIsSuspended(t *testing.T) {
	a := newApp(t, appOpts{heartbeat: 20 * time.Millisecond})
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	s := u.open(run, "")
	recv(t, fr.opens, "stream")
	s.waitFor(t, "run.started") // the browser is connected
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived the suspension")
	}
	if !s.has("run.disconnected") || s.has("run.completed") {
		t.Errorf("events: %v", s.names())
	}
}

// #150: a session ended while its stream is open (sign-out here, sign-out everywhere on another device, or any
// revocation) ends the stream at the next heartbeat, as a suspension does, and the run is not reported completed.
func TestOpenStreamClosesWhenTheSessionIsRevoked(t *testing.T) {
	a := newApp(t, appOpts{heartbeat: 20 * time.Millisecond})
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	s := u.open(run, "")
	recv(t, fr.opens, "stream")
	s.waitFor(t, "run.started")
	if _, err := store.RevokeUserSessions(a.svc.base, a.db, u.id, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived the session")
	}
	if !s.has("run.disconnected") || s.has("run.completed") {
		t.Errorf("events: %v", s.names())
	}
}

// Review #186: revocation followed at once by completion. The terminal event lands before any heartbeat (which is
// set far away here), and the revoked session still gets run.disconnected, never run.completed.
func TestRevokedSessionGetsNoTerminalEventEvenBeforeAHeartbeat(t *testing.T) {
	a := newApp(t, appOpts{heartbeat: time.Hour})
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	s := u.open(run, "")
	recv(t, fr.opens, "stream")
	s.waitFor(t, "run.started")
	if _, err := store.RevokeUserSessions(a.svc.base, a.db, u.id, time.Now()); err != nil {
		t.Fatal(err)
	}
	a.hx.push(fr, "run.completed", `{"output":"after revocation"}`)
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end")
	}
	if s.has("run.completed") || !s.has("run.disconnected") || strings.Contains(s.text(), "after revocation") {
		t.Errorf("a revoked session received %v", s.names())
	}
}

func TestMessagesAreProxiedFromTheOwnedSessionOnly(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chatA := alice.newChat()
	chatB := bob.newChat()
	sessA := decodeSession(t, a, alice, chatA)
	sessB := decodeSession(t, a, bob, chatB)
	a.hx.mu.Lock()
	a.hx.msgs[sessA] = []map[string]any{{"id": "m1", "role": "user", "content": "hi"}, {"id": "m2", "role": "assistant", "content": "<script>alert(1)</script>"}}
	a.hx.msgs[sessB] = []map[string]any{{"id": "m9", "role": "user", "content": "bob's private text"}}
	a.hx.mu.Unlock()

	w := alice.get("/api/chats/" + chatA + "/messages")
	m := decodeBody[struct {
		Messages  []hermes.Message
		Truncated bool
	}](t, w)
	if w.Code != 200 || len(m.Messages) != 2 || m.Messages[1].Text != "<script>alert(1)</script>" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "bob's private text") {
		t.Error("another user's transcript was returned")
	}
	for _, r := range a.hx.requests() {
		if strings.HasSuffix(r.Path, "/messages") && !strings.HasPrefix(r.Path, "/p/"+alice.profile+"/api/sessions/"+sessA) {
			t.Errorf("transcript read from the wrong place: %s", r.Path)
		}
	}
	if w := alice.get("/api/chats/" + chatB + "/messages"); w.Code != 404 {
		t.Errorf("alice read bob's transcript: %d", w.Code)
	}
}

func decodeSession(t *testing.T, a *app, p *person, chatID string) string {
	t.Helper()
	var sid string
	if err := a.db.QueryRow(`SELECT hermes_session_id FROM chats WHERE id = ? AND user_id = ?`, chatID, p.id).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

// No credential appears in any response, log line or stored error.
func TestNoCredentialInResponsesLogsOrStoredState(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	secrets := []string{alice.key, string(a.box.Fingerprint(alice.key)), alice.cookie}

	var bodies []string
	collect := func(w interface{ String() string }) { bodies = append(bodies, w.String()) }
	// Success and failure paths, including upstream errors that echo the key.
	a.hx.mu.Lock()
	a.hx.failNext["POST /v1/runs"] = 503
	a.hx.mu.Unlock()
	run := alice.send(chat, "x")
	eventually(t, func() bool { return alice.runStatus(run) == store.RunFailed }, "failed")
	s := alice.open(run, "")
	<-s.done
	bodies = append(bodies, s.buf.String())
	for _, p := range []string{"/api/chats", "/api/chats/" + chat, "/api/runs/" + run, "/api/chats/" + chat + "/messages", "/api/me"} {
		w := alice.get(p)
		collect(w.Body)
	}
	a.hx.mu.Lock()
	a.hx.failNext["POST /api/sessions"] = 500
	a.hx.mu.Unlock()
	collect(alice.do("POST", "/api/chats", nil, map[string]string{}).Body)
	bodies = append(bodies, logBuf.String())

	var stored []string
	rows, _ := a.db.Query(`SELECT COALESCE(error_code,'') || status FROM runs`)
	for rows.Next() {
		var v string
		rows.Scan(&v)
		stored = append(stored, v)
	}
	rows.Close()
	bodies = append(bodies, stored...)

	for _, b := range bodies {
		for _, sec := range secrets {
			if sec != "" && strings.Contains(b, sec) {
				t.Errorf("secret %q... appears in %q", sec[:8], truncate(b))
			}
		}
		if strings.Contains(b, a.hx.srv.URL) || strings.Contains(b, "127.0.0.1") {
			t.Errorf("upstream address appears in %q", truncate(b))
		}
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// The HTTP server must be able to hold a stream open past its read timeout and
// release it on shutdown; the handler checks both through the real net/http stack.
func TestStreamSurvivesOverRealHTTPAndShutdownEndsIt(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "q")
	_, fr := a.hx.runWithInput(t, "q")
	recv(t, fr.opens, "worker stream")

	srv := newHTTPServer(a.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/runs/"+run+"/events", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-antares_session", Value: u.cookie})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	time.Sleep(900 * time.Millisecond) // three times the server's ReadTimeout
	a.hx.push(fr, "assistant.delta", `{"delta":"over the wire"}`)
	buf := make([]byte, 4096)
	var got string
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(got, "over the wire") && time.Now().Before(deadline) {
		n, _ := resp.Body.Read(buf)
		got += string(buf[:n])
	}
	if !strings.Contains(got, "event: assistant.delta") || !strings.Contains(got, "over the wire") {
		t.Errorf("stream body: %q", got)
	}
	a.svc.Close() // shutdown: the stream must end rather than hold the drain
	done := make(chan struct{})
	go func() {
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream still open after shutdown")
	}
}

func TestRateLimitedStatusInvariant(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RATE_LIMITED with a non-429 status was silently accepted")
		}
	}()
	apiError(httptest.NewRecorder(), http.StatusServiceUnavailable, hermes.CodeRateLimited, "test limit")
}
