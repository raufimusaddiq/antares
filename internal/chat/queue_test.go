package chat

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

type queueAnswer struct {
	Items []struct {
		ID, Input, Status string
		Position          int
	}
	Paused  string
	Started *struct{ RunID, Input string }
}

func (p *person) queue(chatID string) queueAnswer {
	p.a.t.Helper()
	w := p.get("/api/chats/" + chatID + "/queue")
	if w.Code != 200 {
		p.a.t.Fatalf("queue: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[queueAnswer](p.a.t, w)
}

func (p *person) enqueue(chatID, text string) queueAnswer {
	p.a.t.Helper()
	w := p.do("POST", "/api/chats/"+chatID+"/queue", nil, map[string]string{"input": text})
	if w.Code != 201 {
		p.a.t.Fatalf("enqueue: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[queueAnswer](p.a.t, w)
}

// hermesRuns counts the runs Hermes was asked to start with this input.
func hermesRuns(a *app, input string) int {
	n := 0
	for _, r := range a.hx.requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/v1/runs") && strings.Contains(r.Body, `"input":"`+input+`"`) {
			n++
		}
	}
	return n
}

// QUEUE-001: a message queued behind a running reply waits, shows its place, and is sent exactly once
// after the reply completes, as a new turn under its stable key.
func TestQueuedMessageIsSentOnceAfterTheRunCompletes(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "first")
	_, fr := a.hx.runWithInput(t, "first")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")

	q := u.enqueue(chat, "second")
	if len(q.Items) != 1 || q.Items[0].Position != 1 || q.Items[0].Status != "QUEUED" || q.Paused != "" {
		t.Fatalf("queued: %+v", q)
	}
	u.enqueue(chat, "third")
	if q := u.queue(chat); len(q.Items) != 2 || q.Items[1].Input != "third" || q.Items[1].Position != 2 {
		t.Fatalf("queue: %+v", q)
	}
	if hermesRuns(a, "second") != 0 {
		t.Fatal("a queued message reached Hermes while the run was still going")
	}

	a.hx.push(fr, "run.completed", `{"output":"done"}`)
	s.waitFor(t, "run.completed")
	_, fr2 := a.hx.runWithInput(t, "second")
	if hermesRuns(a, "second") != 1 || hermesRuns(a, "third") != 0 {
		t.Fatalf("after the first reply: second=%d third=%d", hermesRuns(a, "second"), hermesRuns(a, "third"))
	}
	if q := u.queue(chat); len(q.Items) != 1 || q.Items[0].Input != "third" || q.Items[0].Position != 1 {
		t.Fatalf("queue after dispatch: %+v", q)
	}
	var idem string
	for _, r := range a.hx.requests() {
		if strings.Contains(r.Body, `"input":"second"`) {
			idem = r.Idem
		}
	}
	if !strings.HasPrefix(idem, "antares-") {
		t.Errorf("the queued turn was not sent as a normal run: idempotency %q", idem)
	}
	recv(t, fr2.opens, "second run's stream")
	a.hx.push(fr2, "run.completed", `{"output":"ok"}`)
	a.hx.runWithInput(t, "third")
	eventually(t, func() bool { return len(u.queue(chat).Items) == 0 }, "the queue to drain")
	if hermesRuns(a, "second") != 1 || hermesRuns(a, "third") != 1 {
		t.Errorf("each queued message must be sent exactly once: second=%d third=%d", hermesRuns(a, "second"), hermesRuns(a, "third"))
	}
}

// QUEUE-003: a failed, cancelled or interrupted reply pauses the queue; nothing is sent until the
// owner resumes it or sends the next message by hand.
func TestQueuePausesWhenTheRunDoesNotComplete(t *testing.T) {
	for _, tc := range []struct{ frame, want string }{
		{"run.failed", store.RunFailed},
		{"run.cancelled", store.RunCancelled},
	} {
		t.Run(tc.frame, func(t *testing.T) {
			a := newApp(t)
			u := a.user("alice")
			chat := u.newChat()
			run := u.send(chat, "first")
			_, fr := a.hx.runWithInput(t, "first")
			s := u.open(run, "")
			recv(t, fr.opens, "upstream stream")
			u.enqueue(chat, "next")
			a.hx.push(fr, tc.frame, `{}`)
			eventually(t, func() bool { return s.has("run.failed") || s.has("run.cancelled") }, "terminal event")
			eventually(t, func() bool { return u.queue(chat).Paused == tc.want }, "the queue to pause")
			time.Sleep(100 * time.Millisecond)
			if hermesRuns(a, "next") != 0 {
				t.Fatal("a paused queue sent on its own")
			}
			// Resuming is the explicit recovery: the free chat sends its head.
			if w := u.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]bool{"paused": false}); w.Code != 200 {
				t.Fatalf("resume: %d %s", w.Code, w.Body.String())
			}
			a.hx.runWithInput(t, "next")
			if q := u.queue(chat); len(q.Items) != 0 || q.Paused != "" {
				t.Errorf("after resume: %+v", q)
			}
		})
	}
}

// The user's own cancel pauses the queue too.
func TestCancelPausesTheQueue(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "first")
	_, fr := a.hx.runWithInput(t, "first")
	recv(t, fr.opens, "upstream stream")
	u.enqueue(chat, "next")
	if w := u.do("POST", "/api/runs/"+run+"/cancel", nil, nil); w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if q := u.queue(chat); q.Paused != store.RunCancelled || len(q.Items) != 1 {
		t.Errorf("after cancel: %+v", q)
	}
}

// "Send next" only after the previous run ended; while it runs it is RUN_CONFLICT and the message stays
// queued without pausing. From a paused queue it sends and the queue stays paused.
func TestSendNextWaitsForTheRunAndWorksWhenPaused(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "first")
	_, fr := a.hx.runWithInput(t, "first")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	u.enqueue(chat, "next")
	if w := u.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]bool{"paused": true}); w.Code != 200 {
		t.Fatalf("pause: %d", w.Code)
	}
	if w := u.do("POST", "/api/chats/"+chat+"/queue/next", nil, nil); w.Code != 409 || errCode(t, w) != "RUN_CONFLICT" {
		t.Fatalf("send next during a run: %d %s", w.Code, w.Body.String())
	}
	if q := u.queue(chat); len(q.Items) != 1 || q.Items[0].Status != "QUEUED" || q.Paused != "USER" {
		t.Fatalf("after a refused send next: %+v", q)
	}
	a.hx.push(fr, "run.completed", `{"output":"done"}`)
	s.waitFor(t, "run.completed")
	time.Sleep(100 * time.Millisecond)
	if hermesRuns(a, "next") != 0 {
		t.Fatal("a paused queue sent on its own")
	}
	w := u.do("POST", "/api/chats/"+chat+"/queue/next", nil, nil)
	if w.Code != 202 {
		t.Fatalf("send next: %d %s", w.Code, w.Body.String())
	}
	a.hx.runWithInput(t, "next")
	if q := u.queue(chat); len(q.Items) != 0 || q.Paused != "USER" {
		t.Errorf("after send next: %+v", q)
	}
	if w := u.do("POST", "/api/chats/"+chat+"/queue/next", nil, nil); w.Code != 409 || errCode(t, w) != "QUEUE_EMPTY" {
		t.Errorf("send next on an empty queue: %d %s", w.Code, w.Body.String())
	}
}

// LLM-ACC-010: two tabs pressing "Send next" at once send the message once.
func TestConcurrentSendNextDispatchesOnce(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	if w := u.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]bool{"paused": true}); w.Code != 200 {
		t.Fatalf("pause: %d", w.Code)
	}
	u.enqueue(chat, "only once")
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := u.do("POST", "/api/chats/"+chat+"/queue/next", nil, nil)
			mu.Lock()
			codes[w.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	a.hx.runWithInput(t, "only once")
	if codes[202] != 1 || codes[409] != 5 {
		t.Errorf("answers %v, want one 202 and five 409", codes)
	}
	time.Sleep(100 * time.Millisecond)
	if n := hermesRuns(a, "only once"); n != 1 {
		t.Errorf("Hermes was asked %d times", n)
	}
}

// Negative IDOR (security-invariants §4): B cannot see or change A's queue, by chat or by item id.
func TestQueueRoutesAreOwnerScoped(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bob")
	chat := alice.newChat()
	if w := alice.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]bool{"paused": true}); w.Code != 200 {
		t.Fatalf("pause: %d", w.Code)
	}
	item := alice.enqueue(chat, "private").Items[0].ID
	base := "/api/chats/" + chat + "/queue"
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", base, nil},
		{"POST", base, map[string]string{"input": "x"}},
		{"PATCH", base, map[string]bool{"paused": false}},
		{"POST", base + "/next", nil},
		{"PATCH", base + "/" + item, map[string]string{"input": "x"}},
		{"DELETE", base + "/" + item, nil},
	} {
		if w := bob.do(c.method, c.path, nil, c.body); w.Code != 404 {
			t.Errorf("bob %s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	// Bob's own chat cannot address Alice's item either.
	own := bob.newChat()
	if w := bob.do("DELETE", "/api/chats/"+own+"/queue/"+item, nil, nil); w.Code != 404 {
		t.Errorf("bob deletes alice's item via his chat: %d", w.Code)
	}
	if q := alice.queue(chat); len(q.Items) != 1 || q.Items[0].Input != "private" || q.Paused != "USER" {
		t.Errorf("alice's queue changed: %+v", q)
	}
	if hermesRuns(a, "private") != 0 || hermesRuns(a, "x") != 0 {
		t.Error("bob's requests reached Hermes")
	}
}

func TestQueueRejectsBadInput(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	for name, body := range map[string]any{
		"empty":        map[string]string{"input": "  "},
		"too long":     map[string]string{"input": strings.Repeat("a", maxInput+1)},
		"server field": map[string]string{"input": "x", "profile": "u_other"},
	} {
		if w := u.do("POST", "/api/chats/"+chat+"/queue", nil, body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if w := u.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]any{}); w.Code != 400 {
		t.Errorf("pause without paused: %d", w.Code)
	}
}

// Review #177: a message queued while the reply is running is claimed at once and refused (busy chat). If
// the reply ends before that claim is released, the end-of-run dispatch finds the claim in flight and stands
// down; the releaser must then look again, or the unpaused queue is stranded with nothing to wake it.
func TestQueueIsNotStrandedWhenTheRunEndsDuringARefusedClaim(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	run := u.send(chat, "first")
	_, fr := a.hx.runWithInput(t, "first")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	once := sync.Once{}
	a.svc.queueConflictHook = func() {
		once.Do(func() {
			a.hx.push(fr, "run.completed", `{"output":"done"}`)
			s.waitFor(t, "run.completed") // emitted after the end-of-run dispatch, which saw this claim and stood down
		})
	}
	q := u.enqueue(chat, "second")
	_, fr2 := a.hx.runWithInput(t, "second")
	if q.Started == nil || q.Started.Input != "second" || len(q.Items) != 0 {
		t.Errorf("enqueue answer: %+v", q)
	}
	recv(t, fr2.opens, "second run's stream")
	if n := hermesRuns(a, "second"); n != 1 {
		t.Errorf("Hermes was asked %d times", n)
	}
}

// Review #177: resuming a free chat's queue starts its head and says which run, so the browser can follow it.
func TestResumeReportsTheRunItStarted(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	if w := u.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]bool{"paused": true}); w.Code != 200 {
		t.Fatalf("pause: %d", w.Code)
	}
	if q := u.enqueue(chat, "waiting"); q.Started != nil {
		t.Fatalf("a paused queue started a run on enqueue: %+v", q)
	}
	w := u.do("PATCH", "/api/chats/"+chat+"/queue", nil, map[string]bool{"paused": false})
	if w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	q := decodeBody[queueAnswer](t, w)
	if q.Started == nil || q.Started.Input != "waiting" || q.Started.RunID == "" || len(q.Items) != 0 {
		t.Fatalf("resume answer: %+v", q)
	}
	a.hx.runWithInput(t, "waiting")
	if st := u.runStatus(q.Started.RunID); st == "" {
		t.Error("the reported run is not the caller's")
	}
}
