package hermes

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// Capability discovery (PRD §9A.4, §9A.8, issue "Phase 2b slice 1"). Everything
// the UI may offer is read from the deployed Hermes under the caller's own
// binding: GET /v1/capabilities for the feature flags and the route table, then
// the routes it advertises for toolsets, skills and model options.
//
// The rule throughout is that only an explicit, well-formed "yes" is
// "available". A missing flag, a missing route, a malformed document or a failed
// call is "unsupported" with a reason; nothing is defaulted to available.

const (
	StatusAvailable   = "available"
	StatusUnsupported = "unsupported"
)

// Capability identifiers the UI knows how to present.
const (
	CapModels       = "models"
	CapModelLock    = "model_lock"
	CapSettings     = "settings"
	CapToolsets     = "toolsets"
	CapSkills       = "skills"
	CapToolProgress = "tool_progress"
)

const (
	maxModelOptions    = 4 << 20 // a provider catalog can be large; a bigger answer is unusable, not truncated
	maxModelsPerRow    = 200
	maxListed          = 500 // toolsets, skills, providers
	maxToolsPerToolset = 100
)

// Capability is one verdict. Reason is set when Status is unsupported.
type Capability struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type Toolset struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
	Tools  []string `json:"tools"`
}

// Provider is one authenticated model provider. TotalModels exceeds len(Models)
// when the list was cut, so a cut is visible.
type Provider struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Models      []string `json:"models"`
	TotalModels int      `json:"totalModels"`
}

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// Inventory is the whole discovery result: closed fields, no upstream free text
// beyond names, descriptions and categories of skills.
type Inventory struct {
	Capabilities []Capability `json:"capabilities"`
	Toolsets     []Toolset    `json:"toolsets"`
	Providers    []Provider   `json:"providers"`
	Skills       []Skill      `json:"skills"`
}

type discoveryCaps struct {
	Object    string                     `json:"object"`
	Features  map[string]json.RawMessage `json:"features"`
	Endpoints map[string]struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"endpoints"`
}

func (d discoveryCaps) flag(name string) bool { return string(d.Features[name]) == "true" }

var routeRE = regexp.MustCompile(`^/[A-Za-z0-9_./-]{1,100}$`)

// route returns the GET path Hermes advertises for name. A route that is not
// advertised, is not a GET, or is templated or odd-looking is not used.
func (d discoveryCaps) route(name string) (string, bool) {
	e, ok := d.Endpoints[name]
	if !ok || e.Method != http.MethodGet || !routeRE.MatchString(e.Path) || strings.Contains(e.Path, "..") {
		return "", false
	}
	return e.Path, true
}

// getJSON reads one advertised route under the binding. ok is false for any
// failure; tenant is true when Hermes refused the binding itself.
func (c *Client) getJSON(ctx context.Context, b Binding, path string, v any, limit int64) (ok, tenant bool) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodGet, path, nil, nil)
	if err != nil {
		return false, CodeOf(err) == CodeTenantNotReady
	}
	return readJSON(resp, v, limit) == nil, false
}

func unsupported(id, label, reason string) Capability {
	return Capability{ID: id, Label: label, Status: StatusUnsupported, Reason: reason}
}

func available(id, label string) Capability {
	return Capability{ID: id, Label: label, Status: StatusAvailable}
}

// Discover builds the inventory for the binding. It returns an error only when
// Hermes refuses the binding (CodeTenantNotReady): then nothing may be shown. Any
// other failure leaves the affected capabilities unsupported, with a reason.
func (c *Client) Discover(ctx context.Context, b Binding) (Inventory, error) {
	inv := Inventory{Toolsets: []Toolset{}, Providers: []Provider{}, Skills: []Skill{}}

	var caps discoveryCaps
	capsOK, tenant := c.getJSON(ctx, b, "/v1/capabilities", &caps, maxAPIResponse)
	if tenant {
		return Inventory{}, fail(CodeTenantNotReady)
	}
	if capsOK && caps.Object != "hermes.api_server.capabilities" {
		capsOK = false
	}
	if !capsOK {
		caps = discoveryCaps{}
	}
	why := func(feature string) string {
		if !capsOK {
			return "Hermes did not return a usable capability document"
		}
		return "Hermes does not report " + feature + " as enabled"
	}

	// models
	models := unsupported(CapModels, "Model choices", why("model_options"))
	if capsOK && caps.flag("model_options") {
		models.Reason = "Hermes does not advertise a model options route"
		if p, ok := caps.route("model_options"); ok {
			inv.Providers, models.Reason = c.modelOptions(ctx, b, p)
			if len(inv.Providers) > 0 {
				models = available(CapModels, "Model choices")
			}
		}
	}
	lock := unsupported(CapModelLock, "Per-chat model", why("session_model_lock"))
	if caps.flag("session_model_lock") {
		lock = available(CapModelLock, "Per-chat model")
	}

	// settings: the config write API. Read-only discovery never needs it.
	settings := unsupported(CapSettings, "Change Hermes settings", why("admin_config_rw"))
	if caps.flag("admin_config_rw") {
		settings = available(CapSettings, "Change Hermes settings")
	}

	// toolsets
	tools := unsupported(CapToolsets, "Tools", "Hermes does not advertise a toolsets route")
	if p, ok := caps.route("toolsets"); ok && capsOK {
		var d struct {
			Object string `json:"object"`
			Data   []struct {
				Name       string   `json:"name"`
				Enabled    *bool    `json:"enabled"`
				Configured *bool    `json:"configured"`
				Tools      []string `json:"tools"`
			} `json:"data"`
		}
		tools.Reason = "Hermes did not return a usable toolset list"
		if ok, _ := c.getJSON(ctx, b, p, &d, maxAPIResponse); ok && d.Object == "list" {
			tools.Reason = "No toolset is enabled and configured"
			for _, t := range d.Data {
				if len(inv.Toolsets) >= maxListed {
					break
				}
				if !printable(t.Name) {
					continue
				}
				ts := Toolset{Name: t.Name, Status: StatusUnsupported, Tools: boundedNames(t.Tools, maxToolsPerToolset)}
				switch {
				case t.Enabled == nil || t.Configured == nil:
					ts.Reason = "Hermes did not say whether this toolset is enabled and configured"
				case !*t.Enabled:
					ts.Reason = "Not enabled on this Hermes"
				case !*t.Configured:
					ts.Reason = "Enabled but not configured (missing credentials)"
				default:
					ts.Status = StatusAvailable
					tools = available(CapToolsets, "Tools")
				}
				inv.Toolsets = append(inv.Toolsets, ts)
			}
		}
	}

	// skills
	skills := unsupported(CapSkills, "Skills", why("skills_api"))
	if capsOK && caps.flag("skills_api") {
		skills.Reason = "Hermes does not advertise a skills route"
		if p, ok := caps.route("skills"); ok {
			var d struct {
				Object string `json:"object"`
				Data   []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Category    string `json:"category"`
				} `json:"data"`
			}
			skills.Reason = "Hermes did not return a usable skills list"
			if ok, _ := c.getJSON(ctx, b, p, &d, maxAPIResponse); ok && d.Object == "list" {
				skills.Reason = "Hermes reports no installed skills"
				for _, s := range d.Data {
					if len(inv.Skills) >= maxListed {
						break
					}
					if printable(s.Name) {
						inv.Skills = append(inv.Skills, Skill{Name: s.Name, Description: clip(s.Description, 300), Category: clip(s.Category, 60)})
					}
				}
				if len(inv.Skills) > 0 {
					skills = available(CapSkills, "Skills")
				}
			}
		}
	}

	// tool progress arrives on the run event stream, so both must be on.
	progress := unsupported(CapToolProgress, "Live tool progress", why("tool_progress_events"))
	if capsOK && caps.flag("tool_progress_events") {
		progress.Reason = why("run_events_sse")
		if caps.flag("run_events_sse") {
			progress = available(CapToolProgress, "Live tool progress")
		}
	}

	inv.Capabilities = []Capability{models, lock, settings, tools, skills, progress}
	return inv, nil
}

// modelOptions reads the picker inventory. Only providers Hermes marks
// authenticated=true and that list at least one model are returned; a provider
// without an explicit true is not offered. The second result is the reason when
// nothing is returned.
func (c *Client) modelOptions(ctx context.Context, b Binding, path string) ([]Provider, string) {
	var d struct {
		Providers []struct {
			Slug          string   `json:"slug"`
			Name          string   `json:"name"`
			Models        []string `json:"models"`
			Authenticated *bool    `json:"authenticated"`
		} `json:"providers"`
	}
	if ok, _ := c.getJSON(ctx, b, path+"?include_unconfigured=false", &d, maxModelOptions); !ok || d.Providers == nil {
		return nil, "Hermes did not return a usable model list"
	}
	var out []Provider
	for _, p := range d.Providers {
		if len(out) >= maxListed {
			break
		}
		if p.Authenticated == nil || !*p.Authenticated || !printable(p.Slug) {
			continue
		}
		m := boundedNames(p.Models, maxModelsPerRow)
		if len(m) == 0 {
			continue
		}
		name := clip(p.Name, 80)
		if name == "" {
			name = p.Slug
		}
		out = append(out, Provider{Slug: p.Slug, Name: name, Models: m, TotalModels: len(p.Models)})
	}
	if len(out) == 0 {
		return nil, "Hermes lists no authenticated provider with models"
	}
	return out, ""
}

// printable reports a short, plain upstream name safe to show.
func printable(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func boundedNames(in []string, max int) []string {
	out := []string{}
	for _, s := range in {
		if len(out) >= max {
			break
		}
		if printable(s) {
			out = append(out, s)
		}
	}
	return out
}
