// Package tenant runs the provisioning pipeline behind admin approval
// (PRD §5.3): claim an approved account, obtain a profile key from the host
// helper, seal and store it, verify the profile, and only then mark the tenant
// READY. Every step fails closed and records a short, secret-free reason.
package tenant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Defaults for the retry policy: bounded exponential backoff, then the account
// stays FAILED with its reason until an admin requeues it.
const (
	DefaultMaxAttempts = 5
	DefaultBaseBackoff = 15 * time.Second
	maxBackoff         = 10 * time.Minute
	// A run is a helper call (hermes.HelperTimeout) plus a verification
	// (hermes.MaxVerifyProbes requests of hermes.ProbeTimeout). obtainCredential
	// can make a second helper call, Rotate after ErrKeyExists; that 409 comes
	// from the helper's fast path (no key written), so the budget covers one call
	// plus probes, not two calls at their timeout. If both did run long, the run
	// is cut at runBudget and stays CONFIGURING. The lease outlives runBudget, so
	// a run still in flight is never mistaken for an interrupted one, and a cut
	// run is retried, not lost. TestRunBudgetCoversHelperAndProbes and
	// TestLeaseOutlastsRunBudget pin the order.
	lease        = 12 * time.Minute
	sweepEvery   = 10 * time.Second
	sweepBatch   = 20
	endpointID   = "hermes-primary"
	maxSiblings  = 3
	runBudget    = 11 * time.Minute
	scopePrefix  = "chatui:user:"
	slugRandomLn = 12 // bytes -> 24 hex chars, within the helper's 8..32
)

// Helper is the host-side provisioner as the pipeline uses it.
type Helper interface {
	Provision(ctx context.Context, slug string) (hermes.Credential, error)
	Rotate(ctx context.Context, slug string) (hermes.Credential, error)
}

// Verifier is the pre-READY check suite.
type Verifier interface {
	Verify(ctx context.Context, t hermes.Target) hermes.Report
}

// Options configures a Service.
type Options struct {
	DB          *sql.DB
	Box         *secrets.Box
	Helper      Helper
	Verifier    Verifier
	Now         func() time.Time
	MaxAttempts int
	BaseBackoff time.Duration
}

// Service owns provisioning runs. It is safe for concurrent use; a given user
// is processed by at most one goroutine at a time.
type Service struct {
	db       *sql.DB
	box      *secrets.Box
	helper   Helper
	verifier Verifier
	now      func() time.Time
	max      int
	base     time.Duration

	mu       sync.Mutex
	inflight map[string]bool
	wg       sync.WaitGroup
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Box == nil || o.Helper == nil || o.Verifier == nil {
		return nil, errors.New("tenant: database, secret box, helper and verifier are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = DefaultBaseBackoff
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{db: o.DB, box: o.Box, helper: o.Helper, verifier: o.Verifier, now: o.Now,
		max: o.MaxAttempts, base: o.BaseBackoff, inflight: map[string]bool{}, ctx: ctx, cancel: cancel}, nil
}

// Kick schedules a run for one user and returns immediately. It is idempotent:
// a run already in flight for the user absorbs the call, and a user with
// nothing to do is left alone, so duplicate admin clicks cannot provision twice.
func (s *Service) Kick(userID string) {
	s.mu.Lock()
	if s.closed || s.inflight[userID] {
		s.mu.Unlock()
		return
	}
	s.inflight[userID] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, userID)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.ctx, runBudget)
		defer cancel()
		if err := s.Process(ctx, userID); err != nil {
			log.Printf("tenant: run for %s ended: %v", userID, err)
		}
	}()
}

// Run sweeps for due accounts until ctx is done, then stops accepting work,
// cancels runs in flight and waits for them. Call it before closing the
// database. It is what resumes an interrupted run and what retries a failed one
// after its backoff.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	s.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			s.Close()
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

func (s *Service) sweep(ctx context.Context) {
	ids, err := store.DueTenants(ctx, s.db, s.now(), s.max, sweepBatch)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("tenant: sweep failed: %v", err)
		}
		return
	}
	for _, id := range ids {
		s.Kick(id)
	}
}

// Close refuses new work, cancels runs in flight and waits for them.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func newSlug() string {
	b := make([]byte, slugRandomLn)
	if _, err := rand.Read(b); err != nil {
		panic("tenant: crypto/rand failed: " + err.Error())
	}
	return "u_" + hex.EncodeToString(b)
}

func (s *Service) backoff(attempts int) time.Duration {
	d := s.base
	for i := 1; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// Process performs one run for one user, synchronously. It returns nil both
// for a completed run (READY or FAILED, recorded in the database) and for a
// user with nothing to do; an error means the run could not even record its
// outcome, and the lease will bring it back.
func (s *Service) Process(ctx context.Context, userID string) error {
	c, err := store.ClaimTenant(ctx, s.db, userID, newSlug(), s.now(), lease, s.max)
	if errors.Is(err, store.ErrNotClaimable) || errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if c.BindingID == "" {
		if err := s.obtainCredential(ctx, &c); err != nil {
			return s.finish(ctx, c, err)
		}
	}
	return s.finish(ctx, c, s.verify(ctx, c))
}

// outcome carries a failure's fixed detail and whether retrying can help.
type outcome struct {
	detail    string
	permanent bool
}

func (o *outcome) Error() string { return o.detail }

func fail(detail string, permanent bool) error { return &outcome{detail, permanent} }

// finish records the run's result. A nil err means verified: READY.
func (s *Service) finish(ctx context.Context, c store.Claim, err error) error {
	// Record the outcome even if the run's context ended (shutdown): the write is
	// short, and leaving CONFIGURING behind would only delay the retry to lease expiry.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	now := s.now()
	if err == nil {
		switch werr := store.MarkReady(wctx, s.db, c, now); {
		case werr == nil:
			return nil
		case errors.Is(werr, store.ErrSuperseded):
			return nil // suspended or rejected meanwhile: nothing may unlock
		default:
			return werr
		}
	}
	if errors.Is(err, store.ErrSuperseded) {
		return nil // the account moved on while the helper ran; the credential is stored DISABLED
	}
	var o *outcome
	if !errors.As(err, &o) {
		o = &outcome{detail: "provisioning failed", permanent: false}
	}
	if ctx.Err() != nil {
		return nil // shutdown or run budget: stay CONFIGURING, the lease resumes it
	}
	attempts := c.Attempts + 1
	if o.permanent {
		attempts = s.max // needs an operator; no automatic retry
	}
	switch werr := store.MarkFailed(wctx, s.db, c, o.detail, attempts, now.Add(s.backoff(attempts)), now); {
	case werr == nil, errors.Is(werr, store.ErrSuperseded):
		return nil
	default:
		return werr
	}
}

// obtainCredential asks the helper for the profile's key and stores it sealed.
func (s *Service) obtainCredential(ctx context.Context, c *store.Claim) error {
	cred, err := s.helper.Provision(ctx, c.Profile)
	if errors.Is(err, hermes.ErrKeyExists) {
		// The profile has a key we never stored: a previous response was lost.
		// The helper's documented recovery is a rotation, which issues a fresh key.
		cred, err = s.helper.Rotate(ctx, c.Profile)
	}
	if err != nil {
		return helperFailure(err)
	}
	id := store.NewID()
	sealed, err := s.box.Seal(cred.Key, id)
	if err != nil {
		return fail("could not seal the credential", true)
	}
	b := store.NewBinding{
		ID: id, Endpoint: endpointID, Ciphertext: sealed, KeyVersion: secrets.Version,
		Fingerprint: s.box.Fingerprint(cred.Key), MemoryScopeKey: scopePrefix + c.UserID,
	}
	if err := store.AttachBinding(ctx, s.db, *c, b, s.now()); err != nil {
		if errors.Is(err, store.ErrSuperseded) {
			return err // finish() treats a superseded run as done
		}
		// The key exists on the host but could not be stored (for example a
		// duplicate credential). A retry rotates it.
		return fail("could not store the credential", false)
	}
	c.BindingID = id
	return nil
}

func helperFailure(err error) error {
	var f *hermes.Failure
	if errors.As(err, &f) {
		return fail(f.Detail, errors.Is(f.Class, hermes.ErrPermanent))
	}
	return fail("provisioning helper failed", false)
}

// verify checks the stored binding against Hermes and, if it passes, returns nil.
func (s *Service) verify(ctx context.Context, c store.Claim) error {
	cred, err := store.BindingCredential(ctx, s.db, c.UserID)
	if err != nil {
		return fail("tenant binding is missing", false)
	}
	key, err := s.box.Open(cred.Ciphertext, cred.BindingID)
	if err != nil {
		return fail("stored credential cannot be opened", true)
	}
	sib, err := store.OtherReadyProfiles(ctx, s.db, c.UserID, maxSiblings)
	if err != nil {
		return fail("could not list other tenants for isolation checks", false)
	}
	rep := s.verifier.Verify(ctx, hermes.Target{Profile: cred.Profile, Key: key, Siblings: sib, Fresh: !cred.Verified})
	if !rep.OK() {
		d := rep.Detail()
		if d == "" {
			d = "verification did not complete"
		}
		return fail(d, false)
	}
	return nil
}
