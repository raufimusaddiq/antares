package auth

import (
	"net/http"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Route is one method + path pattern.
type Route struct{ Method, Path string }

// ChatRoutes is every browser-facing route that touches Hermes-backed data
// (PRD §9, §9A.7). All of them sit behind Active until the slice that
// implements them replaces the placeholder handler; adding a route here is how
// it gets the guard, and the route-coverage test fails if one in the PRD table
// is missing.
var ChatRoutes = []Route{
	{"GET", "/api/chats"},
	{"POST", "/api/chats"},
	{"GET", "/api/chats/{chatId}"},
	{"PATCH", "/api/chats/{chatId}"},
	{"DELETE", "/api/chats/{chatId}"},
	{"GET", "/api/chats/{chatId}/messages"},
	{"POST", "/api/chats/{chatId}/messages"},
	{"GET", "/api/chats/{chatId}/queue"},
	{"POST", "/api/chats/{chatId}/queue"},
	{"PATCH", "/api/chats/{chatId}/queue"},
	{"POST", "/api/chats/{chatId}/queue/next"},
	{"PATCH", "/api/chats/{chatId}/queue/{itemId}"},
	{"DELETE", "/api/chats/{chatId}/queue/{itemId}"},
	{"GET", "/api/chats/{chatId}/messages/{seq}/artifacts/{n}"},
	{"GET", "/api/chats/{chatId}/messages/{seq}/artifacts/{n}/preview"},
	{"GET", "/api/chats/{chatId}/search"},
	{"GET", "/api/chats/{chatId}/usage"},
	{"GET", "/api/chats/{chatId}/input"},
	{"GET", "/api/me/budget"},
	{"PUT", "/api/me/budget"},
	{"GET", "/api/search/chats"},
	{"PUT", "/api/chats/{chatId}/model"},
	{"GET", "/api/chats/{chatId}/settings"},
	{"PUT", "/api/chats/{chatId}/settings"},
	{"GET", "/api/me/model-visibility"},
	{"PUT", "/api/me/model-visibility"},
	{"GET", "/api/chats/{chatId}/draft"},
	{"PUT", "/api/chats/{chatId}/draft"},
	{"DELETE", "/api/chats/{chatId}/draft"},
	{"GET", "/api/me/new-chat-draft"},
	{"PUT", "/api/me/new-chat-draft"},
	{"DELETE", "/api/me/new-chat-draft"},
	{"GET", "/api/me/templates"},
	{"POST", "/api/me/templates"},
	{"PUT", "/api/me/templates/{templateId}"},
	{"DELETE", "/api/me/templates/{templateId}"},
	{"POST", "/api/chats/{chatId}/fork"},
	{"POST", "/api/chats/{chatId}/regenerate"},
	{"POST", "/api/chats/{chatId}/edit-turn"},
	{"POST", "/api/chats/{chatId}/rewind"},
	{"GET", "/api/runs/{runId}"},
	{"GET", "/api/runs/{runId}/events"},
	{"POST", "/api/runs/{runId}/cancel"},
	{"POST", "/api/runs/{runId}/approve"},
	{"POST", "/api/runs/{runId}/clarify"},
	{"POST", "/api/runs/{runId}/steer"},
	{"POST", "/api/runs/{runId}/resolve-unknown"},
	{"GET", "/api/capabilities"},
	{"GET", "/api/models"},
	{"GET", "/api/skills"},
	{"GET", "/api/toolsets"},
	{"POST", "/api/assets"},
	{"GET", "/api/assets/{assetId}"},
	{"DELETE", "/api/assets/{assetId}"},
	{"GET", "/api/jobs"},
	{"POST", "/api/jobs"},
	{"GET", "/api/jobs/{jobId}"},
	{"PATCH", "/api/jobs/{jobId}"},
	{"DELETE", "/api/jobs/{jobId}"},
	{"POST", "/api/jobs/{jobId}/pause"},
	{"POST", "/api/jobs/{jobId}/resume"},
	{"POST", "/api/jobs/{jobId}/run"},
}

// PushRoutes are the Web Push routes (PRD §22, UXP-002). They touch no Hermes data
// but only an eligible owner may use them, so they sit behind the same Active guard
// and same-origin check as ChatRoutes. They are mounted only when a push service is
// supplied, and a handler the service does not provide is not mounted at all.
var PushRoutes = []Route{
	{"GET", "/api/push/public-key"},
	{"POST", "/api/push/subscriptions"},
	{"DELETE", "/api/push/subscriptions/{subId}"},
	{"GET", "/api/me/notifications"},
	{"PUT", "/api/me/notifications"},
}

// Register mounts the identity routes and the guarded placeholders. The status
// screens are the React app (web/); it reads /api/me.
func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/google/start", a.start)
	mux.HandleFunc("GET "+callbackPath, a.callback)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("POST /api/auth/logout-all", a.logoutAll)
	mux.Handle("GET /api/me", a.signedIn(http.HandlerFunc(a.me)))
	mux.Handle("GET /api/account/status", a.signedIn(http.HandlerFunc(a.accountStatus)))

	notBuilt := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.apiError(w, r, http.StatusNotImplemented, "CAPABILITY_UNAVAILABLE", "Not available yet")
	})
	var built map[string]http.Handler
	if a.chat != nil {
		built = a.chat.Handlers()
	}
	for _, rt := range ChatRoutes {
		key := rt.Method + " " + rt.Path
		var h http.Handler = notBuilt
		if bh, ok := built[key]; ok {
			h = bh
		}
		mux.Handle(key, a.Active(a.csrf(rt.Method, a.limit(key, h))))
	}
	if a.push != nil {
		pushHandlers := a.push.Handlers()
		for _, rt := range PushRoutes {
			if h, ok := pushHandlers[rt.Method+" "+rt.Path]; ok {
				mux.Handle(rt.Method+" "+rt.Path, a.Active(a.csrf(rt.Method, h)))
			}
		}
	}
	// The whole admin tree is guarded. Anything not registered below is a plain
	// 404, not a "not built yet" stub: /api/admin/system was removed rather than
	// left looking implemented (#47).
	mux.Handle("/api/admin/", a.Admin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.apiError(w, r, http.StatusNotFound, "NOT_FOUND", "No such admin route")
	})))
	a.registerAdmin(mux)
}

// csrf requires a same-origin request for a cookie-authenticated mutation
// (PRD §4.2). Reads pass through.
func (a *Auth) csrf(method string, next http.Handler) http.Handler {
	if method == http.MethodGet || method == http.MethodHead {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.sameOrigin(r) {
			a.apiError(w, r, http.StatusForbidden, "FORBIDDEN", "Cross-origin request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- read models: the only account data the browser receives ----------------

// AccountView is the whole of what /api/me returns. It is deliberately a closed
// struct of plain strings and booleans: nothing about a tenant binding other
// than its status, so no credential can reach the browser through it.
type AccountView struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	AvatarURL      string `json:"avatarUrl"`
	Role           string `json:"role"`
	ApprovalStatus string `json:"approvalStatus"`
	TenantStatus   string `json:"tenantStatus"`
	State          string `json:"state"`
	RequestedAt    string `json:"requestedAt"`
	CanChat        bool   `json:"canChat"`
	CanAdmin       bool   `json:"canAdmin"`
}

func viewOf(u store.User) AccountView {
	st := StateOf(u)
	return AccountView{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL,
		Role: u.Role, ApprovalStatus: u.ApprovalStatus, TenantStatus: u.TenantStatus, State: st,
		RequestedAt: u.CreatedAt, CanChat: st == StateActive, CanAdmin: st == StateActive && u.Role == "ADMIN"}
}

func (a *Auth) me(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	writeJSON(w, http.StatusOK, viewOf(u))
}

// StatusView is /api/account/status.
type StatusView struct {
	State          string `json:"state"`
	ApprovalStatus string `json:"approvalStatus"`
	TenantStatus   string `json:"tenantStatus"`
	RequestedAt    string `json:"requestedAt"`
	CanChat        bool   `json:"canChat"`
}

func (a *Auth) accountStatus(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	v := viewOf(u)
	writeJSON(w, http.StatusOK, StatusView{State: v.State, ApprovalStatus: v.ApprovalStatus, TenantStatus: v.TenantStatus,
		RequestedAt: v.RequestedAt, CanChat: v.CanChat})
}
