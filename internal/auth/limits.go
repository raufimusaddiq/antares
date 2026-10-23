package auth

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/quota"
)

type Limits = quota.Limits

func DefaultLimits() Limits { return quota.DefaultLimits() }

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
		u, _ := UserFrom(r.Context())
		retry, release := a.limits.acquire(u.ID, route, a.now())
		if retry != 0 {
			WriteRateLimited(w, "Account request limit reached; retry later", retry)
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
			return -1, nil
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
			return -1, nil // A live connection has no known release time.
		}
		b.streams++
		return 0, func() { l.mu.Lock(); b.streams--; l.mu.Unlock() }
	}
	return 0, nil
}

// WriteRateLimited uses null when capacity depends on work finishing rather than a known deadline.
func WriteRateLimited(w http.ResponseWriter, message string, retry int) {
	var hint *int
	if retry > 0 {
		hint = &retry
		w.Header().Set("Retry-After", strconv.Itoa(retry))
	} else {
		w.Header().Del("Retry-After")
	}
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{
		"code": "RATE_LIMITED", "message": message, "requestId": newRequestID(), "retryAfterSeconds": hint,
	}})
}
