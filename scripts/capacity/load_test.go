//go:build capacity

package main

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// Two workers failing with errors of different dynamic types used to panic
// (atomic.Value: "compare and swap of inconsistently typed value"). The driver
// must survive and report the first error, whichever type it has.
func TestParallelKeepsFirstErrorOfMixedTypes(t *testing.T) {
	plain := errors.New("plain")
	wrapped := fmt.Errorf("wrapped: %w", plain)
	for i := 0; i < 200; i++ {
		got := parallel(2, 2, func(i int) error {
			if i == 0 {
				return plain // *errors.errorString
			}
			return wrapped // *fmt.wrapError
		})
		if got != plain && got != wrapped {
			t.Fatalf("got %v, want one of the two errors", got)
		}
	}
	// Sequential (width 1) makes "first" deterministic.
	if got := parallel(2, 1, func(i int) error {
		if i == 0 {
			return plain
		}
		return wrapped
	}); got != plain {
		t.Fatalf("first error not kept: %v", got)
	}
	if got := parallel(3, 3, func(int) error { return nil }); got != nil {
		t.Fatalf("no failures, got %v", got)
	}
}

func TestWantOpen(t *testing.T) {
	var n atomic.Int64
	n.Add(3)
	if err := wantOpen(&n, 3); err != nil {
		t.Fatal(err)
	}
	if err := wantOpen(&n, 4); err == nil {
		t.Fatal("a missing stream was not reported")
	}
}
