package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

var goodKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

// googleOK is the fabricated Google/origin configuration every valid fixture needs.
var googleOK = map[string]string{
	"ANTARES_GOOGLE_CLIENT_ID":     "client-id.example.test",
	"ANTARES_GOOGLE_CLIENT_SECRET": "fabricated-client-secret",
	"ANTARES_PUBLIC_ORIGIN":        "https://chat.example.test",
	"ANTARES_HERMES_URL":           "http://hermes.example.test:8642",
	"ANTARES_PROVISIONER_URL":      "http://provisioner.example.test:8750",
	"ANTARES_PROVISIONER_TOKEN":    "fabricated-provisioner-token-0123456789",
}

// env layers m over a valid Google configuration; raw is for tests of its absence.
func env(m map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := m[k]; ok {
			return v
		}
		return googleOK[k]
	}
}

func raw(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadRefusesMissingRequired(t *testing.T) {
	_, err := Load(env(nil))
	if err == nil || !strings.Contains(err.Error(), "ANTARES_DB_PATH") || !strings.Contains(err.Error(), "ANTARES_SECRET_KEY") {
		t.Fatalf("want both missing vars named, got %v", err)
	}
}

func TestLoadRejectsBadKey(t *testing.T) {
	for _, k := range []string{"not-base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": k})); err == nil {
			t.Errorf("key %q should be refused", k)
		}
	}
}

func TestLoadAdminPairTogether(t *testing.T) {
	base := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey, "ANTARES_ADMIN_GOOGLE_SUB": "sub-1"}
	if _, err := Load(env(base)); err == nil {
		t.Fatal("sub without email should be refused")
	}
	base["ANTARES_ADMIN_EMAIL"] = "admin@example.test"
	c, err := Load(env(base))
	if err != nil || !c.BootstrapAdmin() {
		t.Fatalf("want bootstrap configured, got %v %v", c, err)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	c, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(s, "kkkk") || strings.Contains(s, goodKey) {
			t.Fatalf("secret leaked: %s", s)
		}
	}
}

func TestLoadRequiresAbsoluteDBPath(t *testing.T) {
	for _, p := range []string{"antares.db", "./data/a.db", "../a.db"} {
		_, err := Load(env(map[string]string{"ANTARES_DB_PATH": p, "ANTARES_SECRET_KEY": goodKey}))
		if err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Errorf("%q should be refused as non-absolute: %v", p, err)
		}
	}
}

func TestSecretNeverLeaksThroughAnyVerbOrEncoder(t *testing.T) {
	raw := []byte(strings.Repeat("k", 32))
	cfg, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%08b", "%o", "%T%v"} {
		out = append(out, fmt.Sprintf(v, cfg.SecretKey), fmt.Sprintf(v, cfg), fmt.Sprintf(v, &cfg))
	}
	j, _ := json.Marshal(cfg.SecretKey)
	jc, _ := json.Marshal(cfg)
	out = append(out, string(j), string(jc))
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "key", cfg.SecretKey, "cfg", cfg)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "key", cfg.SecretKey)
	out = append(out, buf.String())
	hexKey := fmt.Sprintf("%x", raw)
	for _, s := range out {
		for _, bad := range []string{goodKey, hexKey, string(raw), "107 107 107", "[107"} {
			if strings.Contains(s, bad) {
				t.Errorf("secret leaked as %q in %q", bad, s)
			}
		}
	}
}

func TestLoadRefusesMissingGoogleConfig(t *testing.T) {
	base := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}
	_, err := Load(raw(base))
	if err == nil {
		t.Fatal("startup without Google configuration must fail; there is no degraded mode")
	}
	for _, name := range []string{"ANTARES_GOOGLE_CLIENT_ID", "ANTARES_GOOGLE_CLIENT_SECRET", "ANTARES_PUBLIC_ORIGIN"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should name %s: %v", name, err)
		}
		one := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}
		for k, v := range googleOK {
			if k != name {
				one[k] = v
			}
		}
		if _, err := Load(raw(one)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("missing %s alone must refuse startup, got %v", name, err)
		}
	}
}

func TestLoadPublicOriginMustBeBareHTTPS(t *testing.T) {
	for _, bad := range []string{"http://chat.example.test", "https://chat.example.test/app", "https://chat.example.test?x=1",
		"https://u:p@chat.example.test", "chat.example.test", "https://"} {
		if _, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey, "ANTARES_PUBLIC_ORIGIN": bad})); err == nil {
			t.Errorf("origin %q accepted", bad)
		}
	}
	c, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey, "ANTARES_PUBLIC_ORIGIN": "https://Chat.Example.test/"}))
	if err != nil || c.PublicOrigin != "https://chat.example.test" {
		t.Fatalf("origin = %q, err %v", c.PublicOrigin, err)
	}
}

func TestGoogleClientSecretNeverPrints(t *testing.T) {
	c, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}))
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprintf("%v %+v %#v %s %q", c, c, c, c.GoogleClientSecret, c.GoogleClientSecret)} {
		if strings.Contains(out, googleOK["ANTARES_GOOGLE_CLIENT_SECRET"]) {
			t.Fatalf("client secret leaked: %s", out)
		}
	}
}

func TestLoadHermesAndProvisionerSettings(t *testing.T) {
	base := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}
	c, err := Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.HermesURL != "http://hermes.example.test:8642" || c.ProvisionerURL != "http://provisioner.example.test:8750" || len(c.TenantToolsets) == 0 {
		t.Fatalf("config = %+v", c)
	}
	for _, name := range []string{"ANTARES_HERMES_URL", "ANTARES_PROVISIONER_URL", "ANTARES_PROVISIONER_TOKEN"} {
		m := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}
		for k, v := range googleOK {
			m[k] = v
		}
		m[name] = ""
		if _, err := Load(raw(m)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	for name, v := range map[string]string{
		"ANTARES_HERMES_URL":        "http://hermes:8642/p/x",
		"ANTARES_PROVISIONER_URL":   "http://user:pw@host:8750",
		"ANTARES_PROVISIONER_TOKEN": "too-short",
	} {
		m := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey, name: v}
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%q accepted: %v", name, v, err)
		}
	}
}

func TestProvisionerTokenNeverPrints(t *testing.T) {
	c, err := Load(env(map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey}))
	if err != nil {
		t.Fatal(err)
	}
	tok := "fabricated-provisioner-token-0123456789"
	b, _ := json.Marshal(c)
	var out bytes.Buffer
	slog.New(slog.NewTextHandler(&out, nil)).Info("cfg", "c", c)
	for _, s := range []string{fmt.Sprintf("%v %+v %#v %s", c, c, c, c.ProvisionerToken), string(b), out.String()} {
		if strings.Contains(s, tok) {
			t.Fatalf("provisioner token leaked: %s", s)
		}
	}
}

// A typo must stop Antares at startup and name the offender, not start cleanly
// and crashloop the provisioner (which refuses the same names).
func TestTenantToolsetPolicyRejectsUnknownNames(t *testing.T) {
	m := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey, "ANTARES_TENANT_ALLOWED_TOOLSETS": "web,visoin"}
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "ANTARES_TENANT_ALLOWED_TOOLSETS") || !strings.Contains(err.Error(), "visoin") {
		t.Fatalf("unknown toolset accepted or unnamed: %v", err)
	}
}

func TestTenantToolsetPolicyIsConfigurableButNotPastTheFloor(t *testing.T) {
	m := map[string]string{"ANTARES_DB_PATH": "/x/a.db", "ANTARES_SECRET_KEY": goodKey, "ANTARES_TENANT_ALLOWED_TOOLSETS": "web, memory ,todo"}
	c, err := Load(env(m))
	if err != nil || strings.Join(c.TenantToolsets, ",") != "web,memory,todo" {
		t.Fatalf("narrowed policy: %v %v", c.TenantToolsets, err)
	}
	for _, bad := range []string{"web,terminal", "file", "delegation", "cronjob", "session_search", "code_execution", "connections", "browser"} {
		m["ANTARES_TENANT_ALLOWED_TOOLSETS"] = bad
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "ANTARES_TENANT_ALLOWED_TOOLSETS") {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}
