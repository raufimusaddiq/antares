// Package hermes talks to the two things Antares may reach on the Hermes side:
// the host-side provisioning helper (tools/hermes-provisioner) and a tenant's
// own Hermes API route. It builds no URL from user input, follows no redirect,
// bounds every response and never puts a credential in an error.
package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// slugRE is the helper's own rule (tools/hermes-provisioner/slug.go). It
// is checked here too: the slug is the only value that ever reaches a path.
var slugRE = regexp.MustCompile(`^u_[a-z0-9]{8,32}$`)

// ValidSlug reports whether s is a profile slug the helper would accept.
func ValidSlug(s string) bool { return slugRE.MatchString(s) }

const (
	maxBody = 1 << 20
	// HelperTimeout must exceed the helper's request budget plus its write slack
	// (opBudget 330s + writeSlack 30s in tools/hermes-provisioner/hermes.go), or
	// this caller gives up while the helper is still creating the profile and
	// writing a key. TestHelperTimeoutOutlastsProvisionerBudget enforces it.
	HelperTimeout = 390 * time.Second
)

// Failure classes. Retryable failures are worth another attempt with backoff;
// permanent ones need an operator (a wrong token will not fix itself).
var (
	ErrRetryable = errors.New("hermes: transient failure")
	ErrPermanent = errors.New("hermes: permanent failure")
	// ErrKeyExists is the helper's 409 from /provision: the profile already has a
	// key Antares does not hold (a lost response). Recovery is /rotate.
	ErrKeyExists = errors.New("hermes: profile already has a key")
	// ErrNotProvisioned identifies /rotate responses recoverable by /provision.
	ErrNotProvisioned = errors.New("hermes: profile or key missing")
)

// Failure is a classified helper failure. Detail comes from a fixed
// vocabulary; it never carries upstream response text.
type Failure struct {
	Class  error
	Detail string
}

func (f *Failure) Error() string { return f.Detail }
func (f *Failure) Unwrap() error { return f.Class }

func transient(detail string) *Failure { return &Failure{ErrRetryable, detail} }
func permanent(detail string) *Failure { return &Failure{ErrPermanent, detail} }

// Credential is what the helper returns once. Its String forms are redacted.
type Credential struct {
	Profile string
	Key     string
}

func (Credential) String() string   { return "[redacted credential]" }
func (Credential) GoString() string { return "[redacted credential]" }

func (c Credential) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[redacted credential]")) }

// Helper is a client for tools/hermes-provisioner.
type Helper struct {
	base   *url.URL
	token  string
	client *http.Client
}

// MinTokenLen is the helper's own minimum for its bearer token.
const MinTokenLen = 32

// parseOrigin accepts only a bare http(s) origin: these URLs receive bearer
// credentials, so a path, query or userinfo is a configuration error rather
// than something to normalise.
func parseOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("must be an http(s) origin with no path, query or credentials")
	}
	return u, nil
}

// ValidateOrigin reports whether raw is acceptable as a Hermes or helper URL.
func ValidateOrigin(raw string) error {
	_, err := parseOrigin(raw)
	return err
}

// noRedirect makes any 3xx an answer instead of a hop: the bearer token must not
// follow a redirect to a host nobody configured.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// NewHelper builds a client for the helper at baseURL (scheme://host[:port],
// no path). token is the helper's bearer token. client may be nil.
func NewHelper(baseURL, token string, client *http.Client) (*Helper, error) {
	u, err := parseOrigin(baseURL)
	if err != nil {
		return nil, errors.New("hermes: provisioner URL " + err.Error())
	}
	if len(token) < MinTokenLen {
		return nil, errors.New("hermes: provisioner token must be at least 32 characters")
	}
	if client == nil {
		client = &http.Client{Timeout: HelperTimeout}
	}
	c := *client
	c.CheckRedirect = noRedirect
	return &Helper{base: &url.URL{Scheme: u.Scheme, Host: u.Host}, token: token, client: &c}, nil
}

// Provision creates the profile (if absent), applies the tenant toolset policy and returns its key once. On the
// helper's 409 (a profile that already has a key) it returns ErrKeyExists; use Rotate to obtain a fresh one.
// toolsets is the admin's tenant policy (OfferedToolsets); nil leaves the helper's own default.
func (h *Helper) Provision(ctx context.Context, slug string, toolsets []string) (Credential, error) {
	return h.issue(ctx, "/provision", slug, toolsets)
}

// Rotate reapplies the toolset policy, replaces the profile's key and returns the new one once.
func (h *Helper) Rotate(ctx context.Context, slug string, toolsets []string) (Credential, error) {
	return h.issue(ctx, "/rotate", slug, toolsets)
}

func (h *Helper) issue(ctx context.Context, path, slug string, toolsets []string) (Credential, error) {
	if !ValidSlug(slug) {
		return Credential{}, permanent("invalid profile name")
	}
	if toolsets != nil && ValidateOfferedToolsets(toolsets) != nil {
		return Credential{}, permanent("invalid tenant tool policy")
	}
	req := struct {
		Slug     string   `json:"slug"`
		Toolsets []string `json:"toolsets,omitempty"`
	}{slug, toolsets}
	body, _ := json.Marshal(req)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base.String()+path, bytes.NewReader(body))
	if err != nil {
		return Credential{}, permanent("could not build helper request")
	}
	hr.Header.Set("Authorization", "Bearer "+h.token)
	hr.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(hr)
	if err != nil {
		// err can echo the URL; the URL holds no secret, but the text is dropped anyway.
		return Credential{}, transient("provisioning helper unreachable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return Credential{}, transient("provisioning helper sent an unreadable response")
	}
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusConflict && path == "/provision":
		return Credential{}, ErrKeyExists
	case path == "/rotate" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict):
		return Credential{}, &Failure{errors.Join(ErrPermanent, ErrNotProvisioned), "provisioning helper reported a missing profile or key"}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Credential{}, permanent("provisioning helper refused our token")
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict:
		return Credential{}, permanent("provisioning helper rejected the request")
	default: // 5xx, 3xx and anything unexpected
		return Credential{}, transient("provisioning helper failed")
	}
	var out struct {
		Profile string `json:"profile"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Credential{}, transient("provisioning helper sent an unreadable response")
	}
	// Fail closed on an answer that is not for the profile we asked about, or
	// that carries no usable key.
	if out.Profile != slug {
		return Credential{}, permanent("provisioning helper answered for a different profile")
	}
	if len(out.Key) < 16 || strings.ContainsAny(out.Key, " \r\n\t") {
		return Credential{}, permanent("provisioning helper returned no usable key")
	}
	return Credential{Profile: out.Profile, Key: out.Key}, nil
}

// maxMedia bounds one generated media file (the helper's own limit is 25 MiB).
const maxMedia = 25 << 20

// Media returns a file Hermes generated in the profile's own cache (named MEDIA:<path> in a reply) and its media
// type. The helper refuses any other path: ErrMediaNotFound; a helper that cannot answer is a transient Failure.
func (h *Helper) Media(ctx context.Context, slug, path string) ([]byte, string, error) {
	if !ValidSlug(slug) {
		return nil, "", ErrMediaNotFound
	}
	body, _ := json.Marshal(map[string]string{"slug": slug, "path": path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base.String()+"/media", bytes.NewReader(body))
	if err != nil {
		return nil, "", ErrMediaNotFound
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, "", transient("provisioning helper unreachable")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", ErrMediaNotFound // refused or absent: the helper says so on purpose
	case resp.StatusCode != http.StatusOK:
		return nil, "", transient("provisioning helper failed to read the media") // a broken helper is not "no media"
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMedia+1))
	if err != nil || len(raw) == 0 || len(raw) > maxMedia {
		return nil, "", ErrMediaNotFound
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

// Skills lists the skills one profile can load, through the helper's POST /skills (Hermes v0.22.5's own
// GET /v1/skills fails on every call: tools/hermes-provisioner/skills.go). A profile the helper does not find is an
// empty list, never an error the caller could mistake for "Hermes is down".
func (h *Helper) Skills(ctx context.Context, slug string) ([]Skill, error) {
	if !ValidSlug(slug) {
		return nil, permanent("invalid profile name")
	}
	body, _ := json.Marshal(map[string]string{"slug": slug})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base.String()+"/skills", bytes.NewReader(body))
	if err != nil {
		return nil, permanent("could not build helper request")
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, transient("provisioning helper unreachable")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, transient("provisioning helper failed to list skills")
	}
	var d struct {
		Object string  `json:"object"`
		Data   []Skill `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody || json.Unmarshal(raw, &d) != nil || d.Object != "list" {
		return nil, transient("provisioning helper sent an unreadable skills list")
	}
	out := make([]Skill, 0, len(d.Data))
	for _, s := range d.Data {
		if len(out) >= maxListed {
			break
		}
		if printable(s.Name) {
			out = append(out, Skill{Name: s.Name, Description: clip(s.Description, 300), Category: clip(s.Category, 60)})
		}
	}
	return out, nil
}

// ErrMediaNotFound is any refused or missing media file.
var ErrMediaNotFound = errors.New("hermes: media not found")
