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
	"slices"
	"strconv"
	"strings"
	"time"
)

// Check names. They are the vocabulary admins see (PRD §5.3 step 5).
const (
	CheckEndpoint  = "endpoint"
	CheckIdentity  = "profile_identity"
	CheckIsolation = "credential_isolation"
	CheckSessions  = "session_ownership"
	CheckToolsets  = "toolset_policy"
	CheckMemory    = "memory_boundary"
	// ProbeTimeout and MaxVerifyProbes (and HelperTimeout in helper.go) are
	// exported only so internal/tenant can assert its lease and run-budget
	// arithmetic against them; nothing in production reads MaxVerifyProbes.
	// ProbeTimeout bounds one verification request. The longest Verify path with
	// maxSiblingProbes siblings makes 14 requests (1+1 endpoint, 2 identity, 2+3
	// isolation, 1+3 sessions, 1 toolsets). MaxVerifyProbes is 15: one probe of
	// deliberate slack, so adding a single probe does not instantly break the
	// lease arithmetic. TestVerifyProbeCount pins 14 <= measured <= 15.
	ProbeTimeout     = 15 * time.Second
	MaxVerifyProbes  = 15
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

// KnownToolsets is every toolset the deployed Hermes (v0.22.2) reports from
// /v1/toolsets. ValidateAllowedToolsets rejects names outside it, so Antares
// and the provisioner (which mirrors this list, pinned by
// TestProvisionerToolsetListsMatchAntares) agree on what a valid name is: a
// typo in ANTARES_TENANT_ALLOWED_TOOLSETS stops Antares at startup instead of
// crashlooping the helper.
var KnownToolsets = []string{
	"web", "browser", "terminal", "file", "code_execution", "vision", "video",
	"image_gen", "video_gen", "x_search", "tts", "stt", "skills", "todo",
	"kanban", "memory", "context_engine", "session_search", "connections",
	"clarify", "delegation", "cronjob", "discord", "discord_admin", "yuanbao",
	"computer_use", "a2a",
}

// OfferedToolsets are the toolsets an admin may turn on for tenants (Admin → Assistant tools): known, outside the
// floor, and reviewed as acting only inside the tenant's own profile or through the gateway's own provider keys.
// The rest of KnownToolsets (discord, discord_admin, yuanbao, computer_use, a2a, kanban, context_engine) is not
// offered: not reviewed for shared tenants. The provisioner mirrors this list (offeredToolsets) and refuses any
// other name, pinned by TestProvisionerToolsetListsMatchAntares.
var OfferedToolsets = []string{
	"web", "vision", "image_gen", "video_gen", "video", "tts", "stt", "x_search",
	"skills", "memory", "todo", "clarify",
}

// ValidateOfferedToolsets refuses a tenant policy that is empty, repeats a name, or names a toolset outside
// OfferedToolsets. It is the rule the provisioner applies to a request's toolsets.
func ValidateOfferedToolsets(names []string) error {
	if len(names) == 0 {
		return errors.New("allowed toolsets must not be empty")
	}
	for i, n := range names {
		if !slices.Contains(OfferedToolsets, n) {
			return errors.New("toolset " + safeName(n) + " is not offered to tenants")
		}
		if slices.Contains(names[:i], n) {
			return errors.New("toolset " + safeName(n) + " is listed twice")
		}
	}
	return nil
}

// DefaultAllowedToolsets is the documented default tenant set. It is data, not
// policy: the admin owns it (docs/runbooks/tenant-toolsets.md).
var DefaultAllowedToolsets = []string{"web", "vision", "image_gen", "skills", "memory", "todo", "clarify"}

// ValidateAllowedToolsets refuses a configured allow-list that includes a
// floor toolset or a name Hermes does not report. It is checked at startup,
// not at verification time, so a bad policy never starts. It is the same rule
// the provisioner applies to PROVISIONER_ALLOWED_TOOLSETS.
func ValidateAllowedToolsets(names []string) error {
	if len(names) == 0 {
		return errors.New("allowed toolsets must not be empty")
	}
	for _, n := range names {
		for _, d := range DeniedToolsets {
			if n == d {
				return errors.New("toolset " + safeName(n) + " crosses the profile boundary and cannot be allowed for tenants")
			}
		}
		known := false
		for _, k := range KnownToolsets {
			known = known || n == k
		}
		if !known {
			return errors.New("toolset " + safeName(n) + " is not a known toolset")
		}
	}
	return nil
}

// Result is one check's outcome. Reason is a fixed phrase, never upstream text.
type Result struct {
	Check       string
	Passed      bool
	Reason      string
	Remediation string // fixed helper action for a demonstrated divergence
}

const ReapplyToolsetPolicy = "reapply_toolset_policy"

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
	// Allowed, when set, is the tenant policy this run applied (the admin's choice); otherwise the verifier's own.
	Allowed []string
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
		client = &http.Client{Timeout: ProbeTimeout}
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
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
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
	return v.expectRefusalOr(ctx, path, key, what, false)
}

// expectRefusalOr is expectRefusal; with notFoundOK a 404 also counts. That is
// only right for a route that was never provisioned: the deployed Hermes answers
// 404 for it, which proves the tenant key opens nothing there. For the default
// profile and for sibling routes a 404 would mean a misrouted endpoint.
func (v *Verifier) expectRefusalOr(ctx context.Context, path, key, what string, notFoundOK bool) string {
	r, err := v.get(ctx, path, key)
	switch {
	case err != nil:
		return what + " did not answer"
	case refused(r.status), notFoundOK && r.status == http.StatusNotFound:
		return ""
	case r.status == http.StatusOK:
		return what + " accepted the request (status 200)"
	default:
		want := "401 or 403"
		if notFoundOK {
			want = "401, 403 or 404"
		}
		return what + " answered status " + strconv.Itoa(r.status) + ", expected " + want
	}
}

// Verify runs every check. It never stops at the first failure, so the admin
// sees everything that is wrong at once; it never returns OK on partial data.
func (v *Verifier) Verify(ctx context.Context, t Target) Report {
	if !ValidSlug(t.Profile) || t.Key == "" {
		return Report{{Check: CheckEndpoint, Reason: "no profile to verify"}}
	}
	sib := t.Siblings
	if len(sib) > maxSiblingProbes {
		sib = sib[:maxSiblingProbes]
	}
	for _, s := range sib {
		if !ValidSlug(s) || s == t.Profile {
			return Report{{Check: CheckIsolation, Reason: "invalid sibling target"}}
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
		return Result{Check: CheckEndpoint, Reason: "capabilities endpoint did not answer as expected"}
	}
	r, err := v.get(ctx, route(t.Profile)+"/v1/models", t.Key)
	if err != nil || r.status != http.StatusOK {
		return Result{Check: CheckEndpoint, Reason: "models endpoint did not answer"}
	}
	var m struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(r.body, &m) != nil || m.Object != "list" || m.Data == nil {
		return Result{Check: CheckEndpoint, Reason: "models endpoint answered in an unexpected shape"}
	}
	return Result{Check: CheckEndpoint, Passed: true}
}

// identity: the profile route is its own namespace. Hermes' verified API has no
// profile-name field, so identity is shown the only way it can be: the route
// accepts this profile's key and nothing that is not it. The default profile's
// route and a never-provisioned route must not accept the tenant's key.
func (v *Verifier) identity(ctx context.Context, t Target) Result {
	if why := v.expectRefusal(ctx, "/v1/models", t.Key, "the default profile route (tenant key)"); why != "" {
		return Result{Check: CheckIdentity, Reason: why}
	}
	if why := v.expectRefusalOr(ctx, route(absentSlug)+"/v1/models", t.Key, "an unprovisioned profile route (tenant key)", true); why != "" {
		return Result{Check: CheckIdentity, Reason: why}
	}
	return Result{Check: CheckIdentity, Passed: true}
}

// isolation: a missing key, a wrong key and every sibling's route refuse this
// tenant's key.
func (v *Verifier) isolation(ctx context.Context, t Target, siblings []string) Result {
	own := route(t.Profile) + "/v1/models"
	if why := v.expectRefusal(ctx, own, "", "the profile route (no key)"); why != "" {
		return Result{Check: CheckIsolation, Reason: why}
	}
	sum := sha256.Sum256([]byte("antares-verify-wrong-key:" + t.Key))
	if why := v.expectRefusal(ctx, own, hex.EncodeToString(sum[:]), "the profile route (wrong key)"); why != "" {
		return Result{Check: CheckIsolation, Reason: why}
	}
	for _, sib := range siblings {
		if why := v.expectRefusal(ctx, route(sib)+"/v1/models", t.Key, "another tenant's route (tenant key)"); why != "" {
			return Result{Check: CheckIsolation, Reason: why}
		}
	}
	return Result{Check: CheckIsolation, Passed: true}
}

// sessions: the profile lists its own sessions, no other tenant's route lists
// them for this key, and a new profile owns none. The list envelope is the one
// the deployed build (Hermes v0.22.2) returned for a fresh profile, as
// reproduced in issue #41 and committed as fixtures/sessions-empty.body.json
// (an empty list; the shape of a session item is still unverified). It is
// pinned by TestSessionCountDeployedEnvelope; anything this does not
// recognise fails closed.
func (v *Verifier) sessions(ctx context.Context, t Target, siblings []string) Result {
	r, err := v.get(ctx, route(t.Profile)+"/api/sessions?limit=1&offset=0", t.Key)
	if err != nil || r.status != http.StatusOK {
		return Result{Check: CheckSessions, Reason: "sessions endpoint did not answer"}
	}
	if t.Fresh {
		n, ok := sessionCount(r.body)
		if !ok {
			return Result{Check: CheckSessions, Reason: "sessions endpoint answered in an unrecognised shape"}
		}
		if n != 0 {
			return Result{Check: CheckSessions, Reason: "new profile already holds sessions"}
		}
	}
	for _, sib := range siblings {
		if why := v.expectRefusal(ctx, route(sib)+"/api/sessions?limit=1&offset=0", t.Key, "another tenant's sessions (tenant key)"); why != "" {
			return Result{Check: CheckSessions, Reason: why}
		}
	}
	return Result{Check: CheckSessions, Passed: true}
}

// sessionCount accepts the deployed envelope
// {"object":"list","data":[...],"limit":N,"offset":N,"has_more":bool}, a bare
// array, or an object whose "sessions" member is an array. Anything else is
// unrecognised, including {"data":[]} without "object":"list" (the shape a
// naive client sends).
func sessionCount(b []byte) (int, bool) {
	var env struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &env) == nil && env.Object == "list" && env.Data != nil {
		return len(env.Data), true
	}
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
	allowed := v.allowed
	if t.Allowed != nil {
		if err := ValidateAllowedToolsets(t.Allowed); err != nil {
			return Result{Check: CheckToolsets, Reason: "the tenant tool policy is invalid"}
		}
		allowed = map[string]bool{}
		for _, n := range t.Allowed {
			allowed[n] = true
		}
	}
	r, err := v.get(ctx, route(t.Profile)+"/v1/toolsets", t.Key)
	if err != nil || r.status != http.StatusOK {
		return Result{Check: CheckToolsets, Reason: "toolsets endpoint did not answer"}
	}
	var d struct {
		Object string `json:"object"`
		Data   []struct {
			Name    string `json:"name"`
			Enabled *bool  `json:"enabled"`
		} `json:"data"`
	}
	if json.Unmarshal(r.body, &d) != nil || d.Object != "list" || len(d.Data) == 0 {
		return Result{Check: CheckToolsets, Reason: "toolsets endpoint answered in an unexpected shape"}
	}
	seen := map[string]bool{}
	for _, ts := range d.Data {
		if ts.Name == "" || ts.Enabled == nil {
			return Result{Check: CheckToolsets, Reason: "toolsets endpoint answered in an unexpected shape"}
		}
		seen[ts.Name] = true
		if !*ts.Enabled {
			continue
		}
		for _, den := range DeniedToolsets {
			if ts.Name == den {
				return Result{Check: CheckToolsets, Reason: "boundary-crossing toolset " + den + " is enabled", Remediation: ReapplyToolsetPolicy}
			}
		}
		if !allowed[ts.Name] {
			return Result{Check: CheckToolsets, Reason: "toolset " + safeName(ts.Name) + " is enabled but not allowed", Remediation: ReapplyToolsetPolicy}
		}
	}
	// A denied toolset the profile does not report at all cannot be shown to be
	// off; the deployed API lists every toolset, so absence is a changed contract.
	for _, den := range DeniedToolsets {
		if !seen[den] {
			return Result{Check: CheckToolsets, Reason: "toolset " + den + " is not reported, so its state is unknown"}
		}
	}
	return Result{Check: CheckToolsets, Passed: true}
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
		return Result{Check: CheckMemory, Reason: "capabilities unavailable"}
	}
	if c.Features.MemoryWriteAPI == nil || *c.Features.MemoryWriteAPI {
		return Result{Check: CheckMemory, Reason: "a memory write API is exposed or its state is unknown"}
	}
	if c.Features.AdminConfigRW == nil || *c.Features.AdminConfigRW {
		return Result{Check: CheckMemory, Reason: "profile configuration is writable through the API or its state is unknown"}
	}
	if c.Auth.Required == nil || !*c.Auth.Required {
		return Result{Check: CheckMemory, Reason: "the API does not require authentication"}
	}
	return Result{Check: CheckMemory, Passed: true}
}
