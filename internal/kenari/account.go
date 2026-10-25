package kenari

// Account reads the provider account's own usage from Kenari: the prepaid balance, the plan and
// coupon quota, and the 30-day usage summary (docs https://kenari.id/docs/account,
// /docs/account-balance, /docs/account-quota, /docs/usage; GET /v1/account/balance,
// /v1/account/quota, /v1/usage in https://kenari.id/openapi.json).
//
// The account is shared: one key serves every Antares account. Everything here is the provider
// account's figure, never one user's or one chat's, and the only caller is the admin route. The
// key is a backend secret: it goes in the Authorization header of these three GETs and nowhere
// else, and no error text, log line or view carries it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/config"
)

// DefaultAccountBase is the origin the account endpoints are read from.
const DefaultAccountBase = "https://kenari.id"

const (
	accountFreshFor     = time.Minute
	accountRefreshEvery = 15 * time.Second
	accountStaleFor     = time.Hour
)

// ErrNotConfigured means no provider key was given to Antares: nothing is read and no number is shown.
var ErrNotConfigured = errors.New("kenari: account usage not configured")

// Section states. A section is one Kenari endpoint; each can fail alone.
const (
	StateOK          = "OK"
	StateRefused     = "REFUSED"     // 403: the key may not read this (restricted or shared key)
	StateUnavailable = "UNAVAILABLE" // network, status or shape problem; nothing is shown
)

// Balance is GET /v1/account/balance. Amounts are micro-IDR (Rp 1 = 1,000,000).
type Balance struct {
	BalanceMicroIDR   int64  `json:"balanceMicroIdr"`
	ReservedMicroIDR  int64  `json:"reservedMicroIdr"`
	AvailableMicroIDR int64  `json:"availableMicroIdr"`
	Currency          string `json:"currency"`
}

// QuotaWindow is one plan window. Amounts are whole Rupiah.
type QuotaWindow struct {
	Key         string  `json:"key"` // five_hour, week or month
	BudgetRp    int64   `json:"budgetRp"`
	UsedRp      int64   `json:"usedRp"`
	RemainingRp int64   `json:"remainingRp"`
	UsedFrac    float64 `json:"usedFrac"`
	ResetsAt    string  `json:"resetsAt"`
}

type Plan struct {
	Name    string        `json:"name"`
	Windows []QuotaWindow `json:"windows"`
}

type Coupon struct {
	Name        string   `json:"name"`
	UsedRp      int64    `json:"usedRp"`
	RemainingRp *int64   `json:"remainingRp"` // nil: the coupon has no spending limit
	ExpiresAt   string   `json:"expiresAt"`
	ScopeModels []string `json:"scopeModels"`
}

// Quota is GET /v1/account/quota. Plan and Coupon are nil when Kenari says null.
type Quota struct {
	Plan     *Plan   `json:"plan"`
	Coupon   *Coupon `json:"coupon"`
	AlertPct int     `json:"alertPct"`
}

// UsageRow is one model and billing kind of GET /v1/usage. Costs are micro-IDR.
type UsageRow struct {
	Model            string `json:"model,omitempty"`
	BillingKind      string `json:"billingKind,omitempty"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	CachedTokens     int64  `json:"cachedPromptTokens"`
	CacheWriteTokens int64  `json:"cacheWritePromptTokens"`
	CostMicroIDR     int64  `json:"costMicroIdr"`
	CatalogMicroIDR  int64  `json:"catalogMicroIdr"`
}

// Usage is GET /v1/usage over the window Kenari resolved (Unix seconds).
type Usage struct {
	Start int64      `json:"start"`
	End   int64      `json:"end"`
	Rows  []UsageRow `json:"rows"`
	Total UsageRow   `json:"total"`
}

// Section carries one endpoint's outcome. Data is set only when State is OK.
type Section[T any] struct {
	State string `json:"state"`
	// Code is the provider's error code for a REFUSED section (restricted_key_not_allowed,
	// shared_key_not_allowed); it is Kenari's word, never a credential.
	Code string `json:"code,omitempty"`
	Data *T     `json:"data,omitempty"`
}

// AccountSnapshot is one read of the three endpoints. FetchedAt is when it was read; Stale says the
// latest attempt failed or the read is older than its freshness window.
type AccountSnapshot struct {
	FetchedAt time.Time
	Stale     bool
	Balance   Section[Balance]
	Quota     Section[Quota]
	Usage     Section[Usage]
}

// Account is the cached reader. Zero value is not usable: use NewAccount.
type Account struct {
	base string
	key  config.Secret // redacts under every verb, encoder and logger; read only for the header
	hc   *http.Client
	now  func() time.Time

	mu        sync.Mutex
	snap      *AccountSnapshot
	attempted time.Time
	failing   bool
}

// NewAccount returns a reader of base using key. An empty key gives a reader whose Snapshot is
// ErrNotConfigured and which never makes a request. Redirects are refused so the Authorization
// header cannot follow one elsewhere.
func NewAccount(base string, key config.Secret, hc *http.Client) *Account {
	if hc == nil {
		hc = &http.Client{Timeout: fetchTime}
	}
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Account{base: base, key: key, hc: &cp, now: time.Now}
}

// fmt reaches an unexported field by reflection and never calls its Format method, so a
// config.Secret field alone still prints its bytes in a struct dump. These make the Account itself
// render without the key under every verb, encoder and logger.
const redacted = "kenari.Account{[redacted]}"

func (*Account) String() string               { return redacted }
func (*Account) GoString() string             { return redacted }
func (*Account) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, redacted) }
func (*Account) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (*Account) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (*Account) LogValue() slog.Value         { return slog.StringValue(redacted) }

// Configured reports whether a key is held.
func (a *Account) Configured() bool { return a != nil && len(a.key) > 0 }

// Snapshot returns the account figures. A read is made when none is held, the held one is older than
// a minute, or refresh asks; attempts are spaced 15 seconds apart whatever the caller does. When a
// read fails the last good one is returned marked stale for up to an hour, then ErrUnavailable.
// A snapshot where every section failed counts as a failed attempt.
func (a *Account) Snapshot(ctx context.Context, refresh bool) (AccountSnapshot, error) {
	if !a.Configured() {
		return AccountSnapshot{}, ErrNotConfigured
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	var age time.Duration
	if a.snap != nil {
		age = now.Sub(a.snap.FetchedAt)
	}
	due := a.snap == nil || age >= accountFreshFor || refresh
	if due && (a.attempted.IsZero() || now.Sub(a.attempted) >= accountRefreshEvery) {
		a.attempted = now
		s := a.read(ctx)
		s.FetchedAt = a.now()
		if s.Balance.State == StateOK || s.Quota.State == StateOK || s.Usage.State == StateOK || s.refused() {
			a.snap, a.failing = &s, false
			return s, nil
		}
		a.failing = true
	}
	if a.snap == nil || age >= accountStaleFor {
		return AccountSnapshot{}, ErrUnavailable
	}
	s := *a.snap
	s.Stale = a.failing || age >= accountFreshFor
	return s, nil
}

func (s AccountSnapshot) refused() bool {
	return s.Balance.State == StateRefused || s.Quota.State == StateRefused || s.Usage.State == StateRefused
}

func (a *Account) read(ctx context.Context) AccountSnapshot {
	var s AccountSnapshot
	s.Balance = fetchSection(ctx, a, "/v1/account/balance", decodeBalance)
	s.Quota = fetchSection(ctx, a, "/v1/account/quota", decodeQuota)
	s.Usage = fetchSection(ctx, a, "/v1/usage", decodeUsage)
	return s
}

func fetchSection[T any](ctx context.Context, a *Account, path string, decode func([]byte) (*T, error)) Section[T] {
	u, err := url.JoinPath(a.base, path)
	if err != nil {
		return Section[T]{State: StateUnavailable}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Section[T]{State: StateUnavailable}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(a.key))
	resp, err := a.hc.Do(req)
	if err != nil {
		return Section[T]{State: StateUnavailable}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Section[T]{State: StateUnavailable}
	}
	if resp.StatusCode == http.StatusForbidden {
		return Section[T]{State: StateRefused, Code: refusalCode(body)}
	}
	if resp.StatusCode != http.StatusOK {
		return Section[T]{State: StateUnavailable}
	}
	d, err := decode(body)
	if err != nil {
		return Section[T]{State: StateUnavailable}
	}
	return Section[T]{State: StateOK, Data: d}
}

// refusalCode reads Kenari's error code from a 403 body, keeping only the two documented values:
// anything else is reported as no code rather than echoing provider text.
func refusalCode(body []byte) string {
	var e struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	for _, c := range []string{e.Code, e.Error.Code} {
		if c == "restricted_key_not_allowed" || c == "shared_key_not_allowed" {
			return c
		}
	}
	return ""
}

func decodeBalance(b []byte) (*Balance, error) {
	var d struct {
		Object    *string `json:"object"`
		Balance   *int64  `json:"balance_micro_idr"`
		Reserved  *int64  `json:"reserved_micro_idr"`
		Available *int64  `json:"available_micro_idr"`
		Currency  string  `json:"currency"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	// A missing figure is a different shape, not zero; so is any currency but IDR.
	if d.Object == nil || *d.Object != "account.balance" || d.Balance == nil || d.Reserved == nil || d.Available == nil || d.Currency != "IDR" {
		return nil, errors.New("kenari: unexpected balance shape")
	}
	return &Balance{BalanceMicroIDR: *d.Balance, ReservedMicroIDR: *d.Reserved, AvailableMicroIDR: *d.Available, Currency: d.Currency}, nil
}

var windowOrder = []string{"five_hour", "week", "month"}

func decodeQuota(b []byte) (*Quota, error) {
	type window struct {
		Budget    *int64   `json:"budget_rp"`
		Used      *int64   `json:"used_rp"`
		Remaining *int64   `json:"remaining_rp"`
		Frac      *float64 `json:"used_frac"`
		ResetsAt  string   `json:"resets_at"`
	}
	var d struct {
		Plan *struct {
			Name    string            `json:"name"`
			Windows map[string]window `json:"windows"`
		} `json:"plan"`
		Coupon *struct {
			Name        string   `json:"name"`
			Used        *int64   `json:"used_rp"`
			Remaining   *int64   `json:"remaining_rp"`
			ExpiresAt   string   `json:"expires_at"`
			ScopeModels []string `json:"scope_models"`
		} `json:"coupon"`
		AlertPct *int `json:"alert_pct"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.AlertPct == nil {
		return nil, errors.New("kenari: unexpected quota shape")
	}
	q := &Quota{AlertPct: *d.AlertPct}
	if p := d.Plan; p != nil {
		q.Plan = &Plan{Name: p.Name, Windows: []QuotaWindow{}}
		for _, k := range windowOrder { // fixed order; an unknown window key is not shown
			w, ok := p.Windows[k]
			if !ok {
				continue
			}
			if w.Budget == nil || w.Used == nil || w.Remaining == nil || w.Frac == nil {
				return nil, errors.New("kenari: unexpected quota window shape")
			}
			q.Plan.Windows = append(q.Plan.Windows, QuotaWindow{Key: k, BudgetRp: *w.Budget, UsedRp: *w.Used, RemainingRp: *w.Remaining, UsedFrac: *w.Frac, ResetsAt: w.ResetsAt})
		}
	}
	if c := d.Coupon; c != nil {
		if c.Used == nil {
			return nil, errors.New("kenari: unexpected coupon shape")
		}
		q.Coupon = &Coupon{Name: c.Name, UsedRp: *c.Used, RemainingRp: c.Remaining, ExpiresAt: c.ExpiresAt, ScopeModels: c.ScopeModels}
		if q.Coupon.ScopeModels == nil {
			q.Coupon.ScopeModels = []string{}
		}
	}
	return q, nil
}

func decodeUsage(b []byte) (*Usage, error) {
	type row struct {
		Model       string `json:"model"`
		BillingKind string `json:"billing_kind"`
		Requests    *int64 `json:"requests"`
		Prompt      *int64 `json:"prompt_tokens"`
		Completion  *int64 `json:"completion_tokens"`
		Cached      *int64 `json:"cached_prompt_tokens"`
		CacheWrite  *int64 `json:"cache_write_prompt_tokens"`
		Cost        *int64 `json:"cost_micro_idr"`
		Catalog     *int64 `json:"catalog_micro_idr"`
	}
	var d struct {
		Object *string `json:"object"`
		Start  *int64  `json:"start"`
		End    *int64  `json:"end"`
		Data   []row   `json:"data"`
		Total  *row    `json:"total"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Object == nil || *d.Object != "usage.summary" || d.Start == nil || d.End == nil || d.Total == nil {
		return nil, errors.New("kenari: unexpected usage shape")
	}
	conv := func(r row) (UsageRow, error) {
		for _, p := range []*int64{r.Requests, r.Prompt, r.Completion, r.Cached, r.CacheWrite, r.Cost, r.Catalog} {
			if p == nil {
				return UsageRow{}, errors.New("kenari: unexpected usage row shape")
			}
		}
		return UsageRow{Model: r.Model, BillingKind: r.BillingKind, Requests: *r.Requests, PromptTokens: *r.Prompt, CompletionTokens: *r.Completion,
			CachedTokens: *r.Cached, CacheWriteTokens: *r.CacheWrite, CostMicroIDR: *r.Cost, CatalogMicroIDR: *r.Catalog}, nil
	}
	u := &Usage{Start: *d.Start, End: *d.End, Rows: []UsageRow{}}
	for _, r := range d.Data {
		v, err := conv(r)
		if err != nil {
			return nil, err
		}
		u.Rows = append(u.Rows, v)
	}
	t, err := conv(*d.Total)
	if err != nil {
		return nil, err
	}
	u.Total = t
	return u, nil
}
