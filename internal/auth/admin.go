package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Tenants is what the admin API needs from the provisioning pipeline: start (or
// absorb a repeat of) a run for one account. The pipeline decides whether there
// is anything to do, so calling it is always safe.
type Tenants interface {
	Kick(userID string)
}

var userIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// AdminUserView is the whole of what an administrator receives about an
// account. It is a closed struct of plain strings: no profile name, credential
// or content field exists on it, so none can be serialised (invariant 6, rule 2).
type AdminUserView struct {
	ID              string       `json:"id"`
	Email           string       `json:"email"`
	DisplayName     string       `json:"displayName"`
	Role            string       `json:"role"`
	ApprovalStatus  string       `json:"approvalStatus"`
	TenantStatus    string       `json:"tenantStatus"`
	TenantDetail    string       `json:"tenantDetail"`
	TenantAttempts  int          `json:"tenantAttempts"`
	TenantCheckedAt string       `json:"tenantCheckedAt"`
	CreatedAt       string       `json:"createdAt"`
	ApprovedAt      string       `json:"approvedAt"`
	LastLoginAt     string       `json:"lastLoginAt"`
	Binding         *BindingView `json:"binding"`
}

// BindingView is a binding's status, nothing else.
type BindingView struct {
	Status     string `json:"status"`
	VerifiedAt string `json:"verifiedAt"`
}

func adminView(u store.AdminUser) AdminUserView {
	v := AdminUserView{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: u.Role,
		ApprovalStatus: u.ApprovalStatus, TenantStatus: u.TenantStatus, TenantDetail: u.TenantDetail,
		TenantAttempts: u.TenantAttempts, TenantCheckedAt: u.TenantCheckedAt, CreatedAt: u.CreatedAt,
		ApprovedAt: u.ApprovedAt, LastLoginAt: u.LastLoginAt}
	if u.BindingStatus != "" {
		v.Binding = &BindingView{Status: u.BindingStatus, VerifiedAt: u.BindingVerifiedAt}
	}
	return v
}

// AuditView is one audit row; metadata only.
type AuditView struct {
	ID        string `json:"id"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	TargetID  string `json:"targetId"`
	Outcome   string `json:"outcome"`
	RequestID string `json:"requestId"`
	CreatedAt string `json:"createdAt"`
}

// adminRoute wraps an admin handler in the Admin guard, refuses request
// parameters only the server may choose, and, for mutations, enforces the
// same-origin CSRF check (PRD §4.2).
func (a *Auth) adminRoute(mutating bool, h http.HandlerFunc) http.Handler {
	return a.Admin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name := forbiddenInput(r); name != "" {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request carries a field only the server may choose: "+name)
			return
		}
		if mutating && !a.sameOrigin(r) {
			a.apiError(w, r, http.StatusForbidden, "FORBIDDEN", "Cross-origin request refused")
			return
		}
		h(w, r)
	}))
}

// registerAdmin mounts the admin tree. More specific patterns win over the
// guarded "/api/admin/" placeholder registered by Register, which keeps
// answering for everything not listed here.
func (a *Auth) registerAdmin(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/users", a.adminRoute(false, a.adminList))
	mux.Handle("GET /api/admin/users/{userId}", a.adminRoute(false, a.adminGet))
	for _, act := range []string{store.ActionApprove, store.ActionReject, store.ActionSuspend, store.ActionReactivate} {
		mux.Handle("POST /api/admin/users/{userId}/"+act, a.adminRoute(true, a.adminDecide(act)))
	}
	mux.Handle("POST /api/admin/users/{userId}/revoke-sessions", a.adminRoute(true, a.adminRevokeSessions))
	mux.Handle("POST /api/admin/users/{userId}/tenant-binding", a.adminRoute(true, a.adminRequeue(store.RequeueProvision)))
	mux.Handle("POST /api/admin/users/{userId}/tenant-verify", a.adminRoute(true, a.adminRequeue(store.RequeueVerify)))
	mux.Handle("GET /api/admin/audit", a.adminRoute(false, a.adminAudit))
	mux.Handle("GET /api/admin/model-policy", a.adminRoute(false, a.adminModelPolicy))
	mux.Handle("PUT /api/admin/model-policy", a.adminRoute(true, a.adminSetModelPolicy))
	mux.Handle("GET /api/admin/tool-policy", a.adminRoute(false, a.adminToolPolicy))
	mux.Handle("PUT /api/admin/tool-policy", a.adminRoute(true, a.adminSetToolPolicy))
	mux.Handle("GET /api/admin/kenari-usage", a.adminRoute(false, a.adminKenariUsage))
	mux.Handle("GET /api/admin/metrics", a.adminRoute(false, a.adminMetrics))
}

func targetID(w http.ResponseWriter, r *http.Request, a *Auth) (string, bool) {
	id := r.PathValue("userId")
	if !userIDRE.MatchString(id) {
		a.apiError(w, r, http.StatusNotFound, "NOT_FOUND", "No such user")
		return "", false
	}
	return id, true
}

var approvalFilters = map[string]bool{"PENDING": true, "APPROVED": true, "REJECTED": true, "SUSPENDED": true}

func page(r *http.Request) (limit, offset int, ok bool) {
	limit, offset = 50, 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return 0, 0, false
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}

// validSearch bounds a directory search: short, printable, nothing the SQL layer must trust.
func validSearch(s string) bool {
	if utf8.RuneCountInString(s) > 100 {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func (a *Auth) adminList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := page(r)
	status := r.URL.Query().Get("status")
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if !ok || (status != "" && !approvalFilters[status]) || !validSearch(search) {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid status, search, limit or offset")
		return
	}
	users, err := store.ListAdminUsers(r.Context(), a.db, status, search, limit, offset)
	if err != nil {
		log.Printf("auth: admin list failed: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Could not list users")
		return
	}
	out := make([]AdminUserView, 0, len(users))
	for _, u := range users {
		out = append(out, adminView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "limit": limit, "offset": offset})
}

func (a *Auth) adminGet(w http.ResponseWriter, r *http.Request) {
	id, ok := targetID(w, r, a)
	if !ok {
		return
	}
	u, err := store.GetAdminUser(r.Context(), a.db, id)
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminView(u))
}

// decodeBody reads an optional JSON object into dst. Unknown fields are an
// error, not ignored: a body that names a profile, key or session is refused
// (rule 2). An absent body is fine.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.Is(err, io.EOF)
	}
	return dec.Decode(&struct{}{}) == io.EOF
}

func (a *Auth) adminDecide(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := targetID(w, r, a)
		if !ok {
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if !decodeBody(w, r, &body) {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Body must be {\"reason\": \"...\"} and nothing else")
			return
		}
		actor, _ := UserFrom(r.Context())
		u, err := store.Decide(r.Context(), a.db, actor.ID, id, action, body.Reason, newRequestID(), a.now())
		if err != nil {
			a.adminError(w, r, err)
			return
		}
		if (action == store.ActionApprove || action == store.ActionReactivate) && a.tenants != nil {
			a.tenants.Kick(id)
		}
		if action == store.ActionSuspend {
			// Re-read either way: the view shows the pending mark, or that it was cleared.
			a.pauseJobs(r.Context(), actor.ID, id)
			if again, err := store.GetAdminUser(r.Context(), a.db, id); err == nil {
				u = again
			}
		}
		writeJSON(w, http.StatusOK, adminView(u))
	}
}

// jobPauser is the chat service's part in a suspension (PRD §12 rule 3): pause the account's scheduled jobs upstream.
type jobPauser interface {
	PauseUserJobs(ctx context.Context, userID string) error
}

// pauseJobs pauses a just-suspended account's jobs and audits the outcome (admin.suspend_jobs, ok or pending). The
// suspension already recorded SUSPENSION_PENDING_JOB_SHUTDOWN in its own transaction; it is cleared here only when the
// pause is verified, so a failure, or a restart before this runs, leaves it to the chat service's retry loop. Without
// a chat service there are no jobs to pause and the mark is cleared. It reports whether the mark is still set.
func (a *Auth) pauseJobs(ctx context.Context, actor, id string) (pending bool) {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	var perr error
	if p, ok := a.chat.(jobPauser); ok {
		perr = p.PauseUserJobs(pctx, id)
	}
	cancel()
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer rcancel()
	outcome := "ok"
	if perr != nil {
		outcome, pending = "pending", true
	} else if err := store.ClearJobShutdownPending(rctx, a.db, id); err != nil {
		log.Printf("auth: user %s: jobs paused, but the pending mark could not be cleared (the retry will): %v", id, err)
		pending = true
	}
	if err := store.Audit(rctx, a.db, actor, "admin.suspend_jobs", "user", id, outcome, newRequestID(), a.now()); err != nil {
		log.Printf("auth: audit write failed: %v", err)
	}
	return pending
}

// adminRevokeSessions signs the user out everywhere (#113): every session ends, devices stop getting alerts, open run
// streams stop at once. The account and its data are untouched. Audited as metadata only (actor, target, action).
func (a *Auth) adminRevokeSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := targetID(w, r, a)
	if !ok {
		return
	}
	var body struct{}
	if !decodeBody(w, r, &body) {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "This action takes no body")
		return
	}
	if _, err := store.GetAdminUser(r.Context(), a.db, id); err != nil { // an unknown user is the same 404 as elsewhere
		a.adminError(w, r, err)
		return
	}
	actor, _ := UserFrom(r.Context())
	now := a.now()
	// Revocation and audit commit together; a failure of either changes nothing.
	if err := store.AdminRevokeSessions(r.Context(), a.db, actor.ID, id, newRequestID(), now); err != nil {
		a.adminError(w, r, err)
		return
	}
	a.revoked(id, "")
	u, err := store.GetAdminUser(r.Context(), a.db, id)
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminView(u))
}

func (a *Auth) adminRequeue(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := targetID(w, r, a)
		if !ok {
			return
		}
		var body struct{}
		if !decodeBody(w, r, &body) {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "This action takes no body: the server chooses the profile and credential")
			return
		}
		actor, _ := UserFrom(r.Context())
		u, err := store.RequeueTenant(r.Context(), a.db, actor.ID, id, mode, newRequestID(), a.now())
		if err != nil {
			a.adminError(w, r, err)
			return
		}
		if a.tenants != nil {
			a.tenants.Kick(id)
		}
		writeJSON(w, http.StatusAccepted, adminView(u))
	}
}

// auditCursor is "<created_at>|<id>" of the last entry on the previous page. It
// is only ever a position in the feed: the store binds both halves as values.
func auditCursor(s string) (at, id string, ok bool) {
	if s == "" {
		return "", "", true
	}
	at, id, found := strings.Cut(s, "|")
	if !found || at == "" || id == "" || len(at) > 64 || len(id) > 64 {
		return "", "", false
	}
	for _, c := range at + id {
		if unicode.IsControl(c) {
			return "", "", false
		}
	}
	return at, id, true
}

func (a *Auth) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid limit or before")
			return
		}
		limit = n
	}
	at, id, ok := auditCursor(r.URL.Query().Get("before"))
	if !ok || r.URL.Query().Has("offset") {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid limit or before")
		return
	}
	rows, more, err := store.ListAudit(r.Context(), a.db, limit, at, id)
	if err != nil {
		log.Printf("auth: audit list failed: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Could not read the audit feed")
		return
	}
	out := make([]AuditView, 0, len(rows))
	for _, e := range rows {
		out = append(out, AuditView{ID: e.ID, Actor: e.Actor, Action: e.Action, TargetID: e.TargetID,
			Outcome: e.Outcome, RequestID: e.RequestID, CreatedAt: e.CreatedAt})
	}
	next := ""
	if more && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = last.CreatedAt + "|" + last.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "limit": limit, "next": next})
}

func (a *Auth) adminError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.apiError(w, r, http.StatusNotFound, "NOT_FOUND", "No such user")
	case errors.Is(err, store.ErrReason):
		a.apiError(w, r, http.StatusBadRequest, "REASON_REQUIRED", "A reason is required for this action")
	case errors.Is(err, store.ErrConflict):
		a.apiError(w, r, http.StatusConflict, "STATE_CONFLICT", "That action is not allowed from the account's current state")
	case errors.Is(err, store.ErrNoBinding):
		a.apiError(w, r, http.StatusConflict, "NO_BINDING", "The account has no tenant binding to verify")
	case errors.Is(err, store.ErrSelf), errors.Is(err, store.ErrNotAdmin):
		a.apiError(w, r, http.StatusForbidden, "FORBIDDEN", "You cannot do that to this account")
	case errors.Is(err, store.ErrReasonInvalid):
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The reason is too long or contains control characters")
	default:
		log.Printf("auth: admin action failed: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Could not complete the action")
	}
}

// The global model block list (PRD §26.1, LLM-ACC-003). It is policy, not user content: an
// admin sets which models nobody may pick; it always wins over a user's own hiding.
type modelPolicy struct {
	Blocked []string `json:"blocked"`
}

func (a *Auth) adminModelPolicy(w http.ResponseWriter, r *http.Request) {
	ids, err := store.BlockedModels(r.Context(), a.db)
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	writeJSON(w, http.StatusOK, modelPolicy{Blocked: ids})
}

func (a *Auth) adminSetModelPolicy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body modelPolicy
	if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || body.Blocked == nil || len(body.Blocked) > store.MaxModelIDs {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Body must be {\"blocked\": [model ids]} and nothing else")
		return
	}
	for _, id := range body.Blocked {
		if id == "" || len(id) > 200 || !utf8.ValidString(id) || strings.ContainsFunc(id, unicode.IsControl) {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "blocked carries an invalid model id")
			return
		}
	}
	actor, _ := UserFrom(r.Context())
	if err := store.SetBlockedModels(r.Context(), a.db, actor.ID, body.Blocked, newRequestID(), a.now()); err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	a.adminModelPolicy(w, r)
}
