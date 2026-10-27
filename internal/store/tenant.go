package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"
	"unicode"

	"modernc.org/sqlite"
)

// Errors the admin and provisioning layers translate into API answers.
var (
	ErrNotFound      = errors.New("store: no such user")
	ErrConflict      = errors.New("store: action not allowed from the current state")
	ErrSelf          = errors.New("store: an administrator cannot act on their own account")
	ErrNotAdmin      = errors.New("store: actor is not an active administrator")
	ErrReason        = errors.New("store: a reason is required")
	ErrReasonInvalid = errors.New("store: the reason is too long or contains control characters")
	ErrNoBinding     = errors.New("store: user has no tenant binding")
	ErrSuperseded    = errors.New("store: user changed state while the run was in flight")
	ErrNotClaimable  = errors.New("store: nothing to provision for this user")
)

const maxReason = 500

// Admin actions on an account (PRD §3.3, AUTH-005).
const (
	ActionApprove    = "approve"
	ActionReject     = "reject"
	ActionSuspend    = "suspend"
	ActionReactivate = "reactivate"
)

var transitions = map[string]struct {
	from     []string
	to       string
	needsWhy bool
}{
	ActionApprove:    {[]string{"PENDING", "REJECTED"}, "APPROVED", false},
	ActionReject:     {[]string{"PENDING"}, "REJECTED", true},
	ActionSuspend:    {[]string{"APPROVED"}, "SUSPENDED", true},
	ActionReactivate: {[]string{"SUSPENDED"}, "APPROVED", false},
}

// AdminUser is what an administrator may see of an account: identity metadata
// and status. There is deliberately no profile name, credential or any content
// field, so no read model built from it can leak one (invariant 6).
type AdminUser struct {
	ID                string
	Email             string
	DisplayName       string
	Role              string
	ApprovalStatus    string
	TenantStatus      string
	TenantDetail      string
	TenantAttempts    int
	TenantCheckedAt   string
	CreatedAt         string
	ApprovedAt        string
	LastLoginAt       string
	BindingStatus     string // empty when no binding exists
	BindingVerifiedAt string
}

const adminCols = `u.id, u.email, COALESCE(u.display_name,''), u.role, u.approval_status, u.tenant_status,
	COALESCE(u.tenant_detail,''), u.tenant_attempts, COALESCE(u.tenant_checked_at,''), u.created_at,
	COALESCE(u.approved_at,''), COALESCE(u.last_login_at,''),
	COALESCE(b.status,''), COALESCE(b.verified_at,'')`

const adminFrom = ` FROM users u LEFT JOIN tenant_bindings b ON b.user_id = u.id`

func scanAdmin(row interface{ Scan(...any) error }) (AdminUser, error) {
	var a AdminUser
	err := row.Scan(&a.ID, &a.Email, &a.DisplayName, &a.Role, &a.ApprovalStatus, &a.TenantStatus,
		&a.TenantDetail, &a.TenantAttempts, &a.TenantCheckedAt, &a.CreatedAt,
		&a.ApprovedAt, &a.LastLoginAt, &a.BindingStatus, &a.BindingVerifiedAt)
	return a, err
}

// SQLite's built-in LOWER() folds ASCII only, while strings.ToLower folds all of Unicode, so
// lowering the term in Go and the column in SQL disagreed for names like "Ünal". ulower is the
// same Go fold, applied to the column, so both sides of the comparison fold identically.
// (LIKE itself is only ASCII case-insensitive, which is fine: both operands are already lowered.)
func init() {
	sqlite.RegisterDeterministicScalarFunction("ulower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return strings.ToLower(v), nil
		case []byte:
			return strings.ToLower(string(v)), nil
		}
		return args[0], nil
	})
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ListAdminUsers returns accounts oldest request first, optionally filtered by
// approval status. limit is clamped to 1..200.
func ListAdminUsers(ctx context.Context, db *sql.DB, approval, search string, limit, offset int) ([]AdminUser, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + adminCols + adminFrom
	var args []any
	var where []string
	if approval != "" {
		where = append(where, `u.approval_status = ?`)
		args = append(args, approval)
	}
	if search != "" {
		// A substring match on identity metadata only; %, _ and the escape itself are literals.
		like := "%" + likeEscaper.Replace(strings.ToLower(search)) + "%"
		where = append(where, `(ulower(u.email) LIKE ? ESCAPE '\' OR ulower(COALESCE(u.display_name,'')) LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY u.created_at, u.id LIMIT ? OFFSET ?`
	rows, err := db.QueryContext(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAdminUser returns one account's admin view, or ErrNotFound.
func GetAdminUser(ctx context.Context, db *sql.DB, id string) (AdminUser, error) {
	a, err := scanAdmin(db.QueryRowContext(ctx, `SELECT `+adminCols+adminFrom+` WHERE u.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	return a, err
}

// cleanReason trims and validates an admin-supplied reason. It is free text
// shown to other admins, so control characters are refused outright.
func cleanReason(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > maxReason {
		return "", ErrReasonInvalid
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", ErrReasonInvalid
		}
	}
	return s, nil
}

// requireAdmin proves, inside the transaction, that the actor is an approved
// administrator. The HTTP guard already checked; the data layer does not rely
// on it (invariant 4).
func requireAdmin(ctx context.Context, tx *sql.Tx, actor string) error {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ? AND role = 'ADMIN' AND approval_status = 'APPROVED'`, actor).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotAdmin
	}
	return nil
}

func audit(ctx context.Context, tx *sql.Tx, actor, action, target, outcome, reqID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log
		(id, actor_user_id, action, target_kind, target_id, outcome, request_id, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		NewID(), nullIfEmpty(actor), action, "user", target, outcome, nullIfEmpty(reqID), ts(now))
	return err
}

// Decide applies one admin action to one account.
//
// The status change, the approvals history row, the audit row and (for reject
// and suspend) the revocation of the target's sessions commit together or not
// at all: an unaudited approval, or a suspension that left a live session, is
// not a state the system can reach. The UPDATE is conditional on the status it
// read, inside an IMMEDIATE transaction, so two admins acting at once produce
// one winner and one ErrConflict, never a lost update (PRD §3.5).
//
// Suspension also disables the tenant binding: the credential is kept so a
// reactivation can re-verify it, but nothing may use it meanwhile.
func Decide(ctx context.Context, db *sql.DB, actor, target, action, reason, reqID string, now time.Time) (AdminUser, error) {
	t, ok := transitions[action]
	if !ok {
		return AdminUser{}, ErrConflict
	}
	reason, err := cleanReason(reason)
	if err != nil {
		return AdminUser{}, err
	}
	if t.needsWhy && reason == "" {
		return AdminUser{}, ErrReason
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, err
	}
	defer tx.Rollback()
	if err := requireAdmin(ctx, tx, actor); err != nil {
		return AdminUser{}, err
	}
	if actor == target {
		return AdminUser{}, ErrSelf
	}

	var from, tenant string
	var hasBinding bool
	err = tx.QueryRowContext(ctx, `SELECT u.approval_status, u.tenant_status,
		EXISTS (SELECT 1 FROM tenant_bindings b WHERE b.user_id = u.id) FROM users u WHERE u.id = ?`, target).Scan(&from, &tenant, &hasBinding)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	if err != nil {
		return AdminUser{}, err
	}
	allowed := false
	for _, f := range t.from {
		allowed = allowed || f == from
	}
	if !allowed {
		return AdminUser{}, ErrConflict
	}

	var res sql.Result
	switch action {
	case ActionApprove, ActionReactivate:
		// Access still waits for a verified binding (AUTH-006): an existing one
		// is re-verified, a missing one is provisioned.
		next := "UNPROVISIONED"
		if hasBinding {
			next = "CONFIGURING"
		}
		res, err = tx.ExecContext(ctx, `UPDATE users SET approval_status = ?, tenant_status = ?, tenant_detail = NULL,
			tenant_attempts = 0, tenant_next_attempt_at = NULL, approved_at = ?, approved_by = ?
			WHERE id = ? AND approval_status = ? AND tenant_detail IS NOT ?`, t.to, next, ts(now), actor, target, from, JobShutdownPending)
		// A suspended account whose scheduled jobs are not yet verified paused is not reactivated (ErrConflict): the
		// retry loop only reconciles suspended accounts, so reactivating would abandon jobs that may still run.
	case ActionSuspend:
		// The pending job shutdown is recorded with the suspension itself (PRD §12 rule 3): it is cleared only once
		// the account's scheduled jobs are verified paused, so a restart in between still leaves it to the retry loop.
		res, err = tx.ExecContext(ctx, `UPDATE users SET approval_status = 'SUSPENDED', tenant_status = 'DISABLED',
			tenant_detail = ?, tenant_next_attempt_at = NULL WHERE id = ? AND approval_status = ?`, JobShutdownPending, target, from)
	default: // reject
		res, err = tx.ExecContext(ctx, `UPDATE users SET approval_status = 'REJECTED', tenant_detail = NULL
			WHERE id = ? AND approval_status = ?`, target, from)
	}
	if err != nil {
		return AdminUser{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return AdminUser{}, ErrConflict
	}
	switch action {
	case ActionSuspend:
		if _, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'DISABLED' WHERE user_id = ?`, target); err != nil {
			return AdminUser{}, err
		}
	case ActionApprove, ActionReactivate:
		// Back to CONFIGURING, never READY: the credential is verified again first.
		if _, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'CONFIGURING' WHERE user_id = ?`, target); err != nil {
			return AdminUser{}, err
		}
	}
	if action == ActionSuspend || action == ActionReject {
		if _, err := tx.ExecContext(ctx, `UPDATE app_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, ts(now), target); err != nil {
			return AdminUser{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO approvals (id, target_user_id, actor_user_id, action, from_status, to_status, reason, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, NewID(), target, actor, action, from, t.to, nullIfEmpty(reason), ts(now)); err != nil {
		return AdminUser{}, err
	}
	if err := audit(ctx, tx, actor, "admin."+action, target, "ok", reqID, now); err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, err
	}
	return GetAdminUser(ctx, db, target)
}

// Requeue modes for RequeueTenant.
const (
	RequeueProvision = "tenant-binding" // (re)run provisioning, e.g. after FAILED
	RequeueVerify    = "tenant-verify"  // reapply policy and re-verify an existing binding
)

// RequeueTenant puts an approved account back in the provisioning queue with a
// fresh attempt budget. RequeueVerify needs an existing binding. It never
// touches an account that is not APPROVED: a rejected or suspended user cannot
// be provisioned by this route.
func RequeueTenant(ctx context.Context, db *sql.DB, actor, target, mode, reqID string, now time.Time) (AdminUser, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, err
	}
	defer tx.Rollback()
	if err := requireAdmin(ctx, tx, actor); err != nil {
		return AdminUser{}, err
	}
	var approval, tenant string
	var hasBinding bool
	err = tx.QueryRowContext(ctx, `SELECT u.approval_status, u.tenant_status,
		EXISTS (SELECT 1 FROM tenant_bindings b WHERE b.user_id = u.id) FROM users u WHERE u.id = ?`, target).Scan(&approval, &tenant, &hasBinding)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	if err != nil {
		return AdminUser{}, err
	}
	if approval != "APPROVED" {
		return AdminUser{}, ErrConflict
	}
	if mode == RequeueVerify && !hasBinding {
		return AdminUser{}, ErrNoBinding
	}
	if tenant == "DISABLED" {
		return AdminUser{}, ErrConflict // only a reactivation re-enables a disabled tenant
	}
	next := "UNPROVISIONED"
	if hasBinding {
		next = "CONFIGURING"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET tenant_status = ?, tenant_detail = NULL, tenant_attempts = 0,
		tenant_next_attempt_at = NULL WHERE id = ? AND approval_status = 'APPROVED'`, next, target); err != nil {
		return AdminUser{}, err
	}
	if hasBinding {
		if _, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'CONFIGURING' WHERE user_id = ? AND status IN ('READY','FAILED','CONFIGURING')`, target); err != nil {
			return AdminUser{}, err
		}
	}
	if err := audit(ctx, tx, actor, "admin."+mode, target, "ok", reqID, now); err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, err
	}
	return GetAdminUser(ctx, db, target)
}

// ---- the provisioning pipeline --------------------------------------------

// Claim is one provisioning run's authority over one account.
type Claim struct {
	UserID    string
	Profile   string // the server-chosen slug, stable across retries
	BindingID string // empty until a credential has been stored
	Attempts  int
}

// DueTenants lists approved accounts whose provisioning should run now:
// never started, requeued, a lease that expired (an interrupted run), or a
// FAILED run whose backoff elapsed and whose attempt budget is not spent.
func DueTenants(ctx context.Context, db *sql.DB, now time.Time, maxAttempts, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM users WHERE approval_status = 'APPROVED' AND (
		tenant_status = 'UNPROVISIONED'
		OR (tenant_status = 'CONFIGURING' AND (tenant_next_attempt_at IS NULL OR tenant_next_attempt_at <= ?))
		OR (tenant_status = 'FAILED' AND tenant_attempts < ? AND tenant_next_attempt_at IS NOT NULL AND tenant_next_attempt_at <= ?))
		ORDER BY created_at, id LIMIT ?`, ts(now), maxAttempts, ts(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ClaimTenant moves one due account to CONFIGURING under a lease and returns
// its run. newProfile is used only if the account has no slug yet; once chosen
// the slug never changes. Two claimers cannot both win: the UPDATE is
// conditional on the state it read.
func ClaimTenant(ctx context.Context, db *sql.DB, userID, newProfile string, now time.Time, lease time.Duration, maxAttempts int) (Claim, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer tx.Rollback()
	var approval, tenant, next string
	var profile sql.NullString
	var attempts int
	var nextAt sql.NullString
	var bindingID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT u.approval_status, u.tenant_status, u.tenant_profile, u.tenant_attempts, u.tenant_next_attempt_at,
		(SELECT b.id FROM tenant_bindings b WHERE b.user_id = u.id) FROM users u WHERE u.id = ?`, userID).
		Scan(&approval, &tenant, &profile, &attempts, &nextAt, &bindingID)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	if err != nil {
		return Claim{}, err
	}
	due := false
	switch tenant {
	case "UNPROVISIONED":
		due = true
	case "CONFIGURING":
		due = !nextAt.Valid || nextAt.String <= ts(now)
	case "FAILED":
		due = attempts < maxAttempts && nextAt.Valid && nextAt.String <= ts(now)
	}
	if approval != "APPROVED" || !due {
		return Claim{}, ErrNotClaimable
	}
	if !profile.Valid {
		profile = sql.NullString{String: newProfile, Valid: true}
	}
	next = ts(now.Add(lease))
	res, err := tx.ExecContext(ctx, `UPDATE users SET tenant_status = 'CONFIGURING', tenant_profile = ?, tenant_next_attempt_at = ?
		WHERE id = ? AND approval_status = 'APPROVED' AND tenant_status = ?`, profile.String, next, userID, tenant)
	if err != nil {
		return Claim{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return Claim{}, ErrNotClaimable
	}
	if bindingID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'CONFIGURING' WHERE id = ? AND status IN ('FAILED','CONFIGURING')`, bindingID.String); err != nil {
			return Claim{}, err
		}
	}
	if err := audit(ctx, tx, "", "tenant.provision", userID, "started", "", now); err != nil {
		return Claim{}, err
	}
	if err := tx.Commit(); err != nil {
		return Claim{}, err
	}
	return Claim{UserID: userID, Profile: profile.String, BindingID: bindingID.String, Attempts: attempts}, nil
}

// NewBinding is the credential a provisioning run obtained, already sealed.
type NewBinding struct {
	ID             string // generated by the caller: it is the sealing AAD
	Endpoint       string
	Ciphertext     []byte
	KeyVersion     int
	Fingerprint    string
	MemoryScopeKey string
}

// AttachBinding stores the credential against the account. If the account was
// suspended or rejected while the helper ran, the credential is still stored
// (the helper already created it) but DISABLED, and ErrSuperseded is returned
// so the run stops: nothing becomes usable.
func AttachBinding(ctx context.Context, db *sql.DB, c Claim, b NewBinding, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var approval, tenant string
	var profile sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT approval_status, tenant_status, tenant_profile FROM users WHERE id = ?`, c.UserID).
		Scan(&approval, &tenant, &profile); err != nil {
		return err
	}
	live := approval == "APPROVED" && tenant == "CONFIGURING" && profile.String == c.Profile
	status := "CONFIGURING"
	if !live {
		status = "DISABLED"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tenant_bindings (id, user_id, profile_name, routing_mode, upstream_endpoint_id,
		credential_ciphertext, credential_key_version, credential_fingerprint, memory_scope_key, status, created_at)
		VALUES (?,?,?,'MUX_PREFIX',?,?,?,?,?,?,?)`,
		b.ID, c.UserID, c.Profile, b.Endpoint, b.Ciphertext, b.KeyVersion, b.Fingerprint, b.MemoryScopeKey, status, ts(now)); err != nil {
		return err
	}
	if err := audit(ctx, tx, "", "tenant.bind", c.UserID, map[bool]string{true: "ok", false: "superseded"}[live], "", now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !live {
		return ErrSuperseded
	}
	return nil
}

// ReplaceBindingCredential replaces only the claimed account's configuring binding.
func ReplaceBindingCredential(ctx context.Context, db *sql.DB, c Claim, b NewBinding, now time.Time) error {
	if b.ID == "" || b.ID != c.BindingID {
		return ErrConflict
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET credential_ciphertext = ?, credential_key_version = ?,
		credential_fingerprint = ? WHERE id = ? AND user_id = ? AND profile_name = ? AND status = 'CONFIGURING'
		AND EXISTS (SELECT 1 FROM users WHERE id = ? AND approval_status = 'APPROVED' AND tenant_status = 'CONFIGURING'
		AND tenant_profile = ?)`,
		b.Ciphertext, b.KeyVersion, b.Fingerprint, c.BindingID, c.UserID, c.Profile, c.UserID, c.Profile)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrSuperseded
	}
	if err := audit(ctx, tx, "", "tenant.rotate", c.UserID, "ok", "", now); err != nil {
		return err
	}
	return tx.Commit()
}

// SealedCredential is the stored key material for a user's binding. It is read
// by the provisioning pipeline only and never reaches a read model.
type SealedCredential struct {
	BindingID  string
	Profile    string
	Ciphertext []byte
	KeyVersion int
	Verified   bool // the binding has passed verification at least once
}

// BindingCredential returns the account's sealed credential, or ErrNoBinding.
func BindingCredential(ctx context.Context, db *sql.DB, userID string) (SealedCredential, error) {
	var s SealedCredential
	err := db.QueryRowContext(ctx, `SELECT id, profile_name, credential_ciphertext, credential_key_version, verified_at IS NOT NULL
		FROM tenant_bindings WHERE user_id = ?`, userID).Scan(&s.BindingID, &s.Profile, &s.Ciphertext, &s.KeyVersion, &s.Verified)
	if errors.Is(err, sql.ErrNoRows) {
		return SealedCredential{}, ErrNoBinding
	}
	return s, err
}

// OtherReadyProfiles returns up to limit profile names of READY bindings that
// belong to someone else. Verification uses them as the targets of cross-user
// negative probes; only names are read, never another user's credential.
func OtherReadyProfiles(ctx context.Context, db *sql.DB, userID string, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT profile_name FROM tenant_bindings WHERE user_id <> ? AND status = 'READY'
		ORDER BY created_at, id LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkReady is the only path to READY. It is conditional on the account still
// being APPROVED and CONFIGURING under this run's profile, so a verification
// that finished after a suspension cannot unlock anything.
func MarkReady(ctx context.Context, db *sql.DB, c Claim, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET tenant_status = 'READY', tenant_detail = NULL, tenant_attempts = 0,
		tenant_next_attempt_at = NULL, tenant_checked_at = ?
		WHERE id = ? AND approval_status = 'APPROVED' AND tenant_status = 'CONFIGURING' AND tenant_profile = ?`,
		ts(now), c.UserID, c.Profile)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrSuperseded
	}
	res, err = tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'READY', verified_at = ?
		WHERE user_id = ? AND profile_name = ? AND status = 'CONFIGURING'`, ts(now), c.UserID, c.Profile)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrSuperseded // rolls the user update back too
	}
	if err := audit(ctx, tx, "", "tenant.ready", c.UserID, "ok", "", now); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkFailed records a failed run with its fixed-vocabulary detail, the new
// attempt total and next attempt time (zero stops retries and audits attention).
// ErrSuperseded leaves the account unchanged if it is no longer CONFIGURING
// under this run (for example, after suspension).
func MarkFailed(ctx context.Context, db *sql.DB, c Claim, detail string, attempts int, nextAt time.Time, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	next := ""
	if !nextAt.IsZero() {
		next = ts(nextAt)
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET tenant_status = 'FAILED', tenant_detail = ?, tenant_attempts = ?,
		tenant_next_attempt_at = ?, tenant_checked_at = ?
		WHERE id = ? AND approval_status = 'APPROVED' AND tenant_status = 'CONFIGURING' AND tenant_profile = ?`,
		detail, attempts, nullIfEmpty(next), ts(now), c.UserID, c.Profile)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrSuperseded
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'FAILED' WHERE user_id = ? AND status = 'CONFIGURING'`, c.UserID); err != nil {
		return err
	}
	if nextAt.IsZero() {
		if err := audit(ctx, tx, "", "tenant.attention", c.UserID, detail, "", now); err != nil {
			return err
		}
	}
	if err := audit(ctx, tx, "", "tenant.failed", c.UserID, "failed:"+detail, "", now); err != nil {
		return err
	}
	return tx.Commit()
}

// AuditEntry is one audit_log row as the admin feed shows it.
type AuditEntry struct {
	ID, Actor, Action, TargetKind, TargetID, Outcome, RequestID, CreatedAt string
}

// ListAudit returns the audit feed newest first. It carries no content: only
// who did what to which account and with what outcome.
//
// Paging is by keyset, not offset: the feed is written to while an admin reads
// it, and an offset into a newest-first list shifts with every new row, so a
// page would repeat entries already seen and hide others. A page starts strictly
// after (beforeAt, beforeID) in the feed's own order; both empty means the
// newest page. more reports whether older entries exist.
func ListAudit(ctx context.Context, db *sql.DB, limit int, beforeAt, beforeID string) (out []AuditEntry, more bool, err error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, COALESCE(actor_user_id,''), action, COALESCE(target_kind,''), COALESCE(target_id,''),
		outcome, COALESCE(request_id,''), created_at FROM audit_log`
	var args []any
	if beforeAt != "" || beforeID != "" {
		q += ` WHERE (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, beforeAt, beforeAt, beforeID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, append(args, limit+1)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.TargetKind, &e.TargetID, &e.Outcome, &e.RequestID, &e.CreatedAt); err != nil {
			return nil, false, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		out, more = out[:limit], true
	}
	return out, more, nil
}

// SyntheticIssuer is the issuer of accounts minted by scripts/smoke_fixtures.py: never a real Google sign-in.
const SyntheticIssuer = "https://synthetic-test.invalid/antares"

// IsSyntheticProfile reports whether a Hermes profile belongs to a synthetic smoke account. Only those are ever
// recorded by the wire fixture capture (#169).
func IsSyntheticProfile(ctx context.Context, db *sql.DB, profile string) (bool, error) {
	if profile == "" {
		return false, nil
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE tenant_profile = ? AND google_issuer = ?`, profile, SyntheticIssuer).Scan(&n)
	return n > 0, err
}
