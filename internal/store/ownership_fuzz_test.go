package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// Ownership property (PRD §25 UXP-ACC-005, security-invariants §4): whatever
// sequence of operations arrives, in whatever order, with ids that are real,
// another tenant's, unknown or malformed, a caller reaches only what it owns,
// and a terminal run never moves again. The model below is the specification;
// the store must agree with it on every step.
//
// Run as a plain test (seeded programs, every `go test`) and as a native fuzz
// target (`make fuzz`, bounded by -fuzztime).

type ownModel struct {
	chatOwner map[string]string // chat id -> user
	runOwner  map[string]string // run id -> user
	runChat   map[string]string
	runStatus map[string]string
	chatIDs   []string
	runIDs    []string
}

// hostile ids: malformed, empty, wrong case, injection-shaped, over-long.
var hostileIDs = []string{
	"", " ", "%", "%00", "*", "../../etc/passwd", "' OR '1'='1", "\" OR \"\"=\"", "1; DROP TABLE chats;--",
	"0123456789ABCDEF0123456789ABCDEF", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdef0",
	"00000000000000000000000000000000", "ffffffffffffffffffffffffffffffff", "null", "undefined", "\xff\xfe",
	"é0123456789abcdef0123456789abcd",
}

func (m *ownModel) pickID(kind byte, sel byte) string {
	pool := m.chatIDs
	if kind%2 == 1 {
		pool = m.runIDs
	}
	n := len(pool) + len(hostileIDs)
	i := int(sel) % n
	if i < len(pool) {
		return pool[i]
	}
	return hostileIDs[i-len(pool)]
}

// runOwnershipProgram runs one program against a shared database. Every program
// creates its own three users (fresh ids), so programs cannot see each other's
// rows through the model, while the rows still sit side by side in the tables:
// a query that forgot its ownership clause would find them.
func runOwnershipProgram(t testing.TB, db *sql.DB, prog []byte, maxSteps int) {
	ctx := context.Background()
	users := make([]string, 3)
	binds := make([]string, 3)
	for i := range users {
		users[i] = fmt.Sprintf("u%d-%s", i, NewID())
		mkUserTB(t, db, users[i])
		binds[i] = mkBindingTB(t, db, users[i], fmt.Sprintf("u_%s", NewID()[:10]))
	}
	m := &ownModel{chatOwner: map[string]string{}, runOwner: map[string]string{}, runChat: map[string]string{}, runStatus: map[string]string{}}
	keySeq := 0
	for i := 0; i+2 < len(prog) && i < 3*maxSteps; i += 3 {
		op, who, sel := prog[i]%8, int(prog[i+1])%3, prog[i+2]
		actor := users[who]
		switch op {
		case 0: // create a chat
			c, err := CreateChat(ctx, db, actor, binds[who], fmt.Sprintf("sess-%d-%d", who, i), "t", t0)
			if err != nil {
				t.Fatalf("step %d: create chat: %v", i, err)
			}
			if c.UserID != actor {
				t.Fatalf("step %d: chat created for %s came back owned by %s", i, actor, c.UserID)
			}
			m.chatOwner[c.ID] = actor
			m.chatIDs = append(m.chatIDs, c.ID)
		case 1: // read a chat
			id := m.pickID(0, sel)
			c, err := GetChat(ctx, db, actor, id)
			own := m.chatOwner[id] == actor
			switch {
			case own && err != nil:
				t.Fatalf("step %d: %s cannot read own chat %s: %v", i, actor, id, err)
			case !own && !errors.Is(err, ErrNoChat):
				t.Fatalf("step %d: %s reached chat %q owned by %q: %+v %v", i, actor, id, m.chatOwner[id], c, err)
			case !own && c.ID != "":
				t.Fatalf("step %d: a refused read returned data: %+v", i, c)
			}
		case 2: // admit a run on a chat (fresh key, or a key already used by someone)
			id := m.pickID(0, sel)
			key := fmt.Sprintf("idem-key-%06d", keySeq)
			if sel%5 == 0 && keySeq > 0 {
				key = fmt.Sprintf("idem-key-%06d", int(sel)%keySeq) // may collide with another user's key
			} else {
				keySeq++
			}
			r, created, err := AdmitRun(ctx, db, actor, id, key, t0)
			own := m.chatOwner[id] == actor
			switch {
			case !own && !errors.Is(err, ErrNoChat):
				t.Fatalf("step %d: %s admitted/was answered on chat %q owned by %q: %+v %v", i, actor, id, m.chatOwner[id], r, err)
			case own && err == nil:
				if r.UserID != actor || r.ChatID != id || m.runOwner[r.ID] != "" && m.runOwner[r.ID] != actor {
					t.Fatalf("step %d: run %+v does not belong to %s on %s", i, r, actor, id)
				}
				if created {
					m.runOwner[r.ID], m.runChat[r.ID], m.runStatus[r.ID] = actor, id, RunPending
					m.runIDs = append(m.runIDs, r.ID)
				}
			case own && !errors.Is(err, ErrRunActive):
				t.Fatalf("step %d: own chat admit: %v", i, err)
			}
		case 3: // read a run
			id := m.pickID(1, sel)
			r, err := GetRun(ctx, db, actor, id)
			own := m.runOwner[id] == actor
			switch {
			case own && err != nil:
				t.Fatalf("step %d: %s cannot read own run: %v", i, actor, err)
			case !own && !errors.Is(err, ErrNoChat):
				t.Fatalf("step %d: %s reached run %q owned by %q: %+v", i, actor, id, m.runOwner[id], r)
			case !own && r.ID != "":
				t.Fatalf("step %d: a refused run read returned data: %+v", i, r)
			}
		case 4, 5, 6: // move a run to a terminal or running state
			id := m.pickID(1, sel)
			to := map[byte]string{4: RunCancelled, 5: RunCompleted, 6: RunRunning}[op]
			moved, err := MoveRun(ctx, db, actor, id, []string{RunPending, RunRunning, RunDisconnected}, to, "", "", t0)
			if err != nil {
				t.Fatalf("step %d: move: %v", i, err)
			}
			own := m.runOwner[id] == actor
			switch {
			case moved && !own:
				t.Fatalf("step %d: %s moved run %q owned by %q", i, actor, id, m.runOwner[id])
			case own && moved && RunTerminal(m.runStatus[id]):
				t.Fatalf("step %d: terminal run %s (%s) moved to %s", i, id, m.runStatus[id], to)
			case own && !moved && !RunTerminal(m.runStatus[id]):
				t.Fatalf("step %d: live run %s (%s) refused its owner's move", i, id, m.runStatus[id])
			case moved:
				m.runStatus[id] = to
			}
		case 7: // list: exactly the caller's chats
			page, more, err := ListChats(ctx, db, actor, 1000, nil, false)
			if err != nil || more {
				t.Fatalf("step %d: list: more=%v %v", i, more, err)
			}
			want := 0
			for _, o := range m.chatOwner {
				if o == actor {
					want++
				}
			}
			if len(page) != want {
				t.Fatalf("step %d: %s lists %d chats, owns %d", i, actor, len(page), want)
			}
			for _, c := range page {
				if m.chatOwner[c.ID] != actor || c.UserID != actor {
					t.Fatalf("step %d: %s's list holds chat %s of %s", i, actor, c.ID, c.UserID)
				}
			}
		}
	}
	// What is stored agrees with the model: no run changed owner or status behind our back.
	for id, owner := range m.runOwner {
		r, err := GetRun(ctx, db, owner, id)
		if err != nil || r.Status != m.runStatus[id] || r.ChatID != m.runChat[id] {
			t.Fatalf("run %s: stored %+v (%v), model %s on %s", id, r, err, m.runStatus[id], m.runChat[id])
		}
	}
}

// Deterministic programs: always run, so a regression fails the ordinary suite.
func TestOwnershipProperty(t *testing.T) {
	seed := uint64(0x9e3779b97f4a7c15)
	next := func() byte { // xorshift: reproducible without math/rand's global state
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return byte(seed >> 24)
	}
	db := openDB(t)
	for n := 0; n < 12; n++ {
		prog := make([]byte, 3*120)
		for i := range prog {
			prog[i] = next()
		}
		t.Run(fmt.Sprintf("program-%02d", n), func(t *testing.T) { runOwnershipProgram(t, db, prog, 120) })
	}
}

func FuzzOwnership(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 1, 0, 1, 1, 0, 2, 1, 0, 3, 2, 0, 4, 1, 0, 7, 1, 0})
	f.Add([]byte{0, 0, 0, 2, 0, 0, 3, 1, 0, 4, 1, 0, 5, 0, 0, 5, 0, 0, 6, 1, 0, 7, 1, 0})
	db := openDB(f)
	// Instrumented SQLite is slow; short programs reach the same states faster.
	f.Fuzz(func(t *testing.T, prog []byte) { runOwnershipProgram(t, db, prog, 30) })
}

// TB-typed fixtures: the fuzz driver needs them for both *testing.T and *testing.F.

func openDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "db", "antares.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := Migrate(context.Background(), db, Migrations()); err != nil {
		t.Fatal(err)
	}
	return db
}

func mkUserTB(t testing.TB, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES (?, 'iss', ?, ?, 'USER', 'APPROVED', 'READY', ?)`, id, "sub-"+id, id+"@example.test", ts(t0)); err != nil {
		t.Fatal(err)
	}
}

func mkBindingTB(t testing.TB, db *sql.DB, user, profile string) string {
	t.Helper()
	id := NewID()
	if _, err := db.Exec(`INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,
		credential_key_version,credential_fingerprint,memory_scope_key,status,verified_at,created_at)
		VALUES (?,?,?,'MUX_PREFIX','ep',?,1,?,?,'READY',?,?)`, id, user, profile, []byte("sealed-"+user), "fp-"+user, "mem-"+user, ts(t0), ts(t0)); err != nil {
		t.Fatal(err)
	}
	return id
}
