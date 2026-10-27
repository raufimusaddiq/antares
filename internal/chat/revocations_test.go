package chat

import (
	"testing"
	"time"
)

// #150: the in-memory sign-out record names one session or every session of a user, only from the moment it is made.
func TestRevocationRecord(t *testing.T) {
	s := &Service{}
	before := time.Now().Add(-time.Minute)
	s.SessionsRevoked("alice", "hash-a1")
	if !s.revokedSince("alice", "hash-a1", before) {
		t.Error("the signed-out session is not revoked")
	}
	if s.revokedSince("alice", "hash-a2", before) || s.revokedSince("bob", "hash-b1", before) {
		t.Error("a single sign-out reached another session or user")
	}
	s.SessionsRevoked("alice", "")
	if !s.revokedSince("alice", "hash-a2", before) || s.revokedSince("bob", "hash-b1", before) {
		t.Error("sign out everywhere must reach every session of alice and none of bob's")
	}
	if s.revokedSince("alice", "hash-a3", time.Now().Add(time.Minute)) {
		t.Error("a stream opened after the sign-out (a new session) must not be cut by it")
	}
	if s.revokedSince("carol", "", before) {
		t.Error("no session hash must never match a token entry")
	}
}
