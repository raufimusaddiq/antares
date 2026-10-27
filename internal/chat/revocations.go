package chat

import (
	"sync"
	"time"
)

// Sign-outs, held in memory so an open run stream can check before every batch of events without reading the
// database (#150). auth calls SessionsRevoked after it has revoked in the database; the stream's heartbeat still reads
// the database, which covers revocations made elsewhere (an admin suspension, a newer login on the same browser).
type revocations struct {
	mu     sync.Mutex
	users  map[string]time.Time // every session of the user, revoked at
	tokens map[string]time.Time // one session (its stored hash), revoked at
}

// keepRevocations bounds the record: a stream older than this has met its heartbeat's database check many times.
const keepRevocations = time.Hour

// SessionsRevoked records that tokenHash's session, or with tokenHash "" every session of userID, has ended.
func (s *Service) SessionsRevoked(userID, tokenHash string) {
	r := &s.revoked
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.users == nil {
		r.users, r.tokens = map[string]time.Time{}, map[string]time.Time{}
	}
	for k, t := range r.users {
		if now.Sub(t) > keepRevocations {
			delete(r.users, k)
		}
	}
	for k, t := range r.tokens {
		if now.Sub(t) > keepRevocations {
			delete(r.tokens, k)
		}
	}
	if tokenHash == "" {
		r.users[userID] = now
	} else {
		r.tokens[tokenHash] = now
	}
}

// revokedSince reports a sign-out of this stream's session, or of all of its user's sessions, after since.
func (s *Service) revokedSince(userID, tokenHash string, since time.Time) bool {
	r := &s.revoked
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.users[userID]; ok && !t.Before(since) {
		return true
	}
	t, ok := r.tokens[tokenHash]
	return ok && tokenHash != "" && !t.Before(since)
}
