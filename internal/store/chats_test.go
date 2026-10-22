package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

type twoUsers struct {
	a, b  string
	bindA string
	bindB string
	chatA Chat
	chatB Chat
}

func setupTwo(t *testing.T) (*twoUsersDB, twoUsers) {
	t.Helper()
	db, _ := migrated(t)
	a := mkUser(t, db, "ua", "USER", "APPROVED", "READY")
	b := mkUser(t, db, "ub", "USER", "APPROVED", "READY")
	ba := mkBinding(t, db, a, "u_aaaaaaaaaa", "READY")
	bb := mkBinding(t, db, b, "u_bbbbbbbbbb", "READY")
	ctx := context.Background()
	ca, err := CreateChat(ctx, db, a, ba, "sess-a", "A's chat", t0)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := CreateChat(ctx, db, b, bb, "sess-b", "B's chat", t0)
	if err != nil {
		t.Fatal(err)
	}
	return &twoUsersDB{DB: db}, twoUsers{a: a, b: b, bindA: ba, bindB: bb, chatA: ca, chatB: cb}
}

type twoUsersDB struct{ DB *sql.DB }

// Negative IDOR tests (security-invariants §4): every read and write of a chat
// or run takes the caller's id in the query itself.
func TestChatAndRunQueriesAreOwnerScoped(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()

	run, created, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-a1", t0)
	if err != nil || !created {
		t.Fatalf("admit: %v %v", created, err)
	}

	if _, err := GetChat(ctx, db, u.b, u.chatA.ID); !errors.Is(err, ErrNoChat) {
		t.Errorf("B read A's chat: %v", err)
	}
	if _, err := GetRun(ctx, db, u.b, run.ID); !errors.Is(err, ErrNoChat) {
		t.Errorf("B read A's run: %v", err)
	}
	if _, _, err := AdmitRun(ctx, db, u.b, u.chatA.ID, "idem-key-b1", t0); !errors.Is(err, ErrNoChat) {
		t.Errorf("B admitted a run on A's chat: %v", err)
	}
	if moved, err := MoveRun(ctx, db, u.b, run.ID, []string{RunPending}, RunCancelled, "", "", t0); err != nil || moved {
		t.Errorf("B moved A's run: moved=%v err=%v", moved, err)
	}
	if got, _ := GetRun(ctx, db, u.a, run.ID); got.Status != RunPending {
		t.Errorf("A's run changed to %s", got.Status)
	}
	if list, err := ListChats(ctx, db, u.b, 50, 0); err != nil || len(list) != 1 || list[0].ID != u.chatB.ID {
		t.Errorf("B's list: %+v %v", list, err)
	}
	// A foreign chat id also cannot be created on someone else's binding.
	if _, err := CreateChat(ctx, db, u.b, u.bindA, "sess-x", "", t0); err == nil {
		t.Error("a chat was created for B on A's binding")
	}
	// The same idempotency key is per user: B using A's key is a fresh admission, not A's run.
	rb, created, err := AdmitRun(ctx, db, u.b, u.chatB.ID, "idem-key-a1", t0)
	if err != nil || !created || rb.ID == run.ID || rb.UserID != u.b {
		t.Errorf("B with A's key: %+v created=%v %v", rb, created, err)
	}
}

func TestOneActiveRunPerChatAndIdempotentReplay(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	r1, created, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-1", t0)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	// Same key: the same run, even while it is active; nothing new is inserted.
	again, created, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-1", t0)
	if err != nil || created || again.ID != r1.ID {
		t.Errorf("replay: %+v created=%v %v", again, created, err)
	}
	// Different key while active: conflict, decided by the database.
	if _, _, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-2", t0); !errors.Is(err, ErrRunActive) {
		t.Errorf("second active run: %v", err)
	}
	// A key already used for another chat is not a replay.
	otherChat, err := CreateChat(ctx, db, u.a, u.bindA, "sess-a2", "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AdmitRun(ctx, db, u.a, otherChat.ID, "idem-key-1", t0); !errors.Is(err, ErrRunActive) {
		t.Errorf("key reuse across chats: %v", err)
	}
	// DISCONNECTED still occupies the chat; a terminal run frees it.
	if ok, _ := MoveRun(ctx, db, u.a, r1.ID, []string{RunPending}, RunDisconnected, "", "", t0); !ok {
		t.Fatal("could not disconnect")
	}
	if _, _, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-3", t0); !errors.Is(err, ErrRunActive) {
		t.Errorf("disconnected run did not hold the chat: %v", err)
	}
	if ok, _ := MoveRun(ctx, db, u.a, r1.ID, []string{RunDisconnected}, RunFailed, "", "HERMES_UNAVAILABLE", t0); !ok {
		t.Fatal("could not fail")
	}
	if _, created, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-3", t0); err != nil || !created {
		t.Errorf("chat not freed by a terminal run: %v %v", created, err)
	}
}

func TestConcurrentSendsAdmitExactlyOne(t *testing.T) {
	d, u := setupTwo(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, created, err := AdmitRun(context.Background(), d.DB, u.a, u.chatA.ID, "idem-key-race-"+string(rune('a'+i)), t0)
			if err == nil && created {
				mu.Lock()
				admitted++
				mu.Unlock()
			} else if err != nil && !errors.Is(err, ErrRunActive) {
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if admitted != 1 {
		t.Errorf("%d runs admitted on one chat", admitted)
	}
}

// A terminal run never changes: a late upstream "completed" cannot revive a
// cancelled run (security-invariants §8).
func TestTerminalRunsAreImmutable(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	for _, final := range []string{RunCancelled, RunFailed, RunCompleted, RunTimedOut} {
		run, _, err := AdmitRun(ctx, d.DB, u.a, u.chatA.ID, "idem-final-"+final, t0)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := MoveRun(ctx, d.DB, u.a, run.ID, []string{RunPending, RunRunning}, final, "", "", t0); !ok || err != nil {
			t.Fatal(ok, err)
		}
		for _, to := range []string{RunCompleted, RunRunning, RunFailed, RunCancelled, RunDisconnected} {
			if ok, _ := MoveRun(ctx, d.DB, u.a, run.ID, []string{RunPending, RunRunning, RunDisconnected}, to, "", "", t0); ok {
				t.Errorf("%s run moved to %s", final, to)
			}
		}
		if got, _ := GetRun(ctx, d.DB, u.a, run.ID); got.Status != final {
			t.Errorf("%s run reads back as %s", final, got.Status)
		}
	}
}

func TestRecoverRunsNeverReportsCompletionOrReplays(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	running, _, _ := AdmitRun(ctx, d.DB, u.a, u.chatA.ID, "idem-run-1", t0)
	MoveRun(ctx, d.DB, u.a, running.ID, []string{RunPending}, RunRunning, "hrun-1", "", t0)
	pending, _, _ := AdmitRun(ctx, d.DB, u.b, u.chatB.ID, "idem-run-2", t0)
	disc, failed, err := RecoverRuns(ctx, d.DB, t0.Add(time.Second))
	if err != nil || disc != 1 || failed != 1 {
		t.Fatalf("recover: %d %d %v", disc, failed, err)
	}
	if r, _ := GetRun(ctx, d.DB, u.a, running.ID); r.Status != RunDisconnected || r.HermesRunID != "hrun-1" {
		t.Errorf("running -> %+v", r)
	}
	if r, _ := GetRun(ctx, d.DB, u.b, pending.ID); r.Status != RunFailed || r.ErrorCode != "HERMES_UNAVAILABLE" {
		t.Errorf("pending -> %+v", r)
	}
}

// The database half of the default-deny gate: every "no" of
// security-invariants §1 that lives in the database.
func TestActiveBindingIsDefaultDeny(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	verify := func(binding string) {
		if _, err := db.Exec(`UPDATE tenant_bindings SET verified_at = ? WHERE id = ?`, ts(t0), binding); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name             string
		approval, tenant string
		bindStatus       string
		verified         bool
		wantOK           bool
	}{
		{"approved ready verified", "APPROVED", "READY", "READY", true, true},
		{"pending", "PENDING", "READY", "READY", true, false},
		{"rejected", "REJECTED", "READY", "READY", true, false},
		{"suspended", "SUSPENDED", "READY", "READY", true, false},
		{"tenant configuring", "APPROVED", "CONFIGURING", "READY", true, false},
		{"tenant failed", "APPROVED", "FAILED", "READY", true, false},
		{"tenant disabled", "APPROVED", "DISABLED", "READY", true, false},
		{"binding failed", "APPROVED", "READY", "FAILED", true, false},
		{"binding disabled", "APPROVED", "READY", "DISABLED", true, false},
		{"binding configuring", "APPROVED", "READY", "CONFIGURING", true, false},
		{"binding never verified", "APPROVED", "READY", "READY", false, false},
	}
	for i, tc := range cases {
		id := "g" + string(rune('a'+i))
		mkUser(t, db, id, "USER", tc.approval, tc.tenant)
		b := mkBinding(t, db, id, "u_gate"+id+"xxxx", tc.bindStatus)
		if tc.verified {
			verify(b)
		}
		got, err := ActiveBinding(ctx, db, id)
		if tc.wantOK != (err == nil) {
			t.Errorf("%s: err=%v", tc.name, err)
		}
		if !tc.wantOK && (!errors.Is(err, ErrGate) || len(got.Ciphertext) != 0) {
			t.Errorf("%s: denied but returned %v %v", tc.name, err, got)
		}
	}
	mkUser(t, db, "nobind", "USER", "APPROVED", "READY")
	if _, err := ActiveBinding(ctx, db, "nobind"); !errors.Is(err, ErrGate) {
		t.Errorf("no binding row: %v", err)
	}
	if _, err := ActiveBinding(ctx, db, "nobody"); !errors.Is(err, ErrGate) {
		t.Errorf("no such user: %v", err)
	}
}
