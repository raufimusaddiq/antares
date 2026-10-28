package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/obs"
	"github.com/raufimusaddiq/antares/internal/store"
)

const (
	sessionCookie = "__Host-antares_session"
	flowCookie    = "__Host-antares_oidc"

	sessionTTL = 7 * 24 * time.Hour // absolute; sessions are not extended by use
	flowTTL    = 10 * time.Minute
	maxFlows   = 4096 // bounds memory against a flood of /start requests

	callbackPath = "/api/auth/google/callback"
)

// Options configures Auth. Every field except HTTPClient and Now is required.
type Options struct {
	DB           *sql.DB
	Endpoints    Endpoints
	ClientID     string
	ClientSecret string
	PublicOrigin string // https origin, no trailing slash
	HTTPClient   *http.Client
	Now          func() time.Time
	// Tenants starts provisioning when an admin approves or requeues an account.
	// Without it the approval endpoints still change status but nothing is
	// provisioned, which tests of the guards rely on.
	Tenants Tenants
	// Chat supplies the handlers for the chat routes. Routes it does not supply
	// stay behind the guard with the "not available" placeholder.
	Chat   ChatHandlers
	Limits *Limits // nil uses DefaultLimits
	// Push supplies the notification routes (PushRoutes). Without it they are not mounted.
	Push ChatHandlers
	// KenariUsage reads the shared provider account's usage. It is mounted on the admin tree only
	// (GET /api/admin/kenari-usage); nil leaves the route answering "not configured".
	KenariUsage AccountUsage
	// DefaultToolsets is the tenant toolset policy while an admin has set none (ANTARES_TENANT_ALLOWED_TOOLSETS),
	// shown in Admin → Assistant tools.
	DefaultToolsets []string
}

// AccountUsage is the provider account reader the admin route calls.
type AccountUsage interface {
	Snapshot(ctx context.Context, refresh bool) (kenari.AccountSnapshot, error)
}

// ChatHandlers is what the chat package gives the identity layer to mount:
// handlers keyed "METHOD /path" exactly as in ChatRoutes. Auth wraps each in
// the Active guard (and the same-origin check for mutations), so a handler
// never sees an unauthenticated or ineligible request.
type ChatHandlers interface {
	Handlers() map[string]http.Handler
}

// Auth owns login, logout, sessions and the guards.
type Auth struct {
	db       *sql.DB
	ep       Endpoints
	clientID string
	secret   string
	origin   string
	client   *http.Client
	now      func() time.Time
	ver      *verifier
	flows    *flowStore
	tenants  Tenants
	chat     ChatHandlers
	push     ChatHandlers
	usage    AccountUsage
	limits   *requestLimits
	tools    []string // DefaultToolsets
}

func New(o Options) (*Auth, error) {
	if o.DB == nil || o.ClientID == "" || o.ClientSecret == "" || o.PublicOrigin == "" ||
		o.Endpoints.Auth == "" || o.Endpoints.Token == "" || o.Endpoints.JWKS == "" || o.Endpoints.Issuer == "" || len(o.Endpoints.Issued) == 0 {
		return nil, errors.New("auth: database, Google client id/secret, public origin and endpoints are all required")
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: exchangeLimit}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	limits := DefaultLimits()
	if o.Limits != nil {
		limits = *o.Limits
	}
	if limits.RunsPerMinute < 1 || limits.MessagesPerMinute < 1 || limits.UploadsPerMinute < 1 || limits.ArtifactsPerMinute < 1 || limits.HistoryPerMinute < 1 || limits.DraftWritesPerMinute < 1 || limits.SSEConnections < 1 {
		return nil, errors.New("request limits must be positive integers")
	}
	a := &Auth{limits: newRequestLimits(limits), db: o.DB, ep: o.Endpoints, clientID: o.ClientID, secret: o.ClientSecret,
		origin: strings.TrimRight(o.PublicOrigin, "/"), client: o.HTTPClient, now: o.Now, flows: newFlowStore(), tenants: o.Tenants, chat: o.Chat, push: o.Push, usage: o.KenariUsage, tools: o.DefaultToolsets}
	a.ver = &verifier{ep: o.Endpoints, clientID: o.ClientID, client: o.HTTPClient, now: o.Now}
	return a, nil
}

// ---- tokens -------------------------------------------------------------

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is what the database stores for a cookie value.
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// ---- OIDC flow state ------------------------------------------------------

// flow is one in-flight login. It lives in memory, is single-use and expires,
// so a replayed or stale callback finds nothing. A restart drops in-flight
// logins; the user simply signs in again.
type flow struct {
	nonce, verifier, browserHash string
	expires                      time.Time
}

type flowStore struct {
	mu sync.Mutex
	m  map[string]flow // key: hash(state)
}

func newFlowStore() *flowStore { return &flowStore{m: map[string]flow{}} }

func (s *flowStore) put(stateHash string, f flow, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) >= maxFlows {
		for k, v := range s.m {
			if !now.Before(v.expires) {
				delete(s.m, k)
			}
		}
		if len(s.m) >= maxFlows {
			return false
		}
	}
	s.m[stateHash] = f
	return true
}

// take removes and returns the flow: a state can be redeemed exactly once.
func (s *flowStore) take(stateHash string, now time.Time) (flow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.m[stateHash]
	delete(s.m, stateHash)
	if !ok || !now.Before(f.expires) {
		return flow{}, false
	}
	return f, true
}

// ---- cookies ---------------------------------------------------------------

func (a *Auth) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", // __Host- prefix requires Path=/ and no Domain
		MaxAge: int(ttl.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// ---- login ------------------------------------------------------------------

// start begins the Authorization Code + PKCE flow.
func (a *Auth) start(w http.ResponseWriter, r *http.Request) {
	state, nonce, verifier, browser := randomToken(), randomToken(), randomToken(), randomToken()
	now := a.now()
	if !a.flows.put(hashToken(state), flow{nonce: nonce, verifier: verifier, browserHash: hashToken(browser), expires: now.Add(flowTTL)}, now) {
		a.apiError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many sign-ins in progress; try again shortly")
		return
	}
	a.setCookie(w, flowCookie, browser, flowTTL)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {a.clientID},
		"redirect_uri":          {a.origin + callbackPath},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, a.ep.Auth+"?"+q.Encode(), http.StatusFound)
}

// callback completes the login. Every failure is the same redirect to the
// error screen; the specific reason goes to the audit log only, so the
// endpoint is not an oracle.
func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	reqID := newRequestID()
	ctx := r.Context()
	now := a.now()
	fail := func(reason string) {
		clearCookie(w, flowCookie)
		if err := store.Audit(ctx, a.db, "", "auth.login", "google_login", "", "rejected:"+reason, reqID, now); err != nil {
			log.Printf("auth: audit write failed: %v", err)
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/error?code=login_failed", http.StatusSeeOther)
	}

	q := r.URL.Query()
	f, ok := a.flows.take(hashToken(q.Get("state")), now) // consumed even if the rest fails
	if !ok {
		fail("state")
		return
	}
	c, err := r.Cookie(flowCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(hashToken(c.Value)), []byte(f.browserHash)) != 1 {
		fail("browser_binding")
		return
	}
	if q.Get("error") != "" {
		fail("provider_denied")
		return
	}
	code := q.Get("code")
	if code == "" {
		fail("no_code")
		return
	}
	raw, err := exchange(ctx, a.client, a.ep, a.clientID, a.secret, a.origin+callbackPath, code, f.verifier)
	if err != nil {
		fail("exchange")
		return
	}
	claims, err := a.ver.verify(ctx, raw, f.nonce)
	if err != nil {
		fail("id_token")
		return
	}

	u, _, err := store.UpsertLogin(ctx, a.db, a.ep.Issuer, claims.Subject, claims.Email, claims.Name, safeAvatar(claims.Picture), now)
	if err != nil {
		log.Printf("auth: login store failed: %v", err)
		fail("store")
		return
	}
	// Rotate: whatever session this browser held is retired before a fresh token
	// is issued. If it cannot be retired the login fails and nothing new is
	// issued: a rotation that silently leaves the old session valid is no rotation.
	if old, err := r.Cookie(sessionCookie); err == nil {
		if err := store.RevokeSession(ctx, a.db, hashToken(old.Value), now); err != nil {
			log.Printf("auth: session rotation failed: %v", err)
			fail("rotation")
			return
		}
	}
	token := randomToken()
	if err := store.CreateSession(ctx, a.db, u.ID, hashToken(token), now, sessionTTL); err != nil {
		log.Printf("auth: session create failed: %v", err)
		fail("store")
		return
	}
	if err := store.Audit(ctx, a.db, u.ID, "auth.login", "user", u.ID, "ok", reqID, now); err != nil {
		// An unaudited login is not allowed to stand.
		log.Printf("auth: audit write failed: %v", err)
		if err := store.RevokeSession(ctx, a.db, hashToken(token), now); err != nil {
			// The token was never sent to the browser, so it cannot be presented.
			log.Printf("auth: could not revoke unsent session: %v", err)
		}
		fail("store")
		return
	}
	clearCookie(w, flowCookie)
	a.setCookie(w, sessionCookie, token, sessionTTL)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// safeAvatar keeps only an https URL; the value is later placed in an <img>.
func safeAvatar(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(s) > 2048 {
		return ""
	}
	return s
}

// logoutAll ends every session of the caller's own account, this one included, and clears the cookie (#150,
// PRD §26.1). Another device's next request is a 401, and an open run stream ends at its next heartbeat
// (SessionLive). Only the caller's own sessions are touched: the account comes from the session, never the request.
func (a *Auth) logoutAll(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		a.apiError(w, r, http.StatusForbidden, "FORBIDDEN", "Cross-origin request refused")
		return
	}
	u, ok := a.current(r)
	if !ok {
		clearCookie(w, sessionCookie)
		a.apiError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in required")
		return
	}
	now := a.now()
	if _, err := store.RevokeUserSessions(r.Context(), a.db, u.ID, now); err != nil {
		log.Printf("auth: revoke all failed: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Could not sign out everywhere; try again")
		return
	}
	a.revoked(u.ID, "")
	if err := store.Audit(r.Context(), a.db, u.ID, "auth.logout_all", "user", u.ID, "ok", newRequestID(), now); err != nil {
		log.Printf("auth: audit write failed: %v", err)
	}
	clearCookie(w, sessionCookie)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// SessionHash is the stored form of the request's session cookie ("" without one): what a revocation names (#150).
func SessionHash(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return hashToken(c.Value)
}

// revocationListener is told when sessions end through a sign-out, so open run streams stop at once instead of at
// their next heartbeat (#150). tokenHash "" means every session of userID.
type revocationListener interface {
	SessionsRevoked(userID, tokenHash string)
}

func (a *Auth) revoked(userID, tokenHash string) {
	if l, ok := a.chat.(revocationListener); ok {
		l.SessionsRevoked(userID, tokenHash)
	}
}

// SessionLive reports whether the request's session cookie still names a live session. A long-lived response (the
// run event stream) calls it while it is open, so a sign-out, a sign-out everywhere or a revocation ends it. Only a
// session the database says is gone is "not live": a lookup that failed (a busy database, a cancelled request) proves
// nothing, and the stream's next check asks again.
func SessionLive(ctx context.Context, db *sql.DB, r *http.Request, now time.Time) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	// Not cancelled with the request: a client that leaves mid-query must not interrupt a database read (an interrupted
	// read can leave its connection in a state that makes the next write on it fail).
	_, err = store.SessionUser(context.WithoutCancel(ctx), db, hashToken(c.Value), now)
	if err != nil && !errors.Is(err, store.ErrNoSession) {
		log.Printf("auth: session re-check failed: %v", err)
		return true
	}
	return err == nil
}

// logout invalidates the session server-side and clears the cookie.
func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		a.apiError(w, r, http.StatusForbidden, "FORBIDDEN", "Cross-origin request refused")
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		a.apiError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in required")
		return
	}
	now := a.now()
	h := hashToken(c.Value)
	u, err := store.SessionUser(r.Context(), a.db, h, now)
	if err != nil {
		clearCookie(w, sessionCookie)
		a.apiError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in required")
		return
	}
	if err := store.RevokeSession(r.Context(), a.db, h, now); err != nil {
		log.Printf("auth: revoke failed: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Could not sign out; try again")
		return
	}
	a.revoked(u.ID, h)
	if err := store.Audit(r.Context(), a.db, u.ID, "auth.logout", "user", u.ID, "ok", newRequestID(), now); err != nil {
		log.Printf("auth: audit write failed: %v", err)
	}
	clearCookie(w, sessionCookie)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// sameOrigin is the CSRF check for cookie-authenticated mutations (PRD §4.2).
func (a *Auth) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == a.origin
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// ---- sessions and guards -----------------------------------------------------

type ctxKey struct{}

// UserFrom returns the user a guard resolved for this request.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(store.User)
	return u, ok
}

// current resolves the request's cookie to a live session's user, rechecking
// the database every time: there is no positive authorization cache (AUTH-009).
func (a *Auth) current(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	u, err := store.SessionUser(r.Context(), a.db, hashToken(c.Value), a.now())
	if err != nil {
		if !errors.Is(err, store.ErrNoSession) {
			log.Printf("auth: session lookup failed: %v", err)
		}
		return store.User{}, false
	}
	obs.From(r.Context()).SetUser(u.ID) // the access log's user field: the internal id, nothing else
	return u, true
}

// Account states the UI and the guards agree on.
const (
	StatePending   = "PENDING"
	StateRejected  = "REJECTED"
	StateSuspended = "SUSPENDED"
	StateSetup     = "SETTING_UP"
	StateError     = "ERROR"
	StateActive    = "ACTIVE"
)

// StateOf is the single place that turns the three status fields into what the
// user may do. Default deny: anything unrecognised is not ACTIVE.
func StateOf(u store.User) string {
	switch u.ApprovalStatus {
	case "PENDING":
		return StatePending
	case "REJECTED":
		return StateRejected
	case "SUSPENDED":
		return StateSuspended
	case "APPROVED":
		switch {
		case u.TenantStatus == "READY" && u.TenantBound:
			return StateActive
		case u.TenantStatus == "UNPROVISIONED" || u.TenantStatus == "CONFIGURING":
			return StateSetup
		}
		return StateError // FAILED, DISABLED, or READY with no ready binding
	}
	return StateError
}

// signedIn requires a live session and nothing else: the minimal
// account-status and logout surface open to non-eligible users.
func (a *Auth) signedIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.current(r)
		if !ok {
			a.apiError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// Active is the guard in front of every chat-touching route: valid session,
// APPROVED, tenant READY with a ready binding. It is the Go side of
// can_chat() in PRD §3.2; the live Hermes health check joins it in a later slice.
func (a *Auth) Active(next http.Handler) http.Handler {
	return a.signedIn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if !a.refuseUnlessActive(w, r, u) {
			return
		}
		if name := forbiddenInput(r); name != "" {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request carries a field only the server may choose: "+name)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// Admin guards the /api/admin tree: valid session, role ADMIN, and an account
// that is active and mapped (PRD §3.1 "Admin (active and mapped)", §3.2) — the
// same eligibility as Active. An approved admin whose tenant is unprovisioned,
// failed or unbound is refused like any other user; the admin's own workspace
// is provisioned out of band before the admin surface opens.
func (a *Auth) Admin(next http.Handler) http.Handler {
	return a.signedIn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if !a.refuseUnlessActive(w, r, u) {
			return
		}
		if u.Role != "ADMIN" {
			a.apiError(w, r, http.StatusForbidden, "FORBIDDEN", "Administrator access required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *Auth) refuseUnlessActive(w http.ResponseWriter, r *http.Request, u store.User) bool {
	if StateOf(u) == StateActive {
		return true
	}
	a.refuseState(w, r, u)
	return false
}

func (a *Auth) refuseState(w http.ResponseWriter, r *http.Request, u store.User) {
	switch StateOf(u) {
	case StatePending:
		a.apiError(w, r, http.StatusForbidden, "ACCOUNT_PENDING", "Account is waiting for administrator approval")
	case StateRejected:
		a.apiError(w, r, http.StatusForbidden, "ACCOUNT_REJECTED", "Access was not approved")
	case StateSuspended:
		a.apiError(w, r, http.StatusForbidden, "ACCOUNT_SUSPENDED", "Account access is disabled")
	default:
		a.apiError(w, r, http.StatusConflict, "TENANT_NOT_READY", "Your workspace is not ready")
	}
}

// forbiddenInput names a caller-supplied profile, route prefix, key, header or
// session id (rule 2: reject, never ignore). Bodies are checked by the
// handlers that decode them, with unknown fields refused.
func forbiddenInput(r *http.Request) string {
	for k := range r.URL.Query() {
		switch strings.ToLower(k) {
		case "profile", "profile_name", "profile_id", "route_prefix", "api_key", "apikey", "hermes_session_id", "session_id":
			return "query parameter"
		}
	}
	for _, h := range []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "X-Hermes-Profile", "X-Hermes-Session-Id", "X-Hermes-Session", "X-Hermes-Session-Key", "X-Hermes-Api-Key"} {
		if r.Header.Get(h) != "" {
			return "header"
		}
	}
	return ""
}

// ---- JSON ---------------------------------------------------------------------

func (a *Auth) apiError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if code == "RATE_LIMITED" {
		if status != http.StatusTooManyRequests {
			panic("auth: RATE_LIMITED requires HTTP 429")
		}
		WriteRateLimited(w, msg, 0)
		return
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg, "requestId": newRequestID()}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
