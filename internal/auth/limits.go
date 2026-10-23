package auth

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Limits struct {
	RunsPerMinute     int
	MessagesPerMinute int
	UploadsPerMinute  int
	SSEConnections    int
}

func DefaultLimits() Limits {
	return Limits{RunsPerMinute: 30, MessagesPerMinute: 60, UploadsPerMinute: 10, SSEConnections: 4}
}

func (l Limits) validate() error {
	if l.RunsPerMinute < 1 || l.MessagesPerMinute < 1 || l.UploadsPerMinute < 1 || l.SSEConnections < 1 {
		return errors.New("request limits must be positive integers")
	}
	return nil
}

const maxLimitAccounts = 10000

type accountLimit struct {
	start                            time.Time
	runs, messages, uploads, streams int
}

// shortcut: counters reset on restart; use shared state before adding replicas.
type requestLimits struct {
	mu       sync.Mutex
	config   Limits
	accounts map[string]*accountLimit
	sweepAt  time.Time
}

func newRequestLimits(c Limits) *requestLimits {
	return &requestLimits{config: c, accounts: make(map[string]*accountLimit)}
}

func (a *Auth) limit(route string, next http.Handler) http.Handler {
	switch route {
	case "POST /api/chats/{chatId}/messages", "POST /api/assets", "GET /api/runs/{runId}/events":
	default:
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok {
			a.apiError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in required")
			return
		}
		retry, release := a.limits.acquire(u.ID, route, a.now())
		if retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{
				"code": "RATE_LIMITED", "message": "Account request limit reached; retry later", "requestId": newRequestID(), "retryAfterSeconds": retry,
			}})
			return
		}
		if release != nil {
			defer release()
		}
		next.ServeHTTP(w, r)
	})
}

func (l *requestLimits) acquire(id, route string, now time.Time) (int, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.sweepAt) {
		for id, b := range l.accounts {
			if b.streams == 0 && !now.Before(b.start.Add(time.Minute)) {
				delete(l.accounts, id)
			}
		}
		l.sweepAt = now.Add(time.Minute)
	}
	b := l.accounts[id]
	if b == nil {
		if len(l.accounts) >= maxLimitAccounts {
			return 60, nil
		}
		b = &accountLimit{start: now}
		l.accounts[id] = b
	}
	if !now.Before(b.start.Add(time.Minute)) {
		b.start, b.runs, b.messages, b.uploads = now, 0, 0, 0
	}
	retry := int((b.start.Add(time.Minute).Sub(now) + time.Second - 1) / time.Second)
	switch route {
	case "POST /api/chats/{chatId}/messages":
		// Sending a message creates a run; both budgets must admit it atomically.
		if b.runs >= l.config.RunsPerMinute || b.messages >= l.config.MessagesPerMinute {
			return retry, nil
		}
		b.runs++
		b.messages++
	case "POST /api/assets":
		if b.uploads >= l.config.UploadsPerMinute {
			return retry, nil
		}
		b.uploads++
	case "GET /api/runs/{runId}/events":
		if b.streams >= l.config.SSEConnections {
			return 1, nil
		}
		b.streams++
		return 0, func() { l.mu.Lock(); b.streams--; l.mu.Unlock() }
	}
	return 0, nil
}
