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

type Catalogue struct {
	url string
	hc  *http.Client
	now func() time.Time

	mu      sync.Mutex
	models  map[string]Modalities
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
	models, err := c.load(ctx)
	if err != nil {
		return Modalities{}, false, err
	}
	m, found = models[id]
	return m, found, nil
}

func (c *Catalogue) load(ctx context.Context) (map[string]Modalities, error) {
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
	return models, nil
}

func (c *Catalogue) fetch(ctx context.Context) (map[string]Modalities, error) {
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
			ID         string `json:"id"`
			Modalities *struct {
				Input  []string `json:"input"`
				Output []string `json:"output"`
			} `json:"modalities"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&d); err != nil {
		return nil, err
	}
	out := make(map[string]Modalities, len(d.Data))
	for _, m := range d.Data {
		if m.ID == "" || m.Modalities == nil { // a model with no published modalities stays unknown
			continue
		}
		out[m.ID] = Modalities{Input: m.Modalities.Input, Output: m.Modalities.Output}
	}
	return out, nil
}
