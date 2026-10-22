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

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

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
