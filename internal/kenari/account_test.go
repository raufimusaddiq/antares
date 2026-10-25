package kenari

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// testdata/account/*.json carry the response shapes Kenari documents (https://kenari.id/docs/account-balance,
// /docs/account-quota, /docs/usage) with the documentation's fabricated values. They are not a live read:
// no provider key was available to this checkout, and no test here calls the real provider.
const fakeKey = "kn-TESTKEY-not-a-real-credential"

type fakeKenari struct {
	mu     sync.Mutex
	hits   map[string]int
	status map[string]int    // path -> forced status
	body   map[string]string // path -> forced body
	auth   []string
}

func newFake(t *testing.T) (*fakeKenari, *Account) {
	t.Helper()
	f := &fakeKenari{hits: map[string]int{}, status: map[string]int{}, body: map[string]string{}}
	files := map[string]string{"/v1/account/balance": "balance", "/v1/account/quota": "quota", "/v1/usage": "usage"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits[r.URL.Path]++
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		if r.Method != http.MethodGet {
			t.Errorf("%s %s: only GET is allowed", r.Method, r.URL.Path)
		}
		if st := f.status[r.URL.Path]; st != 0 {
			w.WriteHeader(st)
			w.Write([]byte(f.body[r.URL.Path]))
			return
		}
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile("testdata/account/" + name + ".json")
		if err != nil {
			t.Error(err)
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	a := NewAccount(srv.URL, []byte(fakeKey), nil)
	return f, a
}

func (f *fakeKenari) total() (n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.hits {
		n += v
	}
	return
}

func TestAccountParsesDocumentedShapes(t *testing.T) {
	f, a := newFake(t)
	s, err := a.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Stale || s.Balance.State != StateOK || s.Quota.State != StateOK || s.Usage.State != StateOK {
		t.Fatalf("snapshot = %+v", s)
	}
	b := s.Balance.Data
	if b.BalanceMicroIDR != 25_000_000_000 || b.ReservedMicroIDR != 1_500_000 || b.AvailableMicroIDR != 24_998_500_000 || b.Currency != "IDR" {
		t.Errorf("balance = %+v", b)
	}
	q := s.Quota.Data
	if q.AlertPct != 80 || q.Coupon != nil || q.Plan == nil || q.Plan.Name != "Indie" || len(q.Plan.Windows) != 2 {
		t.Fatalf("quota = %+v", q)
	}
	if w := q.Plan.Windows[0]; w.Key != "week" || w.BudgetRp != 75000 || w.UsedRp != 12000 || w.RemainingRp != 63000 || w.UsedFrac != 0.16 || w.ResetsAt != "2026-10-13T07:12:00Z" {
		t.Errorf("week window = %+v", w)
	}
	u := s.Usage.Data
	if u.Start != 1788300000 || u.End != 1790892000 || len(u.Rows) != 1 || u.Rows[0].Model != "deepseek-v4-flash" || u.Total.PromptTokens != 480000 || u.Total.CostMicroIDR != 15_400_000 {
		t.Errorf("usage = %+v", u)
	}
	for _, h := range f.auth {
		if h != "Bearer "+fakeKey {
			t.Errorf("Authorization header = %q", h)
		}
	}
}

func TestAccountRefusedKeyIsReportedNotInvented(t *testing.T) {
	f, a := newFake(t)
	for _, p := range []string{"/v1/account/balance", "/v1/account/quota"} {
		f.status[p] = http.StatusForbidden
		f.body[p] = `{"error":{"code":"restricted_key_not_allowed","message":"echo ` + fakeKey + `"}}`
	}
	s, err := a.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Balance.State != StateRefused || s.Balance.Code != "restricted_key_not_allowed" || s.Balance.Data != nil || s.Quota.State != StateRefused {
		t.Errorf("balance/quota = %+v / %+v", s.Balance, s.Quota)
	}
	if s.Usage.State != StateOK {
		t.Errorf("usage should still read for a restricted key: %+v", s.Usage)
	}
}

func TestAccountRejectsWrongShapes(t *testing.T) {
	cases := map[string]string{
		"/v1/account/balance": `{"object":"account.balance","balance_micro_idr":5,"currency":"IDR"}`,           // a missing figure is not zero
		"/v1/account/quota":   `{"plan":null,"coupon":null}`,                                                   // no alert_pct
		"/v1/usage":           `{"object":"usage.summary","start":1,"end":2,"data":[],"total":{"requests":1}}`, // partial total
	}
	for path, body := range cases {
		f, a := newFake(t)
		f.status[path], f.body[path] = http.StatusOK, body
		s, _ := a.Snapshot(context.Background(), false)
		got := map[string]string{"/v1/account/balance": s.Balance.State, "/v1/account/quota": s.Quota.State, "/v1/usage": s.Usage.State}[path]
		if got != StateUnavailable {
			t.Errorf("%s with a wrong shape = %s, want %s", path, got, StateUnavailable)
		}
	}
	f, a := newFake(t)
	f.status["/v1/account/balance"], f.body["/v1/account/balance"] = http.StatusOK, `{"object":"account.balance","balance_micro_idr":1,"reserved_micro_idr":0,"available_micro_idr":1,"currency":"USD"}`
	if s, _ := a.Snapshot(context.Background(), false); s.Balance.State != StateUnavailable {
		t.Errorf("a non-IDR balance must not be shown as Rupiah: %+v", s.Balance)
	}
}

func TestAccountCachesAndThrottlesRefresh(t *testing.T) {
	f, a := newFake(t)
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return clock }
	ctx := context.Background()
	a.Snapshot(ctx, false)
	a.Snapshot(ctx, false)
	if f.total() != 3 {
		t.Fatalf("two reads inside the window made %d requests, want 3 (one read)", f.total())
	}
	a.Snapshot(ctx, true) // refresh inside 15s is answered from the last read
	if f.total() != 3 {
		t.Fatalf("a refresh inside 15s hit Kenari: %d requests", f.total())
	}
	clock = clock.Add(16 * time.Second)
	a.Snapshot(ctx, true)
	if f.total() != 6 {
		t.Fatalf("a refresh after 15s made %d requests, want 6", f.total())
	}
}

func TestAccountStaleThenGoneAfterOutage(t *testing.T) {
	f, a := newFake(t)
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return clock }
	ctx := context.Background()
	if _, err := a.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/v1/account/balance", "/v1/account/quota", "/v1/usage"} {
		f.status[p] = http.StatusBadGateway
	}
	clock = clock.Add(2 * time.Minute)
	s, err := a.Snapshot(ctx, false)
	if err != nil || !s.Stale || s.Balance.State != StateOK {
		t.Fatalf("outage inside the hour: err=%v stale=%v balance=%+v (want the last good read, marked stale)", err, s.Stale, s.Balance)
	}
	if !s.FetchedAt.Equal(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("FetchedAt moved to %v on a failed read", s.FetchedAt)
	}
	clock = clock.Add(30 * time.Second) // still failing: stays stale, does not turn fresh
	if s, _ := a.Snapshot(ctx, false); !s.Stale {
		t.Error("stale cleared without a successful read")
	}
	clock = clock.Add(2 * time.Hour)
	if _, err := a.Snapshot(ctx, false); !errors.Is(err, ErrUnavailable) {
		t.Errorf("after an hour of outage err = %v, want ErrUnavailable", err)
	}
}

func TestAccountNotConfiguredNeverCallsOut(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	a := NewAccount(srv.URL, nil, nil)
	if _, err := a.Snapshot(context.Background(), true); !errors.Is(err, ErrNotConfigured) || hit {
		t.Errorf("err=%v hit=%v", err, hit)
	}
	var nilAcc *Account
	if _, err := nilAcc.Snapshot(context.Background(), false); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("nil account err = %v", err)
	}
}

func TestAccountRefusesRedirectsSoTheKeyStaysPut(t *testing.T) {
	var other int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other++
		if r.Header.Get("Authorization") != "" {
			t.Error("the key followed a redirect")
		}
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	a := NewAccount(srv.URL, []byte(fakeKey), nil)
	_, err := a.Snapshot(context.Background(), false)
	if other != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("redirect target hit %d times, err %v", other, err)
	}
}

func TestAccountKeyNeverAppearsInViewsOrFormatting(t *testing.T) {
	f, a := newFake(t)
	f.status["/v1/usage"], f.body["/v1/usage"] = http.StatusForbidden, `{"code":"shared_key_not_allowed","detail":"`+fakeKey+`"}`
	f.status["/v1/account/quota"], f.body["/v1/account/quota"] = http.StatusInternalServerError, "oops "+fakeKey
	s, _ := a.Snapshot(context.Background(), false)
	var out bytes.Buffer
	fmt.Fprintf(&out, "%v %+v %#v", s, s, s)
	if strings.Contains(out.String(), fakeKey) {
		t.Fatal("key leaked into a snapshot")
	}
	if s.Usage.Code != "shared_key_not_allowed" {
		t.Errorf("usage code = %q", s.Usage.Code)
	}
}
