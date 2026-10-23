package chat

import (
	"context"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// The usage block is the shape Hermes sends (api_server_runs.py _USAGE_FIELDS);
// the values are fabricated.
const usageFrame = `{"usage":{"input_tokens":1000,"output_tokens":200,"total_tokens":1200,"cache_read_tokens":400,"cache_write_tokens":0},"runtime":{"provider":"custom","model":"deepseek-v4-1-flash"}}`

type usageBody struct {
	Tokens *struct{ Input, Output, CacheRead, CacheWrite, Total int64 }
	Runs   struct{ Finished, Reported int }
	Cost   struct {
		Estimated     bool
		Currency      string
		Amount        *float64
		UnpricedRuns  int
		PriceListDate string
	}
	Context struct{ WindowTokens, CarriedTokens *int64 }
	Budget  struct {
		LimitTokens, UsedTokens int64
		Reached                 bool
	}
}

func (p *person) usage(chatID string) *httptestResult {
	w := p.get("/api/chats/" + chatID + "/usage")
	r := &httptestResult{code: w.Code}
	if w.Code == 200 {
		r.b = decodeBody[usageBody](p.a.t, w)
	}
	return r
}

type httptestResult struct {
	code int
	b    usageBody
}

// runWithUsage sends a message and ends the run with a run.completed frame.
func runWithUsage(t *testing.T, a *app, p *person, chatID, text, frame string) string {
	t.Helper()
	runID := p.send(chatID, text)
	_, fr := a.hx.runWithInput(t, text)
	s := p.open(runID, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", frame)
	s.waitFor(t, "run.completed")
	s.close()
	return runID
}

// Price list kenari.id/v1/models read 2026-10-23: deepseek-v4-1-flash is
// 2750 IDR per 1M input, 5500 per 1M output, 65 per 1M cache-read tokens.
func TestEstimateMatchesPriceListOf20261023(t *testing.T) {
	p := prices["deepseek-v4-1-flash"]
	got := estimateIDR(p, store.RunUsage{Input: 1_000_000, Output: 1_000_000, CacheRead: 1_000_000})
	// input includes the cache reads: 0 fresh input + 1M output + 1M cache read.
	if want := 5500.0 + 65.0; math.Abs(got-want) > 1e-6 {
		t.Fatalf("cache-only input: got %v IDR, want %v", got, want)
	}
	got = estimateIDR(p, store.RunUsage{Input: 1_000_000, Output: 100_000, CacheRead: 200_000})
	if want := 800_000*2750.0/1e6 + 100_000*5500.0/1e6 + 200_000*65.0/1e6; math.Abs(got-want) > 1e-6 || math.Abs(want-2763) > 1e-6 {
		t.Fatalf("mixed: got %v IDR, want %v (=2763)", got, want)
	}
	if priceListDate != "2026-10-23" || priceCurrency != "IDR" {
		t.Fatal("the price list date and currency are part of what the numbers mean")
	}
}

func TestUsageFromHermesAndLabelledEstimate(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	runWithUsage(t, a, alice, chat, "q1", usageFrame)
	runWithUsage(t, a, alice, chat, "q2", usageFrame)

	u := alice.usage(chat)
	if u.code != 200 || u.b.Tokens == nil {
		t.Fatalf("usage: %d %+v", u.code, u.b)
	}
	if *u.b.Tokens != (struct{ Input, Output, CacheRead, CacheWrite, Total int64 }{2000, 400, 800, 0, 2400}) {
		t.Fatalf("tokens: %+v", *u.b.Tokens)
	}
	if u.b.Runs.Finished != 2 || u.b.Runs.Reported != 2 {
		t.Fatalf("runs: %+v", u.b.Runs)
	}
	want := 2 * (600*2750.0/1e6 + 200*5500.0/1e6 + 400*65.0/1e6)
	if !u.b.Cost.Estimated || u.b.Cost.Currency != "IDR" || u.b.Cost.Amount == nil || math.Abs(*u.b.Cost.Amount-want) > 1e-9 || u.b.Cost.PriceListDate != "2026-10-23" {
		t.Fatalf("cost: %+v want %v", u.b.Cost, want)
	}
	if u.b.Context.CarriedTokens != nil || u.b.Context.WindowTokens == nil || *u.b.Context.WindowTokens != 1_000_000 {
		t.Fatalf("context: carried must be unknown (Hermes does not report it), window is the provider's: %+v", u.b.Context)
	}
}

// When the terminal frame carries no usage, the poll (GET /v1/runs/{id}) is read.
func TestUsageFallsBackToThePoll(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	runID := alice.send(chat, "polled")
	_, fr := a.hx.runWithInput(t, "polled")
	s := alice.open(runID, "")
	recv(t, fr.opens, "stream")
	a.hx.mu.Lock()
	fr.usage = `{"input_tokens":50,"output_tokens":7,"total_tokens":57,"cache_read_tokens":0,"cache_write_tokens":0}`
	fr.status = "completed"
	a.hx.mu.Unlock()
	a.hx.push(fr, "run.completed", `{}`)
	s.waitFor(t, "run.completed")
	s.close()
	u := alice.usage(chat)
	if u.b.Tokens == nil || u.b.Tokens.Total != 57 {
		t.Fatalf("polled usage not recorded: %+v", u.b)
	}
	// No model reported: tokens are shown, the cost is not guessed.
	if u.b.Cost.Amount != nil || u.b.Cost.UnpricedRuns != 1 {
		t.Fatalf("unpriced run must not get a cost: %+v", u.b.Cost)
	}
}

// Hermes reports 0 for a counter it did not have; that is "unknown", not "free".
func TestUnreportedUsageIsUnknownNotZero(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	runWithUsage(t, a, alice, chat, "silent", `{"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	u := alice.usage(chat)
	if u.code != 200 || u.b.Tokens != nil || u.b.Cost.Amount != nil || u.b.Runs.Finished != 1 || u.b.Runs.Reported != 0 {
		t.Fatalf("want unknown, got %d %+v", u.code, u.b)
	}
}

// One user's usage is never attributed to, or readable through, another user's chat.
func TestUsageCannotCrossUsers(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chatA, chatB := alice.newChat(), bob.newChat()
	runA := runWithUsage(t, a, alice, chatA, "alice q", usageFrame)

	if w := bob.get("/api/chats/" + chatA + "/usage"); w.Code != 404 {
		t.Fatalf("bob reading alice's usage: %d", w.Code)
	}
	if u := bob.usage(chatB); u.code != 200 || u.b.Tokens != nil || u.b.Runs.Finished != 0 {
		t.Fatalf("bob's chat picked up alice's usage: %+v", u.b)
	}
	// The store itself refuses: bob's id with alice's run or chat touches nothing.
	ctx := context.Background()
	if err := store.RecordRunUsage(ctx, a.db, bob.id, runA, store.RunUsage{Input: 9, Output: 9}); err != nil {
		t.Fatal(err)
	}
	cu, err := store.GetChatUsage(ctx, a.db, bob.id, chatA)
	if err != nil || cu.Finished != 0 || len(cu.ByModel) != 0 {
		t.Fatalf("bob via the store: %+v %v", cu, err)
	}
	if u := alice.usage(chatA); u.b.Tokens == nil || u.b.Tokens.Input != 1000 {
		t.Fatalf("bob's write changed alice's usage: %+v", u.b)
	}
}

// The behaviour at the limit is asserted: used < limit admits, used >= limit refuses.
func TestBudgetBoundary(t *testing.T) {
	cases := []struct {
		name string
		used int64
		want int
	}{
		{"one token under", chatTokenBudget - 1, 202},
		{"exactly at the limit", chatTokenBudget, 403},
		{"over the limit", chatTokenBudget + 500, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t)
			alice := a.user("alice")
			chat := alice.newChat()
			runID := runWithUsage(t, a, alice, chat, "seed", usageFrame)
			if err := store.RecordRunUsage(context.Background(), a.db, alice.id, runID, store.RunUsage{Input: c.used - 10, Output: 10, Model: "deepseek-v4-1-flash"}); err != nil {
				t.Fatal(err)
			}
			w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "next"})
			if w.Code != c.want {
				t.Fatalf("used %d: send got %d %s, want %d", c.used, w.Code, w.Body.String(), c.want)
			}
			if c.want == 403 {
				if errCode(t, w) != "BUDGET_REACHED" {
					t.Fatalf("code %q", errCode(t, w))
				}
				if u := alice.usage(chat); !u.b.Budget.Reached || u.b.Budget.UsedTokens != c.used {
					t.Fatalf("budget view: %+v", u.b.Budget)
				}
			}
			// Another user's chat is unaffected by this chat's budget.
			bob := a.user("bobby")
			if bw := bob.do("POST", "/api/chats/"+bob.newChat()+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "bob"}); bw.Code != 202 {
				t.Fatalf("bob blocked by alice's budget: %d", bw.Code)
			}
		})
	}
}

// A run Hermes finished while Antares was down is discovered by attach(); its
// usage must be recorded there too, or the budget is bypassable.
func TestUsageRecordedWhenAttachFindsTheRunFinished(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	run := alice.send(chat, "q")
	_, fr := a.hx.runWithInput(t, "q")
	recv(t, fr.opens, "worker subscribed")
	a.svc.Close()
	if _, _, err := store.RecoverRuns(context.Background(), a.db, time.Now()); err != nil {
		t.Fatal(err)
	}
	a.hx.mu.Lock()
	fr.usage = `{"input_tokens":300,"output_tokens":20,"total_tokens":320,"cache_read_tokens":0,"cache_write_tokens":0}`
	a.hx.mu.Unlock()
	a.hx.setStatus(fr, "completed", "done while down")
	cl, _ := hermes.NewClient(a.hx.srv.URL, hermes.ClientOptions{})
	svc2, err := New(Options{DB: a.db, Box: a.box, Hermes: cl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc2.Close)
	a.h = rebuild(t, a, svc2)
	s := alice.open(run, "")
	<-s.done
	if u := alice.usage(chat); u.b.Tokens == nil || u.b.Tokens.Total != 320 {
		t.Fatalf("usage lost on the attach path: %+v", u.b)
	}
}

// A replay of an old Idempotency-Key at the limit returns the original run; a fresh key is refused.
func TestBudgetReplayReturnsTheOriginalRun(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	idem := nextIdem()
	post := func(key string) *httptest.ResponseRecorder {
		return alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": key}, map[string]string{"input": "q"})
	}
	first := post(idem)
	if first.Code != 202 {
		t.Fatalf("first send: %d", first.Code)
	}
	runID := decodeBody[struct{ RunID string }](t, first).RunID
	_, fr := a.hx.runWithInput(t, "q")
	s := alice.open(runID, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", usageFrame)
	s.waitFor(t, "run.completed")
	s.close()
	if err := store.RecordRunUsage(context.Background(), a.db, alice.id, runID, store.RunUsage{Input: chatTokenBudget, Output: 1, Model: "deepseek-v4-1-flash"}); err != nil {
		t.Fatal(err)
	}
	if w := post(idem); w.Code != 202 || decodeBody[struct{ RunID string }](t, w).RunID != runID {
		t.Fatalf("replay at the limit: %d %s", w.Code, w.Body.String())
	}
	if w := post(nextIdem()); w.Code != 403 {
		t.Fatalf("fresh key at the limit: %d", w.Code)
	}
}

// A cancelled run still spent tokens; send-cancel-repeat must not keep the budget at zero.
func TestCancelledRunUsageIsCounted(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	run := alice.send(chat, "cancel me")
	_, fr := a.hx.runWithInput(t, "cancel me")
	recv(t, fr.opens, "stream")
	a.hx.mu.Lock()
	fr.usage = `{"input_tokens":700,"output_tokens":50,"total_tokens":750,"cache_read_tokens":0,"cache_write_tokens":0}`
	a.hx.mu.Unlock()
	if w := alice.do("POST", "/api/runs/"+run+"/cancel", nil, nil); w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if u := alice.usage(chat); u.b.Tokens == nil || u.b.Tokens.Total != 750 || u.b.Budget.UsedTokens != 750 {
		t.Fatalf("cancelled run's usage not counted: %+v", u.b)
	}
}

// unpricedRuns counts replies, not models.
func TestUnpricedRunsCountsReplies(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	for _, q := range []string{"a", "b", "c"} {
		runWithUsage(t, a, alice, chat, q, `{"usage":{"input_tokens":5,"output_tokens":1},"runtime":{"model":"some-unpriced-model"}}`)
	}
	if u := alice.usage(chat); u.b.Cost.UnpricedRuns != 3 || u.b.Cost.Amount != nil {
		t.Fatalf("want 3 unpriced replies, got %+v", u.b.Cost)
	}
}

// At the limit a chat with a run in flight is busy (409), not over budget (403).
func TestBusyChatAtLimitIsRunConflict(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	done := runWithUsage(t, a, alice, chat, "seed", usageFrame)
	if err := store.RecordRunUsage(context.Background(), a.db, alice.id, done, store.RunUsage{Input: chatTokenBudget, Output: 1}); err != nil {
		t.Fatal(err)
	}
	// Put a run in flight directly: the budget gate would refuse a normal send.
	if _, err := a.db.Exec(`INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at)
		VALUES (?, ?, ?, 'idem-inflight-1', 'RUNNING', 'x', 'x')`, store.NewID(), alice.id, chat); err != nil {
		t.Fatal(err)
	}
	w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x"})
	if w.Code != 409 || errCode(t, w) != "RUN_CONFLICT" {
		t.Fatalf("busy chat at the limit: %d %s", w.Code, w.Body.String())
	}
}
