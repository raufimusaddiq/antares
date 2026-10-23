package hermes

import (
	"context"
	"net/http"
)

// ModelCatalog is the model picker catalog Hermes serves for ONE profile
// (GET /api/model/options, docs/runbooks/models.md). Discovery (discovery.go) reads the
// same route to decide whether models are offered at all; this is the picker's richer view. Only fields Hermes sent are set;
// a field it left out stays nil/empty, which the API reports as unavailable, not
// as a default.
type ModelCatalog struct {
	Provider, Model string // the profile's current default
	Providers       []CatalogProvider
}

type CatalogProvider struct {
	Slug, Name    string
	Current       bool
	Authenticated bool // Hermes' own verdict; a row it did not mark is a configured one
	Warning       string
	Models        []string
	Caps          map[string]ModelCaps
	Pricing       map[string]ModelPrice
	// Quota windows come from the provider's own usage endpoint through Hermes'
	// cache. Nil means Hermes reported none (PROVIDER_USAGE_UNAVAILABLE, PRD CTX-004).
	Quota []QuotaWindow
	// LimitScope/LimitResetsAt: Hermes says the credential pool is rate-limited.
	LimitScope, LimitResetsAt string
}

// ModelCaps are the per-model flags of `capabilities`. Reasoning and Fast are nil when
// Hermes did not say.
type ModelCaps struct {
	Reasoning *bool `json:"reasoning"`
	Fast      *bool `json:"fast"`
}

// ModelPrice is Hermes' formatted per-million-token price text, passed through as
// text: Antares neither parses nor recomputes it.
type ModelPrice struct {
	Input, Output, Cache string
	Free                 bool
}

type QuotaWindow struct {
	Label       string
	UsedPercent float64
	ResetsAt    string
	Scope       string
}

// ModelOptions reads the picker inventory for the binding's profile.
func (c *Client) ModelOptions(ctx context.Context, b Binding, refresh bool) (ModelCatalog, error) {
	// The route is the one capability discovery uses: advertised by /v1/capabilities, behind the
	// model_options flag. Not advertised or not enabled is "not available", never a guessed path.
	caps, capsOK, tenant := c.discoveryDoc(ctx, b)
	if tenant {
		return ModelCatalog{}, fail(CodeTenantNotReady)
	}
	route, ok := caps.route("model_options")
	if !capsOK || !caps.flag("model_options") || !ok {
		return ModelCatalog{}, fail(CodeCapabilityNA)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	path := route + "?include_unconfigured=true"
	if refresh {
		path += "&refresh=true"
	}
	resp, err := c.do(ctx, b, http.MethodGet, path, nil, nil)
	if err != nil {
		return ModelCatalog{}, err
	}
	var d struct {
		Model     string `json:"model"`
		Provider  string `json:"provider"`
		Providers []struct {
			Slug          string               `json:"slug"`
			Name          string               `json:"name"`
			IsCurrent     bool                 `json:"is_current"`
			Authenticated *bool                `json:"authenticated"`
			Warning       string               `json:"warning"`
			Models        []any                `json:"models"`
			Capabilities  map[string]ModelCaps `json:"capabilities"`
			Pricing       map[string]priceWire `json:"pricing"`
			Limit         *struct {
				Scope    string `json:"scope"`
				ResetsAt string `json:"resets_at"`
			} `json:"limit"`
			Usage *struct {
				Windows []struct {
					Label       string   `json:"label"`
					UsedPercent *float64 `json:"used_percent"`
					ResetsAt    string   `json:"resets_at"`
					Scope       string   `json:"scope"`
				} `json:"windows"`
			} `json:"usage"`
		} `json:"providers"`
	}
	if err := readJSON(resp, &d, 4<<20); err != nil {
		return ModelCatalog{}, err
	}
	inv := ModelCatalog{Provider: d.Provider, Model: d.Model}
	for _, p := range d.Providers {
		ip := CatalogProvider{Slug: p.Slug, Name: p.Name, Current: p.IsCurrent, Authenticated: p.Authenticated == nil || *p.Authenticated,
			Warning: p.Warning, Caps: p.Capabilities}
		for _, m := range p.Models {
			if s, ok := m.(string); ok && s != "" {
				ip.Models = append(ip.Models, s)
			}
		}
		if len(p.Pricing) > 0 {
			ip.Pricing = map[string]ModelPrice{}
			for id, w := range p.Pricing {
				ip.Pricing[id] = ModelPrice{Input: w.Input, Output: w.Output, Cache: w.Cache, Free: w.Free}
			}
		}
		if p.Limit != nil {
			ip.LimitScope, ip.LimitResetsAt = p.Limit.Scope, p.Limit.ResetsAt
		}
		if p.Usage != nil {
			for _, w := range p.Usage.Windows {
				if w.UsedPercent != nil { // Hermes drops windows without a figure; so do we
					ip.Quota = append(ip.Quota, QuotaWindow{Label: w.Label, UsedPercent: *w.UsedPercent, ResetsAt: w.ResetsAt, Scope: w.Scope})
				}
			}
		}
		inv.Providers = append(inv.Providers, ip)
	}
	return inv, nil
}

type priceWire struct {
	Input  string `json:"input"`
	Output string `json:"output"`
	Cache  string `json:"cache"`
	Free   bool   `json:"free"`
}

// ReasoningEfforts is the ladder Hermes accepts in model_options.reasoning.effort
// (gateway/platforms/api_server.py `_REASONING_EFFORTS`, less "none", which is
// reasoning.enabled=false). Hermes does not report which of them a given model supports
// (its inventory deliberately omits supported_efforts), so the provider may clamp one.
var ReasoningEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
