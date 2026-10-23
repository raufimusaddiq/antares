package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

func TestAdminCmdsBackupRestoreCheck(t *testing.T) {
	ctx := context.Background()
	live := filepath.Join(t.TempDir(), "data", "antares.sqlite")
	env := func(k string) string {
		if k == "ANTARES_DB_PATH" {
			return live
		}
		return ""
	}
	db, err := store.Open(live)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.Migrations()); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out, errb bytes.Buffer
		err := runAdminCmd(ctx, append([]string{"antares"}, args...), env, &out, &errb)
		return out.String() + errb.String(), err
	}

	bak := filepath.Join(t.TempDir(), "x.bak")
	if out, err := run("backup", bak); err != nil {
		t.Fatal(out, err)
	}
	var buf bytes.Buffer
	if err := runAdminCmd(ctx, []string{"antares", "backup", "-"}, env, &buf, &bytes.Buffer{}); err != nil || buf.Len() == 0 {
		t.Fatalf("stream backup: %v len=%d", err, buf.Len())
	}
	if out, err := run("check", bak); err != nil || !strings.HasPrefix(out, "ok ") {
		t.Fatal(out, err)
	}
	db.Close()

	if _, err := run("restore", bak); err == nil {
		t.Error("restore without -keep-old/-discard-old was accepted")
	}
	if _, err := run("restore", "-keep-old", "/x", "-discard-old", bak); err == nil {
		t.Error("restore with both flags was accepted")
	}
	garbage := filepath.Join(t.TempDir(), "g")
	os.WriteFile(garbage, []byte("garbage garbage garbage"), 0o600)
	if _, err := run("restore", "-discard-old", garbage); err == nil {
		t.Error("garbage restore accepted")
	}
	if out, err := run("check", garbage); err == nil {
		t.Errorf("check passed garbage: %s", out)
	}
	if out, err := run("restore", "-discard-old", bak); err != nil {
		t.Fatal(out, err)
	}
	d2, err := sql.Open("sqlite", "file:"+live+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	var n int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("restored db: %d %v", n, err)
	}
}
