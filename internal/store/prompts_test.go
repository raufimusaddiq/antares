package store

import (
	"context"
	"errors"
	"testing"
)

// Every prompt query takes the caller's id: B can neither list, claim, settle nor plant a prompt on A's
// run (security-invariants §4), and a claim sends once.
func TestPromptQueriesAreOwnerScoped(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	run, _, err := AdmitRun(ctx, db, u.a, u.chatA.ID, "idem-key-a1", t0)
	if err != nil {
		t.Fatal(err)
	}
	gen := int64(2)
	mk := Prompt{UserID: u.a, RunID: run.ID, Kind: PromptApproval, HermesID: "q1", Generation: &gen, Text: "rm x", Choices: []string{"once", "deny"}}
	p, created, err := InsertPrompt(ctx, db, mk, t0)
	if err != nil || !created {
		t.Fatalf("insert: %v %v", created, err)
	}
	if again, created, err := InsertPrompt(ctx, db, mk, t0); err != nil || created || again.ID != p.ID {
		t.Errorf("a replayed frame made a second prompt: %v %v %v", again.ID, created, err)
	}

	if _, _, err := InsertPrompt(ctx, db, Prompt{UserID: u.b, RunID: run.ID, Kind: PromptApproval, HermesID: "evil", Text: "x"}, t0); !errors.Is(err, ErrNoChat) {
		t.Errorf("B planted a prompt on A's run: %v", err)
	}
	if got, err := OpenPrompts(ctx, db, u.b, run.ID); err != nil || len(got) != 0 {
		t.Errorf("B listed A's prompts: %v %v", got, err)
	}
	if _, err := ClaimPrompt(ctx, db, u.b, run.ID, p.ID, PromptApproval); !errors.Is(err, ErrNoChat) {
		t.Errorf("B claimed A's prompt: %v", err)
	}
	if _, err := ClaimPrompt(ctx, db, u.a, run.ID, p.ID, PromptClarify); !errors.Is(err, ErrNoChat) {
		t.Errorf("a prompt was claimed as the wrong kind: %v", err)
	}
	if err := SettlePrompts(ctx, db, u.b, run.ID, t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := OpenPrompts(ctx, db, u.a, run.ID); len(got) != 1 {
		t.Fatalf("B settled A's prompt")
	}

	got, err := ClaimPrompt(ctx, db, u.a, run.ID, p.ID, PromptApproval)
	if err != nil || got.HermesID != "q1" || got.Generation == nil || *got.Generation != 2 {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if _, err := ClaimPrompt(ctx, db, u.a, run.ID, p.ID, PromptApproval); !errors.Is(err, ErrPromptNotPending) {
		t.Errorf("a second claim: %v", err)
	}
	// A restart while the answer was in flight reopens it.
	if _, _, err := RecoverRuns(ctx, db, t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := OpenPrompts(ctx, db, u.a, run.ID); len(got) != 1 {
		t.Fatalf("open after recovery: %v", got)
	}
	if _, err := ClaimPrompt(ctx, db, u.a, run.ID, p.ID, PromptApproval); err != nil {
		t.Fatal(err)
	}
	if err := FinishPrompt(ctx, db, u.b, p.ID, PromptAnswered, "once", t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := OpenPrompts(ctx, db, u.a, run.ID); len(got) != 1 {
		t.Error("B finished A's prompt")
	}
	if err := FinishPrompt(ctx, db, u.a, p.ID, PromptAnswered, "once", t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := OpenPrompts(ctx, db, u.a, run.ID); len(got) != 0 {
		t.Error("an answered prompt is still open")
	}
}
