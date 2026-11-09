package store

import "testing"

// A run that is already RUNNING cannot be moved to RUNNING again (runSources), so MoveRun answers false and the ownership
// model must expect that. Fuzzing found it by chance (CI failed on an unrelated PR); this pins the exact sequence so the
// ordinary suite fails if the model and the lifecycle table ever disagree on it again.
func TestOwnershipRunningRunMovedToRunningAgainIsRefusedNotAFailure(t *testing.T) {
	db := openDB(t)
	// create a chat, admit a run on it, move it to RUNNING, ask for RUNNING again (selector 0 picks the first chat, then the first run).
	runOwnershipProgram(t, db, []byte{0, 0, 0, 2, 0, 0, 6, 0, 0, 6, 0, 0}, 4)
}
