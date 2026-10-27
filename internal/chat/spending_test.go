package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

type spendingAnswer struct {
	Currency   string
	Day, Month struct {
		Spent        *float64
		UnpricedRuns int
	}
	DailySoft   *float64
	MonthlySoft *float64
	DailyHard   *float64
	Warnings    []string
	HardReached bool
}

func putSpending(t *testing.T, p *person, body map[string]any) spendingAnswer {
	t.Helper()
	w := p.do("PUT", "/api/me/spending-limits", nil, body)
	if w.Code != 200 {
		t.Fatalf("put spending: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[spendingAnswer](t, w)
}

// spent seeds one finished reply on the priced model costing exactly idr (1,000,000 output tokens = Rp 5,500).
func spent(t *testing.T, a *app, p *person, chat string, outputTokens int64, model string) string {
	t.Helper()
	runID := runWithUsage(t, a, p, chat, "seed", usageFrame)
	if err := store.RecordRunUsage(context.Background(), a.db, p.id, runID, store.RunUsage{Output: outputTokens, Model: model}); err != nil {
		t.Fatal(err)
	}
	return runID
}

func send(p *person, chat, key string) int {
	return p.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": key}, map[string]string{"input": "next"}).Code
}

// #152: the daily hard limit is an admission gate at the boundary, a soft limit only warns, and another user is not
// affected.
func TestDailyHardSpendingLimitIsAnAdmissionGate(t *testing.T) {
	for _, c := range []struct {
		name string
		hard float64
		want int
	}{
		{"just under the limit", 5501, 202},
		{"exactly at the limit", 5500, 403},
		{"over the limit", 5000, 403},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t)
			alice := a.user("alice")
			chat := alice.newChat()
			spent(t, a, alice, chat, 1_000_000, "deepseek-v4-1-flash") // Rp 5,500 today
			sp := putSpending(t, alice, map[string]any{"dailySoft": nil, "monthlySoft": nil, "dailyHard": c.hard})
			if sp.Day.Spent == nil || *sp.Day.Spent != 5500 || sp.Currency != "IDR" {
				t.Fatalf("spend today: %+v", sp)
			}
			if got := send(alice, chat, nextIdem()); got != c.want {
				t.Fatalf("send = %d, want %d", got, c.want)
			}
			bob := a.user("bobby")
			if got := send(bob, bob.newChat(), nextIdem()); got != 202 {
				t.Fatalf("bob blocked by alice's limit: %d", got)
			}
		})
	}
}

func TestSoftSpendingLimitsWarnAndNeverBlock(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	spent(t, a, alice, chat, 1_000_000, "deepseek-v4-1-flash")
	sp := putSpending(t, alice, map[string]any{"dailySoft": 1000, "monthlySoft": 2000, "dailyHard": nil})
	if len(sp.Warnings) != 2 || sp.HardReached {
		t.Fatalf("warnings: %+v", sp)
	}
	if got := send(alice, chat, nextIdem()); got != 202 {
		t.Fatalf("a soft limit blocked a message: %d", got)
	}
	if w := alice.get("/api/chats/" + chat + "/usage"); !strings.Contains(w.Body.String(), `"warnings":["daily","monthly"]`) || !strings.Contains(w.Body.String(), `"hardReached":false`) {
		t.Errorf("usage lacks the spending state: %s", w.Body.String())
	}
}

// Unknown cost is never zero cost: a reply on an unpriced model is reported and does not count, so it neither trips
// the gate nor hides behind a "0".
func TestUnpricedRepliesAreReportedNotCountedAsZero(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	spent(t, a, alice, chat, 1_000_000, "")
	sp := putSpending(t, alice, map[string]any{"dailySoft": nil, "monthlySoft": nil, "dailyHard": 1})
	if sp.Day.Spent != nil || sp.Day.UnpricedRuns != 1 || sp.HardReached {
		t.Fatalf("unpriced spend: %+v", sp)
	}
	if got := send(alice, chat, nextIdem()); got != 202 {
		t.Fatalf("an unpriced reply tripped the gate: %d", got)
	}
}

// Yesterday's spend is not today's; it still counts for the month when the month is the same.
func TestSpendingWindowsAreTheUTCDayAndMonth(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	runID := spent(t, a, alice, chat, 1_000_000, "deepseek-v4-1-flash")
	now := time.Now().UTC()
	var earlier time.Time
	if now.Day() > 1 {
		earlier = now.AddDate(0, 0, -1) // yesterday, same month
	} else {
		earlier = now.AddDate(0, -1, 0) // first of the month: last month
	}
	if _, err := a.db.Exec(`UPDATE runs SET created_at = ? WHERE id = ?`, earlier.Format("2006-01-02T15:04:05.000000000Z"), runID); err != nil {
		t.Fatal(err)
	}
	sp := putSpending(t, alice, map[string]any{"dailySoft": nil, "monthlySoft": nil, "dailyHard": 1})
	if sp.Day.Spent != nil || sp.HardReached {
		t.Fatalf("yesterday counted as today: %+v", sp)
	}
	if now.Day() > 1 && (sp.Month.Spent == nil || *sp.Month.Spent != 5500) {
		t.Fatalf("this month: %+v", sp.Month)
	}
}

// A replay of a message already admitted gets its run back even at the limit; nothing is sent twice.
func TestReplayAtTheHardLimitReturnsTheSameRun(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	key := nextIdem()
	w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": key}, map[string]string{"input": "first"})
	if w.Code != 202 {
		t.Fatalf("first send: %d", w.Code)
	}
	first := decodeBody[struct{ RunID string }](t, w).RunID
	_, fr := a.hx.runWithInput(t, "first")
	s := alice.open(first, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", `{"output":"ok"}`)
	s.waitFor(t, "run.completed")
	if err := store.RecordRunUsage(context.Background(), a.db, alice.id, first, store.RunUsage{Output: 1_000_000, Model: "deepseek-v4-1-flash"}); err != nil {
		t.Fatal(err)
	}
	putSpending(t, alice, map[string]any{"dailySoft": nil, "monthlySoft": nil, "dailyHard": 100})
	w = alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": key}, map[string]string{"input": "first"})
	if w.Code != 202 || decodeBody[struct{ RunID string }](t, w).RunID != first {
		t.Fatalf("replay at the limit: %d %s", w.Code, w.Body.String())
	}
}

func TestSpendingLimitsInputIsStrictAndOwnerScoped(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	for name, body := range map[string]any{
		"missing member": map[string]any{"dailySoft": 1, "monthlySoft": 1},
		"negative":       map[string]any{"dailySoft": -1, "monthlySoft": nil, "dailyHard": nil},
		"zero":           map[string]any{"dailySoft": 0, "monthlySoft": nil, "dailyHard": nil},
		"string":         map[string]any{"dailySoft": "100", "monthlySoft": nil, "dailyHard": nil},
		"too large":      map[string]any{"dailySoft": 2e12, "monthlySoft": nil, "dailyHard": nil},
		"server field":   map[string]any{"dailySoft": 1, "monthlySoft": nil, "dailyHard": nil, "userId": bob.id},
	} {
		if w := alice.do("PUT", "/api/me/spending-limits", nil, body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	first := putSpending(t, alice, map[string]any{"dailySoft": 10, "monthlySoft": 20, "dailyHard": 30})
	again := putSpending(t, alice, map[string]any{"dailySoft": 10, "monthlySoft": 20, "dailyHard": 30})
	if *first.DailyHard != 30 || *again.DailyHard != 30 || *again.MonthlySoft != 20 {
		t.Fatalf("set twice: %+v %+v", first, again)
	}
	if w := bob.get("/api/me/spending-limits"); w.Code != 200 || decodeBody[spendingAnswer](t, w).DailyHard != nil {
		t.Errorf("bob sees alice's limits: %s", w.Body.String())
	}
	cleared := putSpending(t, alice, map[string]any{"dailySoft": nil, "monthlySoft": nil, "dailyHard": nil})
	if cleared.DailySoft != nil || cleared.MonthlySoft != nil || cleared.DailyHard != nil {
		t.Errorf("null did not clear: %+v", cleared)
	}
}

// Review #190: deleting the chat that spent the money does not reset the limit or the warnings.
func TestDeletingAChatDoesNotResetSpending(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	spender := alice.newChat()
	spent(t, a, alice, spender, 1_000_000, "deepseek-v4-1-flash") // Rp 5,500 today
	putSpending(t, alice, map[string]any{"dailySoft": 1000, "monthlySoft": nil, "dailyHard": 5500})
	ctx := context.Background()
	if _, err := store.BeginDeleteChat(ctx, a.db, alice.id, spender, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishDeleteChat(ctx, a.db, alice.id, spender); err != nil {
		t.Fatal(err)
	}
	w := alice.get("/api/me/spending-limits")
	sp := decodeBody[spendingAnswer](t, w)
	if sp.Day.Spent == nil || *sp.Day.Spent != 5500 || !sp.HardReached || len(sp.Warnings) != 1 {
		t.Fatalf("spend after deleting the chat: %+v", sp)
	}
	if got := send(alice, alice.newChat(), nextIdem()); got != 403 {
		t.Fatalf("a deleted chat reset the daily limit: send = %d", got)
	}
}

// #152 review: a send that read the chat while its run was active, and is admitted after that run finished and took the
// day past the hard limit, is still refused: the gate is decided in the admission transaction, never skipped.
func TestHardLimitHoldsWhenTheChatsRunFinishesDuringTheSend(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	putSpending(t, alice, map[string]any{"dailySoft": nil, "monthlySoft": nil, "dailyHard": 5500})
	w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "first"})
	if w.Code != 202 {
		t.Fatalf("first send: %d", w.Code)
	}
	first := decodeBody[struct{ RunID string }](t, w).RunID
	_, fr := a.hx.runWithInput(t, "first")
	s := alice.open(first, "")
	recv(t, fr.opens, "stream")
	a.svc.admitHook = func() {
		a.svc.admitHook = nil
		a.hx.push(fr, "run.completed", `{"output":"ok"}`)
		s.waitFor(t, "run.completed")
		eventually(t, func() bool { return alice.runStatus(first) == store.RunCompleted }, "the first run to finish")
		if err := store.RecordRunUsage(context.Background(), a.db, alice.id, first, store.RunUsage{Output: 1_000_000, Model: "deepseek-v4-1-flash"}); err != nil {
			t.Error(err)
		}
	}
	if got := send(alice, chat, nextIdem()); got != 403 {
		t.Fatalf("send admitted past the hard limit: %d", got)
	}
}
