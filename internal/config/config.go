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
	"strings"
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

// Config is the validated configuration.
type Config struct {
	Addr      string
	DBPath    string
	StaticDir string // development override; empty serves the embedded bundle
	SecretKey Secret // 32-byte master key for secrets at rest

	// Google OIDC login (AUTH-001). All three are required: there is no
	// degraded mode without a way to sign in.
	GoogleClientID     string
	GoogleClientSecret Secret
	PublicOrigin       string // https origin the browser reaches Antares on, e.g. https://chat.example.test

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

// BootstrapAdmin reports whether an admin identity is configured.
func (c Config) BootstrapAdmin() bool { return c.AdminSub != "" }

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
