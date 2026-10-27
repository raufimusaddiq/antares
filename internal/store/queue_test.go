package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// Negative IDOR (security-invariants §4): B naming A's chat or A's queued message changes nothing and
// is answered exactly like an id that does not exist.
func TestQueueIsOwnerScoped(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	it, err := Enqueue(ctx, db, u.a, u.chatA.ID, "A's message", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetQueue(ctx, db, u.b, u.chatA.ID); !errors.Is(err, ErrNoChat) {
		t.Errorf("B reads A's queue: %v", err)
	}
	if _, err := Enqueue(ctx, db, u.b, u.chatA.ID, "B's message", t0); !errors.Is(err, ErrNoChat) {
		t.Errorf("B enqueues into A's chat: %v", err)
	}
	if err := EditQueued(ctx, db, u.b, u.chatA.ID, it.ID, "hijacked", t0); !errors.Is(err, ErrNoItem) {
		t.Errorf("B edits A's message: %v", err)
	}
	if err := RemoveQueued(ctx, db, u.b, u.chatA.ID, it.ID); !errors.Is(err, ErrNoItem) {
		t.Errorf("B removes A's message: %v", err)
	}
	if err := SetQueuePaused(ctx, db, u.b, u.chatA.ID, "USER"); !errors.Is(err, ErrNoChat) {
		t.Errorf("B pauses A's queue: %v", err)
	}
	if _, err := ClaimNext(ctx, db, u.b, u.chatA.ID, true, t0); !errors.Is(err, ErrNoChat) {
		t.Errorf("B claims from A's queue: %v", err)
	}
	q, err := GetQueue(ctx, db, u.a, u.chatA.ID)
	if err != nil || len(q.Items) != 1 || q.Items[0].Input != "A's message" || q.Items[0].Status != "QUEUED" || q.Paused != "" {
		t.Errorf("A's queue after B's attempts: %+v %v", q, err)
	}
}

func TestQueueOrderEditRemoveAndLimit(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	var ids []string
	for _, s := range []string{"one", "two", "three"} {
		it, err := Enqueue(ctx, db, u.a, u.chatA.ID, s, t0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
	}
	if err := EditQueued(ctx, db, u.a, u.chatA.ID, ids[1], "two, edited", t0); err != nil {
		t.Fatal(err)
	}
	if err := RemoveQueued(ctx, db, u.a, u.chatA.ID, ids[0]); err != nil {
		t.Fatal(err)
	}
	q, _ := GetQueue(ctx, db, u.a, u.chatA.ID)
	if len(q.Items) != 2 || q.Items[0].Input != "two, edited" || q.Items[0].Ordinal != 1 || q.Items[1].Input != "three" || q.Items[1].Ordinal != 2 {
		t.Errorf("queue = %+v", q.Items)
	}
	for i := len(q.Items); i < MaxQueuedPerChat; i++ {
		if _, err := Enqueue(ctx, db, u.a, u.chatA.ID, "more", t0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Enqueue(ctx, db, u.a, u.chatA.ID, "one too many", t0); !errors.Is(err, ErrQueueFull) {
		t.Errorf("past the limit: %v", err)
	}
}

// QUEUE-003: of many concurrent claimers exactly one moves the head to SUBMITTING; while it is in
// flight nothing else is claimed, it cannot be edited or removed, and a paused queue gives nothing to
// an automatic claim.
func TestClaimNextIsExactlyOnce(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	first, _ := Enqueue(ctx, db, u.a, u.chatA.ID, "first", t0)
	if _, err := Enqueue(ctx, db, u.a, u.chatA.ID, "second", t0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won []QueueItem
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if it, err := ClaimNext(ctx, db, u.a, u.chatA.ID, false, t0); err == nil {
				mu.Lock()
				won = append(won, it)
				mu.Unlock()
			} else if !errors.Is(err, ErrQueueBusy) {
				t.Errorf("claim: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(won) != 1 || won[0].ID != first.ID || won[0].IdempotencyKey() != "queue-"+first.ID {
		t.Fatalf("claims = %+v", won)
	}
	if err := EditQueued(ctx, db, u.a, u.chatA.ID, first.ID, "changed", t0); !errors.Is(err, ErrNoItem) {
		t.Errorf("edit while submitting: %v", err)
	}
	if err := RemoveQueued(ctx, db, u.a, u.chatA.ID, first.ID); !errors.Is(err, ErrNoItem) {
		t.Errorf("remove while submitting: %v", err)
	}
	// Released (no run admitted): back at the head, claimable again.
	if err := SettleClaim(ctx, db, u.a, first.ID, false, t0); err != nil {
		t.Fatal(err)
	}
	if err := SetQueuePaused(ctx, db, u.a, u.chatA.ID, "USER"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimNext(ctx, db, u.a, u.chatA.ID, false, t0); !errors.Is(err, ErrNoItem) {
		t.Errorf("automatic claim from a paused queue: %v", err)
	}
	it, err := ClaimNext(ctx, db, u.a, u.chatA.ID, true, t0)
	if err != nil || it.ID != first.ID {
		t.Fatalf("explicit claim from a paused queue: %+v %v", it, err)
	}
	// Sent: it leaves the queue, and settling it twice does not succeed twice.
	if err := SettleClaim(ctx, db, u.a, first.ID, true, t0); err != nil {
		t.Fatal(err)
	}
	if err := SettleClaim(ctx, db, u.a, first.ID, true, t0); !errors.Is(err, ErrNoItem) {
		t.Errorf("second settle: %v", err)
	}
	q, _ := GetQueue(ctx, db, u.a, u.chatA.ID)
	if len(q.Items) != 1 || q.Items[0].Input != "second" || q.Paused != "USER" {
		t.Errorf("queue = %+v", q)
	}
}

// Only a queue with messages waiting is paused, and the first reason stays.
func TestPauseQueueIfWaiting(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	if err := PauseQueueIfWaiting(ctx, db, u.a, u.chatA.ID, RunFailed); err != nil {
		t.Fatal(err)
	}
	if q, _ := GetQueue(ctx, db, u.a, u.chatA.ID); q.Paused != "" {
		t.Errorf("an empty queue was paused: %q", q.Paused)
	}
	if _, err := Enqueue(ctx, db, u.a, u.chatA.ID, "waiting", t0); err != nil {
		t.Fatal(err)
	}
	_ = PauseQueueIfWaiting(ctx, db, u.a, u.chatA.ID, RunFailed)
	_ = PauseQueueIfWaiting(ctx, db, u.a, u.chatA.ID, RunCancelled)
	if q, _ := GetQueue(ctx, db, u.a, u.chatA.ID); q.Paused != RunFailed {
		t.Errorf("paused = %q, want the first reason", q.Paused)
	}
	_ = PauseQueueIfWaiting(ctx, db, u.b, u.chatA.ID, "X")
	if q, _ := GetQueue(ctx, db, u.a, u.chatA.ID); q.Paused != RunFailed {
		t.Errorf("B changed A's pause: %q", q.Paused)
	}
}

// A claim the process died holding is queued again and its chat's queue paused for the owner.
func TestRecoverRunsReleasesInterruptedClaims(t *testing.T) {
	d, u := setupTwo(t)
	db := d.DB
	ctx := context.Background()
	if _, err := Enqueue(ctx, db, u.a, u.chatA.ID, "in flight", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimNext(ctx, db, u.a, u.chatA.ID, false, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecoverRuns(ctx, db, t0); err != nil {
		t.Fatal(err)
	}
	q, _ := GetQueue(ctx, db, u.a, u.chatA.ID)
	if len(q.Items) != 1 || q.Items[0].Status != "QUEUED" || q.Paused != "INTERRUPTED" {
		t.Errorf("after recovery: %+v", q)
	}
}
