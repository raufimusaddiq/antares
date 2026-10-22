package config

import (
	"encoding/base64"
	"fmt"
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
