// Package obs is the app's observability (PRD §13.2, issue #114): the per-request fields an access log line carries,
// and a few in-memory counters for what the database cannot answer (stream disconnects, denials, upstream latency).
// It records identifiers and numbers only, never a prompt, a message body, a token or a key; and every log line also
// passes through internal/redact.
package obs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Request holds what the layers below the access log learn about a request: who it was (the internal, opaque user id)
// and the error category of a refusal. The access log middleware creates it; the auth guard and apiError fill it in.
type Request struct {
	mu       sync.Mutex
	ID       string
	userID   string
	category string
}

type ctxKey struct{}

// With returns ctx carrying r.
func With(ctx context.Context, r *Request) context.Context {
	return context.WithValue(ctx, ctxKey{}, r)
}

// From returns the request's record, or nil outside a logged request.
func From(ctx context.Context) *Request {
	r, _ := ctx.Value(ctxKey{}).(*Request)
	return r
}

// SetUser records the authenticated user's internal id.
func (r *Request) SetUser(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.userID = id
	r.mu.Unlock()
}

// SetCategory records the API error code a refusal answered with.
func (r *Request) SetCategory(code string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.category == "" {
		r.category = code
	}
	r.mu.Unlock()
}

// Fields returns the user id and error category recorded so far.
func (r *Request) Fields() (userID, category string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userID, r.category
}

// Counters that only the running process knows. Read with Snapshot.
var (
	streamsOpened       atomic.Int64
	streamsDisconnected atomic.Int64 // ended by the server as run.disconnected (lost upstream, suspension, sign-out)
	streamsResumed      atomic.Int64 // opened with Last-Event-ID
	denials             atomic.Int64 // 401 and 403 answers
	upstreamCalls       atomic.Int64
	upstreamMillis      atomic.Int64
	upstreamMaxMillis   atomic.Int64
)

func StreamOpened(resumed bool) {
	streamsOpened.Add(1)
	if resumed {
		streamsResumed.Add(1)
	}
}
func StreamDisconnected() { streamsDisconnected.Add(1) }
func Denied()             { denials.Add(1) }

// Upstream records one Hermes call's latency.
func Upstream(d time.Duration) {
	ms := d.Milliseconds()
	upstreamCalls.Add(1)
	upstreamMillis.Add(ms)
	for {
		cur := upstreamMaxMillis.Load()
		if ms <= cur || upstreamMaxMillis.CompareAndSwap(cur, ms) {
			return
		}
	}
}

// Counts is a snapshot of the in-memory counters since the process started.
type Counts struct {
	StreamsOpened       int64   `json:"streamsOpened"`
	StreamsResumed      int64   `json:"streamsResumed"`
	StreamsDisconnected int64   `json:"streamsDisconnected"`
	Denials             int64   `json:"denials"`
	UpstreamCalls       int64   `json:"upstreamCalls"`
	UpstreamMeanMillis  float64 `json:"upstreamMeanMillis"`
	UpstreamMaxMillis   int64   `json:"upstreamMaxMillis"`
}

func Snapshot() Counts {
	c := Counts{
		StreamsOpened: streamsOpened.Load(), StreamsResumed: streamsResumed.Load(), StreamsDisconnected: streamsDisconnected.Load(),
		Denials: denials.Load(), UpstreamCalls: upstreamCalls.Load(), UpstreamMaxMillis: upstreamMaxMillis.Load(),
	}
	if c.UpstreamCalls > 0 {
		c.UpstreamMeanMillis = float64(upstreamMillis.Load()) / float64(c.UpstreamCalls)
	}
	return c
}
