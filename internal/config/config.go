// Package config loads Antares' runtime configuration from the environment.
// Secrets have no defaults: a missing required value refuses startup.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/quota"
)

// Secret holds key material. Every way of rendering it is redacted: fmt verbs
// (Format covers %v %s %q %x %d ..., which Stringer alone does not), JSON/text
// encoders and slog.
type Secret []byte

const redacted = "[redacted]"

func (Secret) String() string   { return redacted }
func (Secret) GoString() string { return redacted }

func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }

type Limits = quota.Limits

// Config is the validated configuration.
type Config struct {
	Limits    Limits
	Addr      string
	DBPath    string
	StaticDir string // development override; empty serves the embedded bundle
	SecretKey Secret // 32-byte master key for secrets at rest

	// Google OIDC login (AUTH-001). All three are required: there is no
	// degraded mode without a way to sign in.
	GoogleClientID     string
	GoogleClientSecret Secret
	PublicOrigin       string // https origin the browser reaches Antares on, e.g. https://chat.example.test

	// Hermes and its host-side provisioning helper (PRD §5.3). Required: without
	// them no approved account could ever reach READY.
	HermesURL        string // private origin of the Hermes API, e.g. http://hermes:8642
	ProvisionerURL   string // origin of tools/hermes-provisioner as reachable from this container
	ProvisionerToken Secret // its bearer token
	// TenantToolsets are the toolsets a tenant profile may have enabled. The
	// floor in hermes.DeniedToolsets cannot be allowed by configuration.
	TenantToolsets []string

	// Web Push (UXP-002). Both set, or neither: with neither, notifications are
	// reported as not configured and nothing is ever sent.
	VAPIDPrivateKey Secret // raw 32-byte P-256 scalar
	VAPIDSubject    string // mailto: or https: contact for the push services

	// Admin bootstrap. All three are set, or none (no bootstrap).
	AdminIssuer string
	AdminSub    string
	AdminEmail  string
}

const defaultIssuer = "https://accounts.google.com"

// Load reads configuration through getenv and reports every problem at once.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Addr:        orDefault(getenv("ANTARES_ADDR"), ":8080"),
		DBPath:      getenv("ANTARES_DB_PATH"),
		StaticDir:   getenv("ANTARES_STATIC_DIR"),
		AdminIssuer: orDefault(getenv("ANTARES_ADMIN_GOOGLE_ISSUER"), defaultIssuer),
		AdminSub:    getenv("ANTARES_ADMIN_GOOGLE_SUB"),
		AdminEmail:  getenv("ANTARES_ADMIN_EMAIL"),

		GoogleClientID: getenv("ANTARES_GOOGLE_CLIENT_ID"),
	}
	var problems []string
	c.Limits = quota.DefaultLimits()
	for _, limit := range []struct {
		name string
		dst  *int
	}{
		{"ANTARES_RUNS_PER_MINUTE", &c.Limits.RunsPerMinute},
		{"ANTARES_MESSAGES_PER_MINUTE", &c.Limits.MessagesPerMinute},
		{"ANTARES_UPLOADS_PER_MINUTE", &c.Limits.UploadsPerMinute},
		{"ANTARES_ARTIFACTS_PER_MINUTE", &c.Limits.ArtifactsPerMinute},
		{"ANTARES_HISTORY_PER_MINUTE", &c.Limits.HistoryPerMinute},
		{"ANTARES_SSE_CONNECTIONS", &c.Limits.SSEConnections},
	} {
		if raw := getenv(limit.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				problems = append(problems, limit.name+" must be a positive integer")
			} else {
				*limit.dst = n
			}
		}
	}
	switch {
	case c.DBPath == "":
		problems = append(problems, "ANTARES_DB_PATH is required")
	case !filepath.IsAbs(c.DBPath):
		problems = append(problems, "ANTARES_DB_PATH must be an absolute path")
	}
	switch raw := getenv("ANTARES_SECRET_KEY"); {
	case raw == "":
		problems = append(problems, "ANTARES_SECRET_KEY is required (base64 of 32 random bytes)")
	default:
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			problems = append(problems, "ANTARES_SECRET_KEY must be base64 of exactly 32 bytes")
		} else {
			c.SecretKey = key
		}
	}
	if c.GoogleClientID == "" {
		problems = append(problems, "ANTARES_GOOGLE_CLIENT_ID is required")
	}
	if raw := getenv("ANTARES_GOOGLE_CLIENT_SECRET"); raw == "" {
		problems = append(problems, "ANTARES_GOOGLE_CLIENT_SECRET is required")
	} else {
		c.GoogleClientSecret = Secret(raw)
	}
	switch origin, err := normalizeOrigin(getenv("ANTARES_PUBLIC_ORIGIN")); {
	case getenv("ANTARES_PUBLIC_ORIGIN") == "":
		problems = append(problems, "ANTARES_PUBLIC_ORIGIN is required (https origin, no path)")
	case err != nil:
		problems = append(problems, "ANTARES_PUBLIC_ORIGIN "+err.Error())
	default:
		c.PublicOrigin = origin
	}
	for _, u := range []struct {
		name, raw string
		dst       *string
	}{
		{"ANTARES_HERMES_URL", getenv("ANTARES_HERMES_URL"), &c.HermesURL},
		{"ANTARES_PROVISIONER_URL", getenv("ANTARES_PROVISIONER_URL"), &c.ProvisionerURL},
	} {
		switch err := hermes.ValidateOrigin(u.raw); {
		case u.raw == "":
			problems = append(problems, u.name+" is required (http(s) origin, no path)")
		case err != nil:
			problems = append(problems, u.name+" "+err.Error())
		default:
			*u.dst = strings.TrimRight(u.raw, "/")
		}
	}
	switch tok := getenv("ANTARES_PROVISIONER_TOKEN"); {
	case tok == "":
		problems = append(problems, "ANTARES_PROVISIONER_TOKEN is required")
	case len(tok) < hermes.MinTokenLen:
		problems = append(problems, fmt.Sprintf("ANTARES_PROVISIONER_TOKEN must be at least %d characters", hermes.MinTokenLen))
	default:
		c.ProvisionerToken = Secret(tok)
	}
	c.TenantToolsets = hermes.DefaultAllowedToolsets
	if raw := getenv("ANTARES_TENANT_ALLOWED_TOOLSETS"); raw != "" {
		c.TenantToolsets = nil
		for _, n := range strings.Split(raw, ",") {
			if n = strings.TrimSpace(n); n != "" {
				c.TenantToolsets = append(c.TenantToolsets, n)
			}
		}
	}
	if err := hermes.ValidateAllowedToolsets(c.TenantToolsets); err != nil {
		problems = append(problems, "ANTARES_TENANT_ALLOWED_TOOLSETS: "+err.Error())
	}
	rawVAPID, subject := getenv("ANTARES_VAPID_PRIVATE_KEY"), getenv("ANTARES_VAPID_SUBJECT")
	switch {
	case rawVAPID == "" && subject == "":
	case rawVAPID == "" || subject == "":
		problems = append(problems, "ANTARES_VAPID_PRIVATE_KEY and ANTARES_VAPID_SUBJECT must be set together")
	default:
		key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(rawVAPID, "="))
		if err != nil || len(key) != 32 {
			problems = append(problems, "ANTARES_VAPID_PRIVATE_KEY must be base64url of exactly 32 bytes")
		} else {
			c.VAPIDPrivateKey = key
		}
		if !strings.HasPrefix(subject, "mailto:") && !strings.HasPrefix(subject, "https://") {
			problems = append(problems, "ANTARES_VAPID_SUBJECT must be a mailto: or https:// URL")
		} else {
			c.VAPIDSubject = subject
		}
	}
	if (c.AdminSub == "") != (c.AdminEmail == "") {
		problems = append(problems, "ANTARES_ADMIN_GOOGLE_SUB and ANTARES_ADMIN_EMAIL must be set together")
	}
	if len(problems) > 0 {
		return Config{}, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return c, nil
}

// normalizeOrigin accepts only a bare https origin: the session cookie is
// Secure and the OAuth redirect URI must match Google's registration exactly,
// so a path, query or http scheme is a configuration error, not something to fix up.
func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("must be an https origin with no path, query or credentials")
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// PushConfigured reports whether a VAPID key pair is set.
func (c Config) PushConfigured() bool { return len(c.VAPIDPrivateKey) > 0 }

// BootstrapAdmin reports whether an admin identity is configured.
func (c Config) BootstrapAdmin() bool { return c.AdminSub != "" }

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
