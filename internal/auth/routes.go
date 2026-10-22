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
	{"PUT", "/api/chats/{chatId}/model"},
	{"POST", "/api/chats/{chatId}/fork"},
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
	{"GET", "/api/jobs"},
	{"POST", "/api/jobs"},
	{"GET", "/api/jobs/{jobId}"},
	{"PATCH", "/api/jobs/{jobId}"},
	{"DELETE", "/api/jobs/{jobId}"},
	{"POST", "/api/jobs/{jobId}/pause"},
	{"POST", "/api/jobs/{jobId}/resume"},
	{"POST", "/api/jobs/{jobId}/run"},
}

// Register mounts the identity routes and the guarded placeholders. The status
// screens are the React app (web/); it reads /api/me.
func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/google/start", a.start)
	mux.HandleFunc("GET "+callbackPath, a.callback)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.Handle("GET /api/me", a.signedIn(http.HandlerFunc(a.me)))
	mux.Handle("GET /api/account/status", a.signedIn(http.HandlerFunc(a.accountStatus)))

	notBuilt := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.apiError(w, r, http.StatusNotImplemented, "CAPABILITY_UNAVAILABLE", "Not available yet")
	})
	for _, rt := range ChatRoutes {
		mux.Handle(rt.Method+" "+rt.Path, a.Active(notBuilt))
	}
	// The whole admin tree is guarded. The catch-all answers for anything not
	// listed below; the specific routes win over it.
	mux.Handle("/api/admin/", a.Admin(notBuilt))
	a.registerAdmin(mux)
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
