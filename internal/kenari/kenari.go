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
	"net/http"
	"sync"
	"time"
)

// DefaultURL is the catalogue endpoint. Public and unauthenticated.
const DefaultURL = "https://kenari.id/v1/models"

const (
	freshFor  = 10 * time.Minute
	staleFor  = time.Hour // a last good answer outlives an outage this long, then the model is unknown
	maxBody   = 4 << 20
	fetchTime = 10 * time.Second
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
	SunsetAt        string
}

// Chat reports whether Kenari lists the model for chat completions.
func (m Model) Chat() bool { return contains(m.Endpoints, "chat") }

type Catalogue struct {
	url string
	hc  *http.Client
	now func() time.Time

	mu      sync.Mutex
	models  []Model
	byID    map[string]Model
	fetched time.Time
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
	return c.load(ctx)
}

func (c *Catalogue) load(ctx context.Context) ([]Model, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.models != nil && c.now().Sub(c.fetched) < freshFor {
		return c.models, nil
	}
	models, err := c.fetch(ctx)
	if err != nil {
		if c.models != nil && c.now().Sub(c.fetched) < staleFor {
			return c.models, nil
		}
		return nil, ErrUnavailable
	}
	c.models, c.fetched = models, c.now()
	c.byID = make(map[string]Model, len(models))
	for _, m := range models {
		c.byID[m.ID] = m
	}
	return models, nil
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
		Data []struct {
			ID         string   `json:"id"`
			Name       string   `json:"name"`
			Endpoints  []string `json:"endpoints"`
			Modalities *struct {
				Input  []string `json:"input"`
				Output []string `json:"output"`
			} `json:"modalities"`
			ContextLength *float64 `json:"context_length"`
			Reasoning     *bool    `json:"reasoning"`
			Levels        []string `json:"supported_reasoning_levels"`
			SunsetAt      *string  `json:"sunset_at"`
			Pricing       *struct {
				Currency  string   `json:"currency"`
				Unit      string   `json:"unit"`
				Free      bool     `json:"free"`
				Input     *float64 `json:"input"`
				Output    *float64 `json:"output"`
				CacheRead *float64 `json:"cache_read"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&d); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(d.Data))
	for _, m := range d.Data {
		if m.ID == "" {
			continue
		}
		row := Model{ID: m.ID, Name: m.Name, Endpoints: m.Endpoints, ContextLength: m.ContextLength, Reasoning: m.Reasoning, ReasoningLevels: m.Levels}
		if m.Modalities != nil { // a model with no published modalities stays unknown
			row.Modalities, row.HasModalities = Modalities{Input: m.Modalities.Input, Output: m.Modalities.Output}, true
		}
		if m.SunsetAt != nil {
			row.SunsetAt = *m.SunsetAt
		}
		if p := m.Pricing; p != nil {
			row.Pricing = &Pricing{Currency: p.Currency, Unit: p.Unit, Free: p.Free, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead}
		}
		out = append(out, row)
	}
	return out, nil
}
