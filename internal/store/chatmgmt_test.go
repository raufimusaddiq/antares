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
	if err := MarkDeleteFailed(ctx, db, u.b, u.chatA.ID); err != nil {
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
