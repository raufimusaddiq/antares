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
