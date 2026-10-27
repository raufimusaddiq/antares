package chat

import (
	"testing"
	"time"
)

// #150: a sign-out marks exactly the matching open streams; sign out everywhere marks every stream of the user and
// none of anyone else's.
func TestSignOutMarksOnlyMatchingStreams(t *testing.T) {
	s := &Service{}
	a1, done1 := s.registerStream("alice", "hash-a1")
	defer done1()
	a2, done2 := s.registerStream("alice", "hash-a2")
	defer done2()
	b1, done3 := s.registerStream("bob", "hash-b1")
	defer done3()
	s.SessionsRevoked("alice", "hash-a1")
	if !a1.revoked || a2.revoked || b1.revoked {
		t.Fatalf("single sign-out: a1=%v a2=%v b1=%v", a1.revoked, a2.revoked, b1.revoked)
	}
	s.SessionsRevoked("alice", "")
	if !a2.revoked || b1.revoked {
		t.Fatalf("sign out everywhere: a2=%v b1=%v", a2.revoked, b1.revoked)
	}
	done1()
	if _, still := s.streams.open[a1]; still {
		t.Error("a closed stream stays registered")
	}
}

// Review #186: a sign-out that arrives while a batch is being written waits for that batch, so once it returns no
// further batch can start; a stream that never lets go holds it at most revokeWait, and is still marked afterwards.
func TestSignOutWaitsForTheBatchInFlight(t *testing.T) {
	s := &Service{}
	st, done := s.registerStream("alice", "hash-a1")
	defer done()
	st.mu.Lock() // a batch is being written
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		st.mu.Unlock() // the batch is done
		close(released)
	}()
	began := time.Now()
	s.SessionsRevoked("alice", "")
	<-released
	if took := time.Since(began); took < 100*time.Millisecond || took > time.Second {
		t.Errorf("the sign-out did not wait for the batch in flight: %v", took)
	}
	st.mu.Lock()
	if !st.revoked {
		t.Error("the stream is not marked after the sign-out returned")
	}
	st.mu.Unlock()

	stuck, done2 := s.registerStream("alice", "hash-a2")
	defer done2()
	stuck.mu.Lock() // a write that never finishes
	began = time.Now()
	s.SessionsRevoked("alice", "")
	if took := time.Since(began); took > revokeWait+time.Second {
		t.Errorf("a stuck stream held the sign-out for %v", took)
	}
	stuck.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	stuck.mu.Lock()
	if !stuck.revoked {
		t.Error("the stuck stream was not marked once it let go")
	}
	stuck.mu.Unlock()
}
