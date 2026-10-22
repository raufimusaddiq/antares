// Package config loads Antares' runtime configuration from the environment.
// Secrets have no defaults: a missing required value refuses startup.
package config

import (
	"encoding/base64"
	"errors"
	"strings"
)

// Secret holds key material. It prints as redacted so a stray %v or %+v in a
// log line cannot leak it.
type Secret []byte

func (Secret) String() string   { return "[redacted]" }
func (Secret) GoString() string { return "[redacted]" }

// Config is the validated configuration.
type Config struct {
	Addr      string
	DBPath    string
	StaticDir string
	SecretKey Secret // 32-byte master key for secrets at rest

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
		StaticDir:   orDefault(getenv("ANTARES_STATIC_DIR"), "web/dist"),
		AdminIssuer: orDefault(getenv("ANTARES_ADMIN_GOOGLE_ISSUER"), defaultIssuer),
		AdminSub:    getenv("ANTARES_ADMIN_GOOGLE_SUB"),
		AdminEmail:  getenv("ANTARES_ADMIN_EMAIL"),
	}
	var problems []string
	if c.DBPath == "" {
		problems = append(problems, "ANTARES_DB_PATH is required")
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
	if (c.AdminSub == "") != (c.AdminEmail == "") {
		problems = append(problems, "ANTARES_ADMIN_GOOGLE_SUB and ANTARES_ADMIN_EMAIL must be set together")
	}
	if len(problems) > 0 {
		return Config{}, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return c, nil
}

// BootstrapAdmin reports whether an admin identity is configured.
func (c Config) BootstrapAdmin() bool { return c.AdminSub != "" }

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
