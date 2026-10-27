package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

type rangeAnswer struct {
	Days            int
	Since, Currency string
	PriceDate       string
	Total           struct {
		Runs, Unreported, Unpriced int
		Input, Output              int64
		Estimate                   *float64
	}
	ByModel []struct {
		Model    string
		Runs     int
		Estimate *float64
	}
	ByChat []struct {
		ChatID, Title    string
		Deleted          bool
		Runs, Unreported int
		Output           int64
	}
	ByDay []struct {
		Day  string
		Runs int
	}
}

var spentN int

// spentOnce is spent with its own input text, so several seeded replies in one test are told apart.
func spentOnce(t *testing.T, a *app, p *person, chat string, outputTokens int64, model string) string {
	t.Helper()
	spentN++
	runID := runWithUsage(t, a, p, chat, fmt.Sprintf("seed-%d", spentN), usageFrame)
	if err := store.RecordRunUsage(context.Background(), a.db, p.id, runID, store.RunUsage{Output: outputTokens, Model: model}); err != nil {
		t.Fatal(err)
	}
	return runID
}

// #151: the range is the caller's own replies only; unreported usage is counted apart and never as zero; a model with
// no known price is never priced; deleted chats still count; the range bounds are UTC days.
func TestUsageRangeIsOwnerScopedAndHonest(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	c1, c2, gone := alice.newChat(), alice.newChat(), alice.newChat()
	spentOnce(t, a, alice, c1, 1_000_000, "deepseek-v4-1-flash") // Rp 5,500
	spentOnce(t, a, alice, c2, 2_000, "unpriced-model")
	quiet := spentOnce(t, a, alice, c2, 0, "")
	if _, err := a.db.Exec(`UPDATE runs SET input_tokens = NULL, output_tokens = NULL, usage_model = NULL WHERE id = ?`, quiet); err != nil {
		t.Fatal(err)
	}
	old := spentOnce(t, a, alice, c1, 1_000_000, "deepseek-v4-1-flash")
	if _, err := a.db.Exec(`UPDATE runs SET created_at = ? WHERE id = ?`, time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339Nano), old); err != nil {
		t.Fatal(err)
	}
	spentOnce(t, a, alice, gone, 500_000, "deepseek-v4-1-flash")
	quietGone := spentOnce(t, a, alice, gone, 0, "")
	if _, err := a.db.Exec(`UPDATE runs SET input_tokens = NULL, output_tokens = NULL, usage_model = NULL WHERE id = ?`, quietGone); err != nil {
		t.Fatal(err)
	}
	deleteAdvertised(t, a)
	if w := alice.do("DELETE", "/api/chats/"+gone, nil, nil); w.Code >= 300 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	eventually(t, func() bool {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM deleted_run_usage WHERE user_id = ?`, alice.id).Scan(&n)
		return n > 0
	}, "the deleted chat's usage to be kept")
	spentOnce(t, a, bob, bob.newChat(), 9_000_000, "deepseek-v4-1-flash")

	w := alice.get("/api/me/usage?days=30")
	if w.Code != 200 {
		t.Fatalf("usage: %d %s", w.Code, w.Body.String())
	}
	v := decodeBody[rangeAnswer](t, w)
	if v.Days != 30 || len(v.ByDay) != 30 || v.Currency != "IDR" || v.PriceDate == "" {
		t.Fatalf("shape: %+v", v)
	}
	if v.Total.Runs != 5 || v.Total.Unreported != 2 || v.Total.Unpriced != 1 { // the deleted chat's unreported reply still counts
		t.Errorf("totals: %+v", v.Total)
	}
	if v.Total.Estimate == nil || *v.Total.Estimate != 5500+2750 {
		t.Errorf("estimate: %v (bob's or the 40-day-old reply leaked in?)", v.Total.Estimate)
	}
	for _, m := range v.ByModel {
		if m.Model == "unpriced-model" && m.Estimate != nil {
			t.Errorf("an unpriced model was priced: %v", *m.Estimate)
		}
	}
	var deleted, withQuiet bool
	for _, c := range v.ByChat {
		if c.Deleted && c.ChatID == "" && c.Output == 500_000 && c.Unreported == 1 && c.Runs == 2 {
			deleted = true
		}
		if c.ChatID == c2 && c.Unreported == 1 {
			withQuiet = true
		}
	}
	if !deleted || !withQuiet {
		t.Errorf("by chat: %+v", v.ByChat)
	}
	if strings.Contains(w.Body.String(), bob.id) {
		t.Error("bob's id in alice's usage")
	}
	if v90 := decodeBody[rangeAnswer](t, alice.get("/api/me/usage?days=90")); v90.Total.Runs != 6 {
		t.Errorf("90 days: %d runs, want 6 (the 40-day-old reply included)", v90.Total.Runs)
	}
	if b := decodeBody[rangeAnswer](t, bob.get("/api/me/usage?days=7")); b.Total.Runs != 1 || len(b.ByChat) != 1 {
		t.Errorf("bob: %+v", b.Total)
	}
	for _, bad := range []string{"", "1", "365", "30;DROP", "-7"} {
		if w := alice.get("/api/me/usage?days=" + bad); w.Code != 400 {
			t.Errorf("days=%q: %d", bad, w.Code)
		}
	}
}
