package smoke

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Actor is one account the scenario drives.
type Actor struct {
	Name string
	// Session is a signed-in session. Login, if set, is called to obtain it first
	// (the in-process tests complete a fake OIDC flow; a deployed run is handed a
	// real session instead, because the Google flow cannot be completed headlessly).
	Session *Session
	Login   func(ctx context.Context) (*Session, error)

	id            string
	chatID, runID string
	reply         string
}

// Config is everything the scenario needs. A nil actor makes the steps that need
// it skipped, never passed.
type Config struct {
	Client               *Client
	Admin, A, B, Pending *Actor
	RunID                string
	Prompt               string
	RunTimeout           time.Duration // chat: send → terminal phase
	TenantTimeout        time.Duration // approval → READY, and a re-verification
	FailedGrace          time.Duration // a tenant that stays FAILED this long has failed
	Poll                 time.Duration
	suspendedB           bool
}

func (c *Config) defaults() {
	if c.Prompt == "" {
		c.Prompt = "Smoke test. Reply with one short sentence saying you are working."
	}
	if c.RunTimeout <= 0 {
		c.RunTimeout = 2 * time.Minute
	}
	if c.TenantTimeout <= 0 {
		c.TenantTimeout = 5 * time.Minute
	}
	if c.FailedGrace <= 0 {
		c.FailedGrace = 20 * time.Second
	}
	if c.Poll <= 0 {
		c.Poll = time.Second
	}
	if c.RunID == "" {
		c.RunID = "run"
	}
}

type meView struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	ApprovalStatus string `json:"approvalStatus"`
	TenantStatus   string `json:"tenantStatus"`
	State          string `json:"state"`
	CanChat        bool   `json:"canChat"`
	CanAdmin       bool   `json:"canAdmin"`
}

type adminView struct {
	ID             string `json:"id"`
	ApprovalStatus string `json:"approvalStatus"`
	TenantStatus   string `json:"tenantStatus"`
	TenantDetail   string `json:"tenantDetail"`
	TenantAttempts int    `json:"tenantAttempts"`
	Binding        *struct {
		Status     string `json:"status"`
		VerifiedAt string `json:"verifiedAt"`
	} `json:"binding"`
}

type chatRun struct {
	RunID     string `json:"runId"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode"`
}

func (s *scn) me(ctx context.Context, a *Actor) (meView, *Response, error) {
	r, err := s.c.Do(ctx, a.Session, "GET", "/api/me", nil, nil)
	if err != nil {
		return meView{}, nil, err
	}
	var v meView
	if r.Status == 200 {
		if err := r.JSON(&v); err != nil {
			return v, r, errors.New("/api/me answered in an unexpected shape")
		}
	}
	return v, r, nil
}

type scn struct {
	cfg *Config
	c   *Client
}

// Run executes the whole scenario through r. Every step is recorded; a step
// whose prerequisite did not pass is recorded as skipped.
func Run(ctx context.Context, cfg *Config, r *Runner) {
	cfg.defaults()
	s := &scn{cfg: cfg, c: cfg.Client}
	r.Run("reach.health", "service answers /healthz and /readyz", nil, func() (string, error) { return s.health(ctx) })

	for _, slot := range []struct {
		key string
		a   *Actor
	}{{"admin", cfg.Admin}, {"a", cfg.A}, {"b", cfg.B}, {"pending", cfg.Pending}} {
		a := slot.a
		r.Run("login."+slot.key, "signed-in session works ("+slot.key+")", []string{"reach.health"}, func() (string, error) { return s.login(ctx, a) })
	}
	r.Run("admin.active", "administrator account can use the admin API", []string{"login.admin"}, func() (string, error) { return s.adminActive(ctx) })

	r.Run("negative.unapproved", "unapproved account cannot chat", []string{"login.pending"}, func() (string, error) { return s.unapproved(ctx) })

	r.Run("account.approve", "test accounts A and B are approved", []string{"admin.active", "login.a", "login.b"}, func() (string, error) { return s.approve(ctx) })
	for _, slot := range []struct {
		n string
		a *Actor
	}{{"a", cfg.A}, {"b", cfg.B}} {
		a, n := slot.a, slot.n
		r.Run("tenant.ready."+n, "tenant "+n+" reaches READY", []string{"account.approve"}, func() (string, error) { return s.tenantReady(ctx, a) })
		r.Run("tenant.verify."+n, "tenant "+n+" re-verified now: own credential, toolset policy applied", []string{"tenant.ready." + n}, func() (string, error) { return s.reverify(ctx, a) })
		r.Run("chat."+n+".reply", "user "+n+" sends a message and gets an assistant reply", []string{"tenant.verify." + n}, func() (string, error) { return s.chat(ctx, a) })
	}
	r.Run("negative.cross_user", "two users cannot read each other's chats", []string{"chat.a.reply", "chat.b.reply"}, func() (string, error) { return s.crossUser(ctx) })
	r.Run("negative.revoked", "revoked account loses access immediately", []string{"chat.b.reply", "chat.a.reply", "admin.active"}, func() (string, error) { return s.revoke(ctx) })
	r.Run("cleanup.restore", "revoked test account restored for the next run", []string{"login.admin"}, func() (string, error) { return s.restore(ctx) })
}

func (s *scn) health(ctx context.Context) (string, error) {
	h, err := s.c.Do(ctx, nil, "GET", "/healthz", nil, nil)
	if err != nil {
		return "", err
	}
	if h.Status != 200 {
		return "", fmt.Errorf("/healthz: %s", h.Brief())
	}
	rz, err := s.c.Do(ctx, nil, "GET", "/readyz", nil, nil)
	if err != nil {
		return "", err
	}
	if rz.Status != 200 || !strings.Contains(string(rz.Body), `"ready"`) {
		return "", fmt.Errorf("/readyz: %s (not ready)", rz.Brief())
	}
	return "/healthz ok, /readyz ready (process answers; this proves nothing about chat, the steps below do)", nil
}

func (s *scn) login(ctx context.Context, a *Actor) (string, error) {
	if a == nil {
		return "", Skipped("no credentials supplied for this account")
	}
	if a.Session == nil && a.Login != nil {
		sess, err := a.Login(ctx)
		if err != nil {
			return "", fmt.Errorf("login flow failed: %w", err)
		}
		a.Session = sess
	}
	if a.Session == nil || a.Session.Cookie == "" {
		return "", Skipped("no credentials supplied for this account")
	}
	v, resp, err := s.me(ctx, a)
	if err != nil {
		return "", err
	}
	if resp.Status != 200 {
		return "", fmt.Errorf("session refused by /api/me: %s (expired? sessions last 7 days; sign in again and copy a fresh cookie)", resp.Brief())
	}
	a.id = v.ID
	return fmt.Sprintf("signed in; account state %s", v.State), nil
}

func (s *scn) adminActive(ctx context.Context) (string, error) {
	a := s.cfg.Admin
	deadline := time.Now().Add(s.cfg.TenantTimeout)
	var last meView
	for {
		v, resp, err := s.me(ctx, a)
		if err != nil {
			return "", err
		}
		if resp.Status != 200 {
			return "", fmt.Errorf("/api/me: %s", resp.Brief())
		}
		last = v
		if v.Role != "ADMIN" {
			return "", errors.New("the admin session is not an administrator (role " + v.Role + ")")
		}
		if v.CanAdmin {
			return "admin API reachable", nil
		}
		if v.State == "ERROR" || time.Now().After(deadline) {
			return "", fmt.Errorf("administrator account is %s (approval %s, tenant %s): the admin API stays closed until the admin's own workspace is READY; the reason is in Antares' log and the provisioner's", v.State, v.ApprovalStatus, v.TenantStatus)
		}
		if !sleep(ctx, s.cfg.Poll) {
			return "", fmt.Errorf("gave up waiting; admin account is %s", last.State)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// refused expects the request to be turned away with one of the given
// "status CODE" pairs, e.g. "403 ACCOUNT_PENDING", and nothing else. Any success is
// reported as ALLOWED.
func refused(r *Response, err error, want ...string) error {
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%d %s", r.Status, r.ErrCode())
	for _, w := range want {
		if got == w {
			return nil
		}
	}
	if r.Status >= 200 && r.Status < 300 {
		return fmt.Errorf("was ALLOWED (%s), expected %s", r.Brief(), strings.Join(want, " or "))
	}
	return fmt.Errorf("answered %s, expected %s", r.Brief(), strings.Join(want, " or "))
}

func (s *scn) unapproved(ctx context.Context) (string, error) {
	p := s.cfg.Pending
	v, resp, err := s.me(ctx, p)
	if err != nil {
		return "", err
	}
	if resp.Status != 200 || v.State != "PENDING" {
		return "", fmt.Errorf("the 'pending' test account is %s, not PENDING: it must never be approved (state is judged by the server)", v.State)
	}
	probes := []struct {
		what, method, path string
		body               any
		hdr                map[string]string
	}{
		{"list chats", "GET", "/api/chats", nil, nil},
		{"create a chat", "POST", "/api/chats", map[string]string{}, nil},
		{"send a message", "POST", "/api/chats/00000000000000000000000000000000/messages", map[string]string{"input": "hi"}, map[string]string{"Idempotency-Key": "smoke-" + s.cfg.RunID + "-pending"}},
		{"open the admin API", "GET", "/api/admin/users", nil, nil},
	}
	for _, p2 := range probes {
		r, err := s.c.Do(ctx, p.Session, p2.method, p2.path, p2.hdr, p2.body)
		if e := refused(r, err, "403 ACCOUNT_PENDING"); e != nil {
			return "", fmt.Errorf("pending account tried to %s and %w", p2.what, e)
		}
	}
	return "pending account refused (403 ACCOUNT_PENDING) on chat list, create, send and admin", nil
}

func (s *scn) admin(ctx context.Context, method, path string, body any) (*Response, error) {
	return s.c.Do(ctx, s.cfg.Admin.Session, method, path, nil, body)
}

func (s *scn) adminUser(ctx context.Context, id string) (adminView, error) {
	r, err := s.admin(ctx, "GET", "/api/admin/users/"+id, nil)
	if err != nil {
		return adminView{}, err
	}
	var v adminView
	if r.Status != 200 || r.JSON(&v) != nil {
		return v, fmt.Errorf("admin read of account: %s", r.Brief())
	}
	return v, nil
}

func (s *scn) approve(ctx context.Context) (string, error) {
	var notes []string
	for _, a := range []*Actor{s.cfg.A, s.cfg.B} {
		v, resp, err := s.me(ctx, a)
		if err != nil {
			return "", err
		}
		if resp.Status != 200 {
			return "", fmt.Errorf("account %s: /api/me %s", a.Name, resp.Brief())
		}
		action := ""
		switch v.ApprovalStatus {
		case "APPROVED":
			notes = append(notes, a.Name+" already approved")
			continue
		case "PENDING":
			action = "approve"
		case "SUSPENDED":
			action = "reactivate"
		default:
			return "", fmt.Errorf("account %s is %s; a test account must be PENDING, APPROVED or SUSPENDED", a.Name, v.ApprovalStatus)
		}
		r, err := s.admin(ctx, "POST", "/api/admin/users/"+a.id+"/"+action, map[string]string{"reason": "smoke test " + s.cfg.RunID})
		if err != nil {
			return "", err
		}
		if r.Status != 200 {
			return "", fmt.Errorf("admin %s of %s: %s", action, a.Name, r.Brief())
		}
		notes = append(notes, a.Name+" "+action+"d")
	}
	return strings.Join(notes, ", "), nil
}

// waitReady polls the admin view until the tenant is READY. A tenant that stays
// FAILED past the grace period is a failure that names Antares' own reason, which
// is where a missing toolset policy or a broken credential shows up.
func (s *scn) waitReady(ctx context.Context, a *Actor) (adminView, time.Duration, error) {
	start := time.Now()
	deadline := start.Add(s.cfg.TenantTimeout)
	var failedSince time.Time
	var last adminView
	for {
		v, err := s.adminUser(ctx, a.id)
		if err != nil {
			return v, 0, err
		}
		last = v
		if v.TenantStatus == "READY" && v.Binding != nil && v.Binding.Status == "READY" {
			return v, time.Since(start), nil
		}
		if v.TenantStatus == "FAILED" {
			if failedSince.IsZero() {
				failedSince = time.Now()
			}
			if time.Since(failedSince) >= s.cfg.FailedGrace {
				return v, 0, failedErr(a, v)
			}
		} else {
			failedSince = time.Time{}
		}
		if time.Now().After(deadline) || !sleep(ctx, s.cfg.Poll) {
			return v, 0, fmt.Errorf("tenant %s not READY after %s: status %s (binding %s), detail %q", a.Name,
				time.Since(start).Round(time.Second), last.TenantStatus, bindingStatus(last), last.TenantDetail)
		}
	}
}

func bindingStatus(v adminView) string {
	if v.Binding == nil {
		return "none"
	}
	return v.Binding.Status
}

func failedErr(a *Actor, v adminView) error {
	d := v.TenantDetail
	if d == "" {
		d = "no detail recorded"
	}
	return fmt.Errorf("tenant %s is FAILED after %d attempt(s): %s", a.Name, v.TenantAttempts, d)
}

func (s *scn) tenantReady(ctx context.Context, a *Actor) (string, error) {
	_, took, err := s.waitReady(ctx, a)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("tenant READY with a READY binding after %s", took.Round(time.Millisecond)), nil
}

// reverify asks Antares to run its whole verification again, with the tenant's
// own credential, against the Hermes it is deployed with. The account goes back
// to CONFIGURING first, so READY afterwards can only mean the checks ran and passed
// now. Among them is toolset_policy: it reads the profile's effective toolsets and
// fails if anything boundary-crossing is on or anything outside the allow-list is.
func (s *scn) reverify(ctx context.Context, a *Actor) (string, error) {
	r, err := s.admin(ctx, "POST", "/api/admin/users/"+a.id+"/tenant-verify", map[string]string{})
	if err != nil {
		return "", err
	}
	if r.Status != http.StatusAccepted {
		return "", fmt.Errorf("tenant-verify for %s: %s", a.Name, r.Brief())
	}
	var v adminView
	if r.JSON(&v) != nil || v.TenantStatus != "CONFIGURING" {
		return "", fmt.Errorf("tenant-verify did not reset %s to CONFIGURING (got %q): a stale READY would pass without verifying anything", a.Name, v.TenantStatus)
	}
	v, took, err := s.waitReady(ctx, a)
	if err != nil {
		return "", err
	}
	if v.TenantDetail != "" || v.Binding == nil || v.Binding.VerifiedAt == "" {
		return "", fmt.Errorf("tenant %s is READY but carries detail %q or no verification time", a.Name, v.TenantDetail)
	}
	return fmt.Sprintf("READY only after Antares' six checks (endpoint, profile_identity, credential_isolation, session_ownership, toolset_policy, memory_boundary) passed again, in %s", took.Round(time.Millisecond)), nil
}

var terminalEvents = map[string]bool{"run.completed": true, "run.failed": true, "run.cancelled": true}

func (s *scn) chat(ctx context.Context, a *Actor) (string, error) {
	r, err := s.c.Do(ctx, a.Session, "POST", "/api/chats", nil, map[string]string{"title": "smoke " + s.cfg.RunID})
	if err != nil {
		return "", err
	}
	var chat struct {
		ID string `json:"id"`
	}
	if r.Status != 201 || r.JSON(&chat) != nil || chat.ID == "" {
		return "", fmt.Errorf("create chat: %s", r.Brief())
	}
	a.chatID = chat.ID
	r, err = s.c.Do(ctx, a.Session, "POST", "/api/chats/"+chat.ID+"/messages",
		map[string]string{"Idempotency-Key": "smoke-" + s.cfg.RunID + "-" + strings.ToLower(a.Name)}, map[string]string{"input": s.cfg.Prompt})
	if err != nil {
		return "", err
	}
	var run chatRun
	if r.Status != 202 || r.JSON(&run) != nil || run.RunID == "" {
		return "", fmt.Errorf("send message: %s", r.Brief())
	}
	a.runID = run.RunID

	rctx, cancel := context.WithTimeout(ctx, s.cfg.RunTimeout)
	defer cancel()
	start := time.Now()
	counts := map[string]int{}
	terminal := ""
	path := "/api/runs/" + run.RunID + "/events"
	status, err := s.c.Stream(rctx, a.Session, path, func(e SSEEvent) bool {
		counts[e.Name]++
		if terminalEvents[e.Name] {
			terminal = e.Name
			return true
		}
		return false
	})
	if err != nil && rctx.Err() == nil {
		return "", err
	}
	if err == nil && status != 200 {
		return "", fmt.Errorf("follow run: event stream %s", (&Response{Status: status}).Brief())
	}
	// The stream is a view; the run row is the authority. Poll it until it is terminal,
	// whether or not the stream said so (a dropped stream proves nothing).
	final := chatRun{}
	for {
		rr, gerr := s.c.Do(rctx, a.Session, "GET", "/api/runs/"+run.RunID, nil, nil)
		if gerr == nil && rr.Status == 200 && rr.JSON(&final) == nil && terminalStatus(final.Status) {
			break
		}
		if !sleep(rctx, s.cfg.Poll) {
			return "", fmt.Errorf("no terminal phase within %s: run is %q, events seen %s", s.cfg.RunTimeout, final.Status, eventSummary(counts))
		}
	}
	if final.Status != "COMPLETED" {
		return "", fmt.Errorf("run ended %s%s after %s (events %s): a failed run is a failure", final.Status, codeSuffix(final.ErrorCode), time.Since(start).Round(time.Millisecond), eventSummary(counts))
	}
	if terminal != "" && terminal != "run.completed" {
		return "", fmt.Errorf("stream ended with %s but the run row says COMPLETED", terminal)
	}
	reply, err := s.assistantReply(rctx, a)
	if err != nil {
		return "", err
	}
	a.reply = reply
	return fmt.Sprintf("run COMPLETED in %s, assistant replied (%d chars); events %s", time.Since(start).Round(time.Millisecond), len([]rune(reply)), eventSummary(counts)), nil
}

func terminalStatus(st string) bool {
	switch st {
	case "COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT":
		return true
	}
	return false
}

func codeSuffix(c string) string {
	if c == "" {
		return ""
	}
	return " (" + c + ")"
}

func eventSummary(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	var parts []string
	for _, k := range []string{"run.started", "tool.started", "assistant.delta", "tool.completed", "run.resumed", "run.completed", "run.failed", "run.cancelled", "run.disconnected"} {
		if n := m[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", k, n))
		}
	}
	return strings.Join(parts, " ")
}

// assistantReply reads the transcript and returns the assistant's non-empty reply
// that follows the prompt. Hermes may lag the run row by a moment, so it retries
// briefly before concluding there is no reply.
func (s *scn) assistantReply(ctx context.Context, a *Actor) (string, error) {
	for attempt := 0; ; attempt++ {
		r, err := s.c.Do(ctx, a.Session, "GET", "/api/chats/"+a.chatID+"/messages", nil, nil)
		if err != nil {
			return "", err
		}
		var out struct {
			Messages []struct {
				Role string `json:"role"`
				Text string `json:"text"`
			} `json:"messages"`
		}
		if r.Status != 200 || r.JSON(&out) != nil {
			return "", fmt.Errorf("read transcript: %s", r.Brief())
		}
		sawUser := false
		for _, m := range out.Messages {
			switch {
			case m.Role == "user" && strings.TrimSpace(m.Text) != "":
				sawUser = true
			case m.Role == "assistant" && sawUser && strings.TrimSpace(m.Text) != "":
				return m.Text, nil
			}
		}
		if attempt >= 5 || !sleep(ctx, s.cfg.Poll) {
			return "", fmt.Errorf("run COMPLETED but the transcript holds no non-empty assistant reply after the prompt (%d messages): an empty answer is a failure", len(out.Messages))
		}
	}
}

func (s *scn) crossUser(ctx context.Context) (string, error) {
	pairs := [][2]*Actor{{s.cfg.A, s.cfg.B}, {s.cfg.B, s.cfg.A}}
	checked := 0
	for _, p := range pairs {
		owner, intruder := p[0], p[1]
		// Control: the owner can read it. Without this a 404 below could just mean the
		// chat is gone or the service is down.
		own, err := s.c.Do(ctx, owner.Session, "GET", "/api/chats/"+owner.chatID, nil, nil)
		if err != nil {
			return "", err
		}
		if own.Status != 200 {
			return "", fmt.Errorf("control failed: %s cannot read their own chat (%s), so the cross-user result would mean nothing", owner.Name, own.Brief())
		}
		who := intruder.Name + " → " + owner.Name + "'s "
		probes := []struct {
			what, method, path string
			body               any
			hdr                map[string]string
		}{
			{"chat", "GET", "/api/chats/" + owner.chatID, nil, nil},
			{"messages", "GET", "/api/chats/" + owner.chatID + "/messages", nil, nil},
			{"send into chat", "POST", "/api/chats/" + owner.chatID + "/messages", map[string]string{"input": "hello from the wrong user"}, map[string]string{"Idempotency-Key": "smoke-" + s.cfg.RunID + "-x" + strings.ToLower(intruder.Name)}},
			{"run", "GET", "/api/runs/" + owner.runID, nil, nil},
			{"cancel run", "POST", "/api/runs/" + owner.runID + "/cancel", map[string]string{}, nil},
		}
		for _, pr := range probes {
			r, err := s.c.Do(ctx, intruder.Session, pr.method, pr.path, pr.hdr, pr.body)
			if err != nil {
				return "", err
			}
			if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
				return "", fmt.Errorf("ISOLATION BREACH: %s%s answered %s, expected 404 NOT_FOUND", who, pr.what, r.Brief())
			}
			if len(owner.reply) >= 8 && strings.Contains(string(r.Body), owner.reply) {
				return "", fmt.Errorf("ISOLATION BREACH: %s%s response carried the owner's reply text", who, pr.what)
			}
			checked++
		}
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		st, serr := s.c.Stream(sctx, intruder.Session, "/api/runs/"+owner.runID+"/events", func(SSEEvent) bool { return true })
		cancel()
		if serr != nil && st == 0 {
			return "", serr
		}
		if st != 404 {
			return "", fmt.Errorf("ISOLATION BREACH: %sevent stream answered HTTP %d, expected 404", who, st)
		}
		checked++
		list, err := s.c.Do(ctx, intruder.Session, "GET", "/api/chats?limit=100", nil, nil)
		if err != nil {
			return "", err
		}
		if list.Status != 200 || strings.Contains(string(list.Body), owner.chatID) {
			return "", fmt.Errorf("ISOLATION BREACH: %slist check: %s or the owner's chat id is listed", who, list.Brief())
		}
		checked++
	}
	return fmt.Sprintf("%d cross-user probes in both directions all refused (404 NOT_FOUND); each owner could read their own chat", checked), nil
}

func (s *scn) revoke(ctx context.Context) (string, error) {
	b, a := s.cfg.B, s.cfg.A
	r, err := s.admin(ctx, "POST", "/api/admin/users/"+b.id+"/suspend", map[string]string{"reason": "smoke test " + s.cfg.RunID})
	if err != nil {
		return "", err
	}
	if r.Status != 200 {
		return "", fmt.Errorf("admin suspend of B: %s", r.Brief())
	}
	s.cfg.suspendedB = true
	t0 := time.Now()
	// Antares revokes the account's sessions in the same transaction as the suspension, so
	// the next request is normally 401; a session that survived would meet the 403. Either
	// is "no access"; a 2xx is not.
	revoked := []string{"401 UNAUTHENTICATED", "403 ACCOUNT_SUSPENDED"}
	// No wait: the next request after the suspension is acknowledged must already be refused.
	probes := []struct {
		what, method, path string
		body               any
		hdr                map[string]string
	}{
		{"list chats", "GET", "/api/chats", nil, nil},
		{"create a chat", "POST", "/api/chats", map[string]string{}, nil},
		{"read own transcript", "GET", "/api/chats/" + b.chatID + "/messages", nil, nil},
		{"send a message", "POST", "/api/chats/" + b.chatID + "/messages", map[string]string{"input": "still there?"}, map[string]string{"Idempotency-Key": "smoke-" + s.cfg.RunID + "-revoked"}},
		{"read own run", "GET", "/api/runs/" + b.runID, nil, nil},
	}
	for _, p := range probes {
		resp, err := s.c.Do(ctx, b.Session, p.method, p.path, p.hdr, p.body)
		if e := refused(resp, err, revoked...); e != nil {
			return "", fmt.Errorf("revoked account tried to %s and %w", p.what, e)
		}
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	st, serr := s.c.Stream(sctx, b.Session, "/api/runs/"+b.runID+"/events", func(SSEEvent) bool { return true })
	cancel()
	if serr != nil && st == 0 {
		return "", serr
	}
	if st != 401 && st != 403 {
		return "", fmt.Errorf("revoked account opened its run event stream: HTTP %d, expected 401 or 403", st)
	}
	took := time.Since(t0).Round(time.Millisecond)
	// Control: revoking B must not have revoked A.
	ra, err := s.c.Do(ctx, a.Session, "GET", "/api/chats", nil, nil)
	if err != nil {
		return "", err
	}
	if ra.Status != 200 {
		return "", fmt.Errorf("suspending B also broke A: A's chat list answered %s", ra.Brief())
	}
	return fmt.Sprintf("B refused (401/403) on 6 routes within %s of the suspension, no waiting; A unaffected. B's session is now revoked: sign B in again before the next run", took), nil
}

func (s *scn) restore(ctx context.Context) (string, error) {
	if !s.cfg.suspendedB {
		return "", Skipped("nothing was suspended by this run")
	}
	b := s.cfg.B
	r, err := s.admin(ctx, "POST", "/api/admin/users/"+b.id+"/reactivate", map[string]string{"reason": "smoke test cleanup " + s.cfg.RunID})
	if err != nil {
		return "", err
	}
	if r.Status != 200 {
		return "", fmt.Errorf("reactivate B: %s", r.Brief())
	}
	if _, _, err := s.waitReady(ctx, b); err != nil {
		return "", fmt.Errorf("B was reactivated but did not return to READY: %w", err)
	}
	s.cfg.suspendedB = false
	return "B reactivated and READY again", nil
}
