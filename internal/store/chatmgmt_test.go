package store

import (
	"context"
	"errors"
	"testing"
)

// Negative IDOR (security-invariants §4) for every chat-management statement: B names A's chat id
// and nothing changes, answered exactly like an id that does not exist.
func TestChatManagementIsOwnerScoped(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	missing := NewID()

	for name, fn := range map[string]func(owner, chat string) error{
		"rename":    func(o, c string) error { return RenameChat(ctx, db, o, c, "hijacked") },
		"pin":       func(o, c string) error { return SetChatPinned(ctx, db, o, c, true, t0) },
		"unpin":     func(o, c string) error { return SetChatPinned(ctx, db, o, c, false, t0) },
		"archive":   func(o, c string) error { return SetChatArchived(ctx, db, o, c, true) },
		"unarchive": func(o, c string) error { return SetChatArchived(ctx, db, o, c, false) },
		"begin delete": func(o, c string) error {
			_, err := BeginDeleteChat(ctx, db, o, c, t0)
			return err
		},
		"finish delete": func(o, c string) error {
			_, err := FinishDeleteChat(ctx, db, o, c)
			return err
		},
	} {
		if err := fn(u.b, u.chatA.ID); !errors.Is(err, ErrNoChat) {
			t.Errorf("B %s on A's chat: %v", name, err)
		}
		if err := fn(u.b, missing); !errors.Is(err, ErrNoChat) {
			t.Errorf("B %s on a missing chat: %v", name, err)
		}
	}
	got, err := GetChat(ctx, db, u.a, u.chatA.ID)
	if err != nil || got.Title != "A's chat" || got.PinnedAt != "" || got.Status != "ACTIVE" {
		t.Errorf("A's chat after B's attempts: %+v %v", got, err)
	}
	// MarkDeleteFailed is a no-op for a foreign chat as well.
	if _, err := BeginDeleteChat(ctx, db, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	if err := MarkDeleteFailed(ctx, db, u.b, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := db.QueryRow(`SELECT status FROM chats WHERE id = ?`, u.chatA.ID).Scan(&st); err != nil || st != "PENDING_DELETE" {
		t.Errorf("B's MarkDeleteFailed changed A's chat to %q (%v)", st, err)
	}
	// B's chat and B's list are untouched by everything above.
	if list, _, err := ListChats(ctx, db, u.b, 50, nil, false); err != nil || len(list) != 1 || list[0].ID != u.chatB.ID {
		t.Errorf("B's list = %v %v", list, err)
	}
}

// A delete is refused under a running turn and counts only the owner's runs.
func TestBeginDeleteChatWaitsForTheRun(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	run, _, err := AdmitRun(ctx, d.DB, u.a, u.chatA.ID, "idem-key-del1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); !errors.Is(err, ErrRunActive) {
		t.Fatalf("delete under a pending run = %v", err)
	}
	if got, _ := GetChat(ctx, d.DB, u.a, u.chatA.ID); got.Status != "ACTIVE" {
		t.Errorf("a refused delete moved the chat to %s", got.Status)
	}
	if _, err := MoveRun(ctx, d.DB, u.a, run.ID, []string{RunPending}, RunCompleted, "", "", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatalf("delete after the run = %v", err)
	}
}

// A restart in the middle of a delete returns the chat to the status it had, flagged.
func TestRecoverRestoresArchivedChatAfterInterruptedDelete(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	if err := SetChatArchived(ctx, d.DB, u.a, u.chatA.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecoverRuns(ctx, d.DB, t0); err != nil {
		t.Fatal(err)
	}
	got, err := GetChat(ctx, d.DB, u.a, u.chatA.ID)
	if err != nil || got.Status != "ARCHIVED" || !got.DeleteFailed {
		t.Errorf("after recovery: %+v %v", got, err)
	}
	// A claim that is then abandoned by an error path restores the same way.
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	if err := MarkDeleteFailed(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetChat(ctx, d.DB, u.a, u.chatA.ID); got.Status != "ARCHIVED" {
		t.Errorf("MarkDeleteFailed left status %s", got.Status)
	}
}

// Only one request can hold the claim; a failure of the holder is the only way back.
func TestBeginDeleteChatIsExclusive(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	if err := SetChatArchived(ctx, d.DB, u.a, u.chatA.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); !errors.Is(err, ErrDeleteInFlight) {
		t.Fatalf("second claim = %v, want ErrDeleteInFlight", err)
	}
	if busy, err := DeleteInFlight(ctx, d.DB, u.a, u.chatA.ID); err != nil || !busy {
		t.Errorf("owner sees in-flight = %v %v", busy, err)
	}
	if busy, _ := DeleteInFlight(ctx, d.DB, u.b, u.chatA.ID); busy {
		t.Error("another account can see that the chat is being deleted")
	}
	if err := MarkDeleteFailed(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetChat(ctx, d.DB, u.a, u.chatA.ID); got.Status != "ARCHIVED" || !got.DeleteFailed {
		t.Errorf("after the holder failed: %+v", got)
	}
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Errorf("claim after settlement = %v", err)
	}
}

// A chat being deleted answers every change as "in flight", never as "no such chat"; only its owner learns that.
func TestChangesToAChatBeingDeletedAreInFlightNotMissing(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	if _, err := BeginDeleteChat(ctx, d.DB, u.a, u.chatA.ID, t0); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func(owner string) error{
		"rename":    func(o string) error { return RenameChat(ctx, d.DB, o, u.chatA.ID, "x") },
		"pin":       func(o string) error { return SetChatPinned(ctx, d.DB, o, u.chatA.ID, true, t0) },
		"unpin":     func(o string) error { return SetChatPinned(ctx, d.DB, o, u.chatA.ID, false, t0) },
		"archive":   func(o string) error { return SetChatArchived(ctx, d.DB, o, u.chatA.ID, true) },
		"unarchive": func(o string) error { return SetChatArchived(ctx, d.DB, o, u.chatA.ID, false) },
	} {
		if err := fn(u.a); !errors.Is(err, ErrDeleteInFlight) {
			t.Errorf("owner %s = %v, want ErrDeleteInFlight", name, err)
		}
		if err := fn(u.b); !errors.Is(err, ErrNoChat) {
			t.Errorf("another account's %s = %v, want ErrNoChat", name, err)
		}
	}
}

// deleted_chat_usage keeps what deleted chats reported, so deleting a chat does not reset the budget, and keeps
// nothing when nothing was reported, so "usage unknown" stays unknown.
func TestDeletedChatUsageLedger(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	report := func(user, chat, key string, in, out *int64) {
		t.Helper()
		run, _, err := AdmitRun(ctx, d.DB, user, chat, key, t0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.DB.Exec(`UPDATE runs SET input_tokens = ?, output_tokens = ?, status = 'COMPLETED' WHERE id = ?`, in, out, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	del := func(user, chat string) {
		t.Helper()
		if _, err := BeginDeleteChat(ctx, d.DB, user, chat, t0); err != nil {
			t.Fatal(err)
		}
		if _, err := FinishDeleteChat(ctx, d.DB, user, chat); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(user string) int {
		var n int
		if err := d.DB.QueryRow(`SELECT COUNT(*) FROM deleted_chat_usage WHERE user_id = ?`, user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	i64 := func(v int64) *int64 { return &v }

	// A chat whose runs reported nothing leaves no trace: usage stays unknown.
	report(u.a, u.chatA.ID, "idem-ledger-0", nil, nil)
	del(u.a, u.chatA.ID)
	if used, known, err := UserReportedTokens(ctx, d.DB, u.a); err != nil || known || used != 0 || rows(u.a) != 0 {
		t.Fatalf("after deleting a chat that reported nothing: used %d known %v rows %d (%v)", used, known, rows(u.a), err)
	}

	// Chats that reported tokens add up across deletes, per user, and are known.
	c2, err := CreateChat(ctx, d.DB, u.a, u.bindA, "sess-a2", "second", t0)
	if err != nil {
		t.Fatal(err)
	}
	c3, err := CreateChat(ctx, d.DB, u.a, u.bindA, "sess-a3", "third", t0)
	if err != nil {
		t.Fatal(err)
	}
	report(u.a, c2.ID, "idem-ledger-1", i64(100), i64(20))
	report(u.a, c3.ID, "idem-ledger-2", i64(7), i64(3))
	report(u.b, u.chatB.ID, "idem-ledger-3", i64(5), i64(5))
	before, _, _ := UserReportedTokens(ctx, d.DB, u.a)
	if before != 130 {
		t.Fatalf("total before any delete = %d, want 130", before)
	}
	del(u.a, c2.ID) // its 120 move from live runs to the ledger; the total does not move
	if used, known, _ := UserReportedTokens(ctx, d.DB, u.a); !known || used != 130 {
		t.Errorf("after one delete: used %d known %v, want 130 true", used, known)
	}
	del(u.a, c3.ID)
	if used, known, _ := UserReportedTokens(ctx, d.DB, u.a); !known || used != 130 || rows(u.a) != 1 {
		t.Errorf("after two deletes: used %d known %v ledger rows %d, want 130 true 1", used, known, rows(u.a))
	}
	if used, known, _ := UserReportedTokens(ctx, d.DB, u.b); !known || used != 10 {
		t.Errorf("the other user's total changed: %d %v", used, known)
	}
	// The budget still counts them: a new run is refused at the limit the deleted chats reached.
	c4, _ := CreateChat(ctx, d.DB, u.a, u.bindA, "sess-a4", "fourth", t0)
	if _, _, err := AdmitRunWithin(ctx, d.DB, u.a, c4.ID, "idem-ledger-4", t0, 130); !errors.Is(err, ErrBudget) {
		t.Errorf("budget after deletes = %v, want ErrBudget", err)
	}
	if _, _, err := AdmitRunWithin(ctx, d.DB, u.a, c4.ID, "idem-ledger-5", t0, 131); err != nil {
		t.Errorf("under the limit = %v", err)
	}
}
