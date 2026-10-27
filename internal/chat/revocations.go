package chat

import (
	"sync"
	"time"
)

// Open run streams and the sign-outs they must obey at once (#150). A stream registers itself with its user and
// session before it re-checks that session in the database, and writes every batch of events while holding its own
// lock. A sign-out (auth calls SessionsRevoked after revoking in the database) marks each matching stream under that
// lock. So a sign-out that happened before registration is seen by the re-check, one after it is seen by the mark, and
// once SessionsRevoked returns no stream can begin another batch for a signed-out session.
//
// The limit: a batch already being written when the sign-out arrives finishes first; SessionsRevoked waits for it, but
// at most revokeWait per stream, so a client that stopped reading cannot hold a sign-out open. Such a stream is still
// marked and stops before its next batch.
type openStream struct {
	userID, session string
	mu              sync.Mutex
	revoked         bool
	wake            chan struct{} // closed by the sign-out, so an idle stream does not wait for its heartbeat
	once            sync.Once
}

const revokeWait = 2 * time.Second

type streamSet struct {
	mu   sync.Mutex
	open map[*openStream]struct{}
}

func (s *Service) registerStream(userID, session string) (*openStream, func()) {
	st := &openStream{userID: userID, session: session, wake: make(chan struct{})}
	s.streams.mu.Lock()
	if s.streams.open == nil {
		s.streams.open = map[*openStream]struct{}{}
	}
	s.streams.open[st] = struct{}{}
	s.streams.mu.Unlock()
	return st, func() {
		s.streams.mu.Lock()
		delete(s.streams.open, st)
		s.streams.mu.Unlock()
	}
}

// SessionsRevoked marks every open stream of tokenHash's session, or with tokenHash "" of every session of userID,
// and returns once each has finished the batch it was writing (or revokeWait passed).
func (s *Service) SessionsRevoked(userID, tokenHash string) {
	s.streams.mu.Lock()
	var hit []*openStream
	for st := range s.streams.open {
		if st.userID == userID && (tokenHash == "" || st.session == tokenHash) {
			hit = append(hit, st)
		}
	}
	s.streams.mu.Unlock()
	var wg sync.WaitGroup
	for _, st := range hit {
		marked := make(chan struct{})
		go func() {
			st.mu.Lock()
			st.revoked = true
			st.mu.Unlock()
			st.once.Do(func() { close(st.wake) })
			close(marked)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-marked:
			case <-time.After(revokeWait):
			}
		}()
	}
	wg.Wait()
}
