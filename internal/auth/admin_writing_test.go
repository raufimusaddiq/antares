package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Admin → Capabilities → Writing rules (specs/005-antislop-writing): the state, the switch, the counts. Counts only.

type writingJSON struct {
	Enabled bool
	Version string
	Source  string
	License string
	Last24h struct{ With, Without, NotApplied int64 }
	Warning string
}

func readWriting(t *testing.T, b *browser) (writingJSON, string) {
	t.Helper()
	w := b.get("/api/admin/writing-rules")
	if w.Code != 200 {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	var v writingJSON
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v, w.Body.String()
}

func TestOnlyAnAdministratorReadsOrSwitchesTheWritingRules(t *testing.T) {
	h, admin, user := modelsHarness(t)
	for name, b := range map[string]*browser{"ordinary user": user, "anonymous": h.browser()} {
		if w := b.get("/api/admin/writing-rules"); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s GET = %d", name, w.Code)
		}
		if w := b.send("PUT", "/api/admin/writing-rules", `{"enabled":false}`, sameOriginHdr); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s PUT = %d", name, w.Code)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM writing_rules_setting`); n != 0 {
		t.Fatalf("a refused call stored %d rows", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'writing_rules.set'`); n != 0 {
		t.Fatalf("a refused call was audited %d times", n)
	}
	v, raw := readWriting(t, admin)
	if !v.Enabled || v.Version != "v1" || !strings.Contains(v.Source, "anti-slop") || !strings.Contains(v.License, "Miqdad Badjuber (antislop)") || v.Warning != "" {
		t.Errorf("read: %+v", v)
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &keys)
	for k := range keys { // nothing but the declared fields: no account, chat, post or text of the rules
		switch k {
		case "enabled", "version", "source", "license", "last24h", "warning":
		default:
			t.Errorf("the read carries an undeclared field %q", k)
		}
	}
	if strings.Contains(raw, "Never invent") || strings.Contains(raw, "@") {
		t.Errorf("the read carries rules text or an address: %s", raw)
	}
}

func TestTheWritingRulesSwitchIsStrictAndAudited(t *testing.T) {
	h, admin, _ := modelsHarness(t)
	for name, body := range map[string]string{
		"missing":       `{}`,
		"not boolean":   `{"enabled":"no"}`,
		"unknown":       `{"enabled":false,"version":"v2"}`,
		"trailing":      `{"enabled":false}{}`,
		"not an object": `false`,
	} {
		if c := admin.send("PUT", "/api/admin/writing-rules", body, sameOriginHdr).Code; c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	if v, _ := readWriting(t, admin); !v.Enabled {
		t.Fatal("a refused body switched it off")
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'writing_rules.set'`); n != 0 {
		t.Fatalf("%d audit rows after refusals", n)
	}
	if c := admin.send("PUT", "/api/admin/writing-rules", `{"enabled":false}`, nil).Code; c != 403 {
		t.Errorf("without the same-origin header: %d", c)
	}
	if w := admin.send("PUT", "/api/admin/writing-rules", `{"enabled":false}`, sameOriginHdr); w.Code != 200 || strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("off: %d %s", w.Code, w.Body)
	}
	if v, _ := readWriting(t, admin); v.Enabled {
		t.Fatal("still on")
	}
	admin.send("PUT", "/api/admin/writing-rules", `{"enabled":true}`, sameOriginHdr)
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'writing_rules.set'`); n != 2 {
		t.Errorf("%d audit rows, want 2", n)
	}
}

// The warning is for runs that should have had the rules and did not: not while switched off, not for runs before the migration.
func TestTheNotAppliedWarningNeedsRunsThatShouldHaveHadTheRules(t *testing.T) {
	h, admin, _ := modelsHarness(t)
	now := time.Now().UTC()
	h.a.now = func() time.Time { return now }
	var applied string
	if err := h.db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version = 29`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse("2006-01-02T15:04:05.000000000Z", applied)
	var uid string
	h.db.QueryRow(`SELECT id FROM users WHERE role = 'USER' LIMIT 1`).Scan(&uid)
	mk := func(id, created, rules string) {
		var rv any
		if rules != "" {
			rv = rules
		}
		if _, err := h.db.Exec(`INSERT INTO tenant_bindings (id, user_id, profile_name, routing_mode, upstream_endpoint_id, credential_ciphertext, credential_key_version, credential_fingerprint, memory_scope_key, status, created_at)
			SELECT 'b-'||?, ?, 'p-'||?, 'MUX_PREFIX','ep',x'00',1,'fp-'||?,'m-'||?,'READY','t' WHERE NOT EXISTS (SELECT 1 FROM tenant_bindings WHERE user_id = ?)`, id, uid, id, id, id, uid); err != nil {
			t.Fatal(err)
		}
		h.db.Exec(`INSERT OR IGNORE INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at) SELECT 'chat1', ?, id, 's', 'ACTIVE','t','t' FROM tenant_bindings WHERE user_id = ?`, uid, uid)
		if _, err := h.db.Exec(`INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at, writing_rules) VALUES (?,?,'chat1',?,'COMPLETED',?,?,?)`, id, uid, id, created, created, rv); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(d time.Duration) string { return now.Add(d).Format("2006-01-02T15:04:05.000000000Z") }
	_ = at
	mk("old", stamp(-2*time.Hour), "") // before the migration if applied_at is later; within 24h either way
	mk("v1", stamp(-time.Minute), "v1")
	mk("off", stamp(-time.Minute), "off")
	v, _ := readWriting(t, admin)
	if v.Last24h.With != 1 || v.Last24h.Without != 2 {
		t.Fatalf("counts %+v", v.Last24h)
	}
	if v.Warning != "" && v.Last24h.NotApplied == 0 {
		t.Fatalf("warning without a count: %+v", v)
	}
	mk("lost", stamp(time.Minute), "") // after the migration, expected, not attached
	v, _ = readWriting(t, admin)
	if v.Last24h.NotApplied < 1 || v.Warning != "not_applied" {
		t.Fatalf("a run without its rules did not raise the warning: %+v", v)
	}
	admin.send("PUT", "/api/admin/writing-rules", `{"enabled":false}`, sameOriginHdr)
	if v, _ = readWriting(t, admin); v.Warning != "" {
		t.Fatalf("a warning while switched off: %+v", v)
	}
}
