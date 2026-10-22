package hermes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Check names. They are the vocabulary admins see (PRD §5.3 step 5).
const (
	CheckEndpoint    = "endpoint"
	CheckIdentity    = "profile_identity"
	CheckIsolation   = "credential_isolation"
	CheckSessions    = "session_ownership"
	CheckToolsets    = "toolset_policy"
	CheckMemory      = "memory_boundary"
	probeTimeout     = 15 * time.Second
	absentSlug       = "u_zzzzzzzzzzzz" // never provisioned: a probe target that must not answer
	maxSiblingProbes = 3
)

// DeniedToolsets is the floor of tenant toolset policy: toolsets that cross the
// profile boundary (Phase 0 findings §6, docs/runbooks/tenant-toolsets.md). No
// configuration may allow them while profiles are not sandboxes, so an admin
// cannot widen the policy past what Phase 0 showed to be unsafe.
var DeniedToolsets = []string{
	"terminal", "file", "code_execution", // read any path the gateway user can
	"delegation", "cronjob", // start agent turns with the full toolset
	"session_search", // reads the shared session store
	"connections",    // third-party credentials
	"browser",        // cookies and logged-in sessions
}

// DefaultAllowedToolsets is the documented default tenant set. It is data, not
// policy: the admin owns it (docs/runbooks/tenant-toolsets.md).
var DefaultAllowedToolsets = []string{"web", "vision", "image_gen", "skills", "memory", "todo", "clarify"}

// ValidateAllowedToolsets refuses a configured allow-list that includes a
// floor toolset. It is checked at startup, not at verification time, so a bad
// policy never starts.
func ValidateAllowedToolsets(names []string) error {
	for _, n := range names {
		for _, d := range DeniedToolsets {
			if n == d {
				return errors.New("toolset " + n + " crosses the profile boundary and cannot be allowed for tenants")
			}
		}
	}
	return nil
}

// Result is one check's outcome. Reason is a fixed phrase, never upstream text.
type Result struct {
	Check  string
	Passed bool
	Reason string
}

// Report is the outcome of a whole verification.
type Report []Result

// OK is true only if every check ran and passed. An empty report is not OK.
func (r Report) OK() bool {
	if len(r) == 0 {
		return false
	}
	for _, c := range r {
		if !c.Passed {
			return false
		}
	}
	return true
}

// Detail is the short, secret-free summary stored for the admin.
func (r Report) Detail() string {
	var parts []string
	for _, c := range r {
		if !c.Passed {
			parts = append(parts, "verification failed: "+c.Check+" ("+c.Reason+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

// Target is the profile to verify. Key never leaves this package's requests.
type Target struct {
	Profile  string
	Key      string
	Siblings []string // other tenants' profile names, for cross-user probes
	Fresh    bool     // first verification: the profile must own no sessions yet
}

// Verifier runs the pre-READY checks against Hermes' multiplexed API.
type Verifier struct {
	base    *url.URL
	client  *http.Client
	allowed map[string]bool
}

// NewVerifier builds a Verifier for the Hermes API origin (e.g. http://hermes:8642)
// and the toolsets a tenant may have enabled.
func NewVerifier(baseURL string, allowed []string, client *http.Client) (*Verifier, error) {
	u, err := parseOrigin(baseURL)
	if err != nil {
		return nil, errors.New("hermes: base URL " + err.Error())
	}
	if err := ValidateAllowedToolsets(allowed); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}
	c := *client
	c.CheckRedirect = noRedirect
	set := map[string]bool{}
	for _, n := range allowed {
		set[n] = true
	}
	return &Verifier{base: &url.URL{Scheme: u.Scheme, Host: u.Host}, client: &c, allowed: set}, nil
}

type resp struct {
	status int
	body   []byte
}

// get performs one GET. key == "" sends no Authorization header. A transport
// failure is returned as an error with no text from the cause.
func (v *Verifier) get(ctx context.Context, path, key string) (resp, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.base.String()+path, nil)
	if err != nil {
		return resp{}, errors.New("request")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	r, err := v.client.Do(req)
	if err != nil {
		return resp{}, errors.New("unreachable")
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		return resp{}, errors.New("unreadable")
	}
	return resp{r.StatusCode, b}, nil
}

func route(profile string) string { return "/p/" + profile }

func refused(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// expectRefusal probes path and requires the authentication refusal, 401 or 403,
// and nothing else: a 200 is a leak, but a 404, a redirect or a 5xx is a broken
// or misrouted endpoint, which proves nothing about the key. It returns "" on a
// refusal, otherwise a fixed-vocabulary reason that tells an unreachable
// endpoint ("did not answer") from a wrong status (the status is named).
func (v *Verifier) expectRefusal(ctx context.Context, path, key, what string) string {
	r, err := v.get(ctx, path, key)
	switch {
	case err != nil:
		return what + " did not answer"
	case refused(r.status):
		return ""
	case r.status == http.StatusOK:
		return what + " accepted the request (status 200)"
	default:
		return what + " answered status " + strconv.Itoa(r.status) + ", expected 401 or 403"
	}
}

// Verify runs every check. It never stops at the first failure, so the admin
// sees everything that is wrong at once; it never returns OK on partial data.
func (v *Verifier) Verify(ctx context.Context, t Target) Report {
	if !ValidSlug(t.Profile) || t.Key == "" {
		return Report{{CheckEndpoint, false, "no profile to verify"}}
	}
	sib := t.Siblings
	if len(sib) > maxSiblingProbes {
		sib = sib[:maxSiblingProbes]
	}
	for _, s := range sib {
		if !ValidSlug(s) || s == t.Profile {
			return Report{{CheckIsolation, false, "invalid sibling target"}}
		}
	}
	caps, capsOK := v.capabilities(ctx, t)
	return Report{
		v.endpoint(ctx, t, capsOK),
		v.identity(ctx, t),
		v.isolation(ctx, t, sib),
		v.sessions(ctx, t, sib),
		v.toolsets(ctx, t),
		v.memory(caps, capsOK),
	}
}

type capsDoc struct {
	Object string `json:"object"`
	Auth   struct {
		Required *bool `json:"required"`
	} `json:"auth"`
	Features struct {
		MemoryWriteAPI *bool `json:"memory_write_api"`
		AdminConfigRW  *bool `json:"admin_config_rw"`
	} `json:"features"`
}

func (v *Verifier) capabilities(ctx context.Context, t Target) (capsDoc, bool) {
	r, err := v.get(ctx, route(t.Profile)+"/v1/capabilities", t.Key)
	var d capsDoc
	if err != nil || r.status != http.StatusOK || json.Unmarshal(r.body, &d) != nil {
		return capsDoc{}, false
	}
	return d, d.Object == "hermes.api_server.capabilities"
}

// endpoint: the tenant's own route answers with its own key, in the documented
// shape.
func (v *Verifier) endpoint(ctx context.Context, t Target, capsOK bool) Result {
	if !capsOK {
		return Result{CheckEndpoint, false, "capabilities endpoint did not answer as expected"}
	}
	r, err := v.get(ctx, route(t.Profile)+"/v1/models", t.Key)
	if err != nil || r.status != http.StatusOK {
		return Result{CheckEndpoint, false, "models endpoint did not answer"}
	}
	var m struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(r.body, &m) != nil || m.Object != "list" || m.Data == nil {
		return Result{CheckEndpoint, false, "models endpoint answered in an unexpected shape"}
	}
	return Result{CheckEndpoint, true, ""}
}

// identity: the profile route is its own namespace. Hermes' verified API has no
// profile-name field, so identity is shown the only way it can be: the route
// accepts this profile's key and nothing that is not it. The default profile's
// route and a never-provisioned route must not accept the tenant's key.
func (v *Verifier) identity(ctx context.Context, t Target) Result {
	if why := v.expectRefusal(ctx, "/v1/models", t.Key, "the default profile route (tenant key)"); why != "" {
		return Result{CheckIdentity, false, why}
	}
	if why := v.expectRefusal(ctx, route(absentSlug)+"/v1/models", t.Key, "an unprovisioned profile route (tenant key)"); why != "" {
		return Result{CheckIdentity, false, why}
	}
	return Result{CheckIdentity, true, ""}
}

// isolation: a missing key, a wrong key and every sibling's route refuse this
// tenant's key.
func (v *Verifier) isolation(ctx context.Context, t Target, siblings []string) Result {
	own := route(t.Profile) + "/v1/models"
	if why := v.expectRefusal(ctx, own, "", "the profile route (no key)"); why != "" {
		return Result{CheckIsolation, false, why}
	}
	sum := sha256.Sum256([]byte("antares-verify-wrong-key:" + t.Key))
	if why := v.expectRefusal(ctx, own, hex.EncodeToString(sum[:]), "the profile route (wrong key)"); why != "" {
		return Result{CheckIsolation, false, why}
	}
	for _, sib := range siblings {
		if why := v.expectRefusal(ctx, route(sib)+"/v1/models", t.Key, "another tenant's route (tenant key)"); why != "" {
			return Result{CheckIsolation, false, why}
		}
	}
	return Result{CheckIsolation, true, ""}
}

// sessions: the profile lists its own sessions, no other tenant's route lists
// them for this key, and a new profile owns none. The sessions list shape is
// documented but UNVERIFIED on the deployed build (docs/hermes/PHASE0-FINDINGS.md);
// anything this does not recognise fails closed.
func (v *Verifier) sessions(ctx context.Context, t Target, siblings []string) Result {
	r, err := v.get(ctx, route(t.Profile)+"/api/sessions?limit=1&offset=0", t.Key)
	if err != nil || r.status != http.StatusOK {
		return Result{CheckSessions, false, "sessions endpoint did not answer"}
	}
	if t.Fresh {
		n, ok := sessionCount(r.body)
		if !ok {
			return Result{CheckSessions, false, "sessions endpoint answered in an unrecognised shape"}
		}
		if n != 0 {
			return Result{CheckSessions, false, "new profile already holds sessions"}
		}
	}
	for _, sib := range siblings {
		if why := v.expectRefusal(ctx, route(sib)+"/api/sessions?limit=1&offset=0", t.Key, "another tenant's sessions (tenant key)"); why != "" {
			return Result{CheckSessions, false, why}
		}
	}
	return Result{CheckSessions, true, ""}
}

// sessionCount accepts a bare array or an object whose "sessions" member is an
// array. Anything else is unrecognised.
func sessionCount(b []byte) (int, bool) {
	var arr []json.RawMessage
	if json.Unmarshal(b, &arr) == nil && arr != nil {
		return len(arr), true
	}
	var obj struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if json.Unmarshal(b, &obj) == nil && obj.Sessions != nil {
		return len(obj.Sessions), true
	}
	return 0, false
}

// toolsets: nothing that crosses the profile boundary is enabled, and nothing
// outside the configured allow-list is either. An empty answer proves nothing
// and fails.
func (v *Verifier) toolsets(ctx context.Context, t Target) Result {
	r, err := v.get(ctx, route(t.Profile)+"/v1/toolsets", t.Key)
	if err != nil || r.status != http.StatusOK {
		return Result{CheckToolsets, false, "toolsets endpoint did not answer"}
	}
	var d struct {
		Object string `json:"object"`
		Data   []struct {
			Name    string `json:"name"`
			Enabled *bool  `json:"enabled"`
		} `json:"data"`
	}
	if json.Unmarshal(r.body, &d) != nil || d.Object != "list" || len(d.Data) == 0 {
		return Result{CheckToolsets, false, "toolsets endpoint answered in an unexpected shape"}
	}
	seen := map[string]bool{}
	for _, ts := range d.Data {
		if ts.Name == "" || ts.Enabled == nil {
			return Result{CheckToolsets, false, "toolsets endpoint answered in an unexpected shape"}
		}
		seen[ts.Name] = true
		if !*ts.Enabled {
			continue
		}
		for _, den := range DeniedToolsets {
			if ts.Name == den {
				return Result{CheckToolsets, false, "boundary-crossing toolset " + den + " is enabled"}
			}
		}
		if !v.allowed[ts.Name] {
			return Result{CheckToolsets, false, "toolset " + safeName(ts.Name) + " is enabled but not allowed"}
		}
	}
	// A denied toolset the profile does not report at all cannot be shown to be
	// off; the deployed API lists every toolset, so absence is a changed contract.
	for _, den := range DeniedToolsets {
		if !seen[den] {
			return Result{CheckToolsets, false, "toolset " + den + " is not reported, so its state is unknown"}
		}
	}
	return Result{CheckToolsets, true, ""}
}

// safeName keeps an upstream toolset name printable and short before it enters
// an admin-visible reason.
func safeName(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

// memory: there is no memory read/list API in Hermes (Phase 0 §2), so the
// boundary is what the API does not offer — no memory write surface, no
// config write that could undo the toolset policy — plus the toolset check
// above. Profiles are not sandboxes (PRD §5.5); this is a policy gate, not a
// proof of filesystem isolation, and is documented as such.
func (v *Verifier) memory(c capsDoc, ok bool) Result {
	if !ok {
		return Result{CheckMemory, false, "capabilities unavailable"}
	}
	if c.Features.MemoryWriteAPI == nil || *c.Features.MemoryWriteAPI {
		return Result{CheckMemory, false, "a memory write API is exposed or its state is unknown"}
	}
	if c.Features.AdminConfigRW == nil || *c.Features.AdminConfigRW {
		return Result{CheckMemory, false, "profile configuration is writable through the API or its state is unknown"}
	}
	if c.Auth.Required == nil || !*c.Auth.Required {
		return Result{CheckMemory, false, "the API does not require authentication"}
	}
	return Result{CheckMemory, true, ""}
}
