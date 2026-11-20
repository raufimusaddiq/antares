// Package kenari reads the public model catalogue of the provider Hermes is configured with
// (GET https://kenari.id/v1/models). The owner designated it as the source of model
// capabilities: Hermes' own model inventory carries no input or output modalities
// (docs/runbooks/models.md), so its silence says nothing about vision.
//
// This is metadata only. The request carries no credential, no user data and no profile, and
// nothing here calls a model: Hermes stays the only execution path.
package kenari

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultURL is the catalogue endpoint. Public and unauthenticated.
const DefaultURL = "https://kenari.id/v1/models"

const (
	freshFor = 10 * time.Minute
	// refreshEvery is the soonest a forced read (the picker's refresh) goes back to Kenari. A refresh
	// inside it is answered from the last read, so opening the picker in a loop cannot hammer Kenari.
	refreshEvery = 15 * time.Second
	staleFor     = time.Hour // a last good answer outlives an outage this long, then the model is unknown
	maxBody      = 4 << 20
	fetchTime    = 10 * time.Second
)

// ErrUnavailable means the catalogue could not be read and no recent answer is held.
var ErrUnavailable = errors.New("kenari: catalogue unavailable")

// Modalities are the input and output kinds Kenari publishes for one model.
type Modalities struct {
	Input, Output []string
}

// AcceptsInput reports whether the input list names kind.
func (m Modalities) AcceptsInput(kind string) bool { return contains(m.Input, kind) }

// Produces reports whether the output list names kind.
func (m Modalities) Produces(kind string) bool { return contains(m.Output, kind) }

func contains(l []string, k string) bool {
	for _, v := range l {
		if v == k {
			return true
		}
	}
	return false
}

// Pricing is what Kenari publishes for one model. A number it left out is nil: unknown, not zero.
type Pricing struct {
	Currency, Unit string
	Free           bool
	Input, Output  *float64
	CacheRead      *float64
}

// Model is one catalogue row, limited to what the model picker shows. Pointer and nil-able
// fields are nil when Kenari did not publish them, which callers must show as unknown.
type Model struct {
	ID, Name        string
	Endpoints       []string
	Modalities      Modalities
	HasModalities   bool
	ContextLength   *float64
	Reasoning       *bool
	ReasoningLevels []string // supported_reasoning_levels; nil when not published
	Pricing         *Pricing
	SunsetAt        string // RFC3339, "" when Kenari published no sunset
	SunsetInvalid   bool   // sunset_at was present but unusable: the model counts as sunset
}

// PastSunset is true when the model is sunset at now. An unusable sunset fails closed: it is sunset, never "no sunset".
func (m Model) PastSunset(now time.Time) bool {
	if m.SunsetInvalid {
		return true
	}
	if m.SunsetAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, m.SunsetAt)
	return err != nil || !t.After(now)
}

// Chat reports whether Kenari lists the model for chat completions.
func (m Model) Chat() bool { return contains(m.Endpoints, "chat") }

type Catalogue struct {
	url string
	hc  *http.Client
	now func() time.Time

	mu        sync.Mutex
	models    []Model
	byID      map[string]Model
	fetched   time.Time
	attempted time.Time // last read attempt, successful or not
	failing   bool      // the latest attempt failed; cleared by a successful read
}

// New returns a catalogue reading url. A nil client gets a bounded default; redirects are
// refused so the request cannot be steered elsewhere.
func New(url string, hc *http.Client) *Catalogue {
	if hc == nil {
		hc = &http.Client{Timeout: fetchTime}
	}
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Catalogue{url: url, hc: &cp, now: time.Now}
}

// Lookup returns the published modalities of model id. found is false when the catalogue was
// read and does not list the model (unknown, not "text only"). err is ErrUnavailable when the
// catalogue could not be read and no answer younger than an hour is held.
func (c *Catalogue) Lookup(ctx context.Context, id string) (m Modalities, found bool, err error) {
	row, found, err := c.Get(ctx, id)
	if err != nil || !found || !row.HasModalities {
		return Modalities{}, false, err
	}
	return row.Modalities, true, nil
}

// Get returns the catalogue row for id; found is false when the catalogue was read and does not list it.
func (c *Catalogue) Get(ctx context.Context, id string) (Model, bool, error) {
	if _, err := c.load(ctx); err != nil {
		return Model{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byID[id]
	return m, ok, nil
}

// List returns every row in Kenari's order, or ErrUnavailable.
func (c *Catalogue) List(ctx context.Context) ([]Model, error) {
	m, _, _, err := c.Snapshot(ctx, false)
	return m, err
}

// Snapshot returns the rows, when Kenari was last read successfully, and whether the rows are stale:
// older than the freshness window, or the latest attempt to read failed. The stale state holds until a
// read succeeds. refresh asks for a new read now. Every attempt, failed or not, is spaced refreshEvery
// apart, so an outage cannot make each caller hit Kenari while holding the catalogue lock. A failed
// attempt keeps the last answer for up to staleFor, marked stale.
func (c *Catalogue) Snapshot(ctx context.Context, refresh bool) (models []Model, fetched time.Time, stale bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	age := now.Sub(c.fetched)
	due := c.models == nil || age >= freshFor || refresh
	if due && (c.attempted.IsZero() || now.Sub(c.attempted) >= refreshEvery) {
		c.attempted = now
		rows, ferr := c.fetch(ctx)
		if ferr == nil {
			c.models, c.fetched, c.failing = rows, c.now(), false
			c.byID = make(map[string]Model, len(rows))
			for _, m := range rows {
				c.byID[m.ID] = m
			}
			return rows, c.fetched, false, nil
		}
		c.failing = true
	}
	if c.models == nil || age >= staleFor {
		return nil, time.Time{}, false, ErrUnavailable
	}
	return c.models, c.fetched, c.failing || age >= freshFor, nil
}

// load is the read every other lookup uses: cached inside the freshness window.
func (c *Catalogue) load(ctx context.Context) ([]Model, error) {
	m, _, _, err := c.Snapshot(ctx, false)
	return m, err
}

func (c *Catalogue) fetch(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("kenari: status " + resp.Status)
	}
	var d struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&d); err != nil {
		return nil, err
	}
	if d.Data == nil {
		return nil, errors.New("kenari: no data array")
	}
	out := make([]Model, 0, len(d.Data))
	for _, raw := range d.Data {
		if row, ok := decodeRow(raw); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

type wireRow struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Endpoints  []string `json:"endpoints"`
	Modalities *struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	ContextLength *float64        `json:"context_length"`
	Reasoning     *bool           `json:"reasoning"`
	Levels        []string        `json:"supported_reasoning_levels"`
	SunsetAt      json.RawMessage `json:"sunset_at"`
	Pricing       *struct {
		Currency  string   `json:"currency"`
		Unit      string   `json:"unit"`
		Free      bool     `json:"free"`
		Input     *float64 `json:"input"`
		Output    *float64 `json:"output"`
		CacheRead *float64 `json:"cache_read"`
	} `json:"pricing"`
}

// decodeRow reads one catalogue row. A row with a field of the wrong type is dropped alone, logged by model id and field
// (never the body); sunset_at is the exception, which keeps the row but marks it sunset (see parseSunset).
func decodeRow(raw json.RawMessage) (Model, bool) {
	var m wireRow
	if err := json.Unmarshal(raw, &m); err != nil {
		var id struct {
			ID any `json:"id"`
		}
		_ = json.Unmarshal(raw, &id)
		field := "row"
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			field = te.Field
		}
		slog.Warn("kenari: dropped a malformed catalogue row", "model", id.ID, "field", field)
		return Model{}, false
	}
	if m.ID == "" {
		return Model{}, false
	}
	row := Model{ID: m.ID, Name: m.Name, Endpoints: m.Endpoints, ContextLength: m.ContextLength, Reasoning: m.Reasoning, ReasoningLevels: m.Levels}
	if m.Modalities != nil { // a model with no published modalities stays unknown
		row.Modalities, row.HasModalities = Modalities{Input: m.Modalities.Input, Output: m.Modalities.Output}, true
	}
	var ok bool
	if row.SunsetAt, ok = parseSunset(m.SunsetAt); !ok {
		slog.Warn("kenari: unusable sunset_at, treating the model as sunset", "model", m.ID, "field", "sunset_at")
		row.SunsetInvalid = true
	}
	if p := m.Pricing; p != nil {
		row.Pricing = &Pricing{Currency: p.Currency, Unit: p.Unit, Free: p.Free, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead}
	}
	return row, true
}

// parseSunset reads sunset_at: null or absent is no sunset, an RFC3339 string stays as is, a number is Unix seconds in UTC.
// Anything else is not ok, and the caller must treat the model as sunset.
func parseSunset(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if _, err := time.Parse(time.RFC3339, str); err != nil {
			return "", false
		}
		return str, true
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || n < 0 || n > 253402300799 { // year 9999
		return "", false
	}
	return time.Unix(int64(n), 0).UTC().Format(time.RFC3339), true
}
