// Package chat is the run lifecycle between the browser and Hermes: create,
// stream, cancel, reconnect (PRD §6.2, §6.3, §9). It owns no model logic. Every
// Hermes call passes through gate, and the profile and key it uses are derived
// from the account in the database, never from the request.
package chat

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/store"
)

const (
	maxActiveRunsPerUser = 4
	reattachTries        = 3
	reconcileTimeout     = 20 * time.Second
)

// Options configures Service. All fields except Now and Backoff are required.
type Options struct {
	DB     *sql.DB
	Box    *secrets.Box
	Hermes *hermes.Client
	Now    func() time.Time
	// Backoff is the wait before re-subscribing to a dropped stream; tests
	// shorten it.
	Backoff func(attempt int) time.Duration
	// Heartbeat is the SSE keep-alive interval, which is also how often an open
	// stream re-checks that the account is still eligible. Default 15s.
	Heartbeat time.Duration
	// Notifier, if set, is told when a run reaches a terminal status (UXP-002).
	Notifier Notifier
}

// Notifier receives the stored terminal status of a run. It gets identifiers and
// the status only — never a title, prompt or output — and must not block.
type Notifier interface {
	RunFinished(userID, runID, chatID, status string)
}

// Service runs chats. Workers are detached from HTTP requests: a closed
// browser tab does not stop a run, and a cancel does.
type Service struct {
	db        *sql.DB
	box       *secrets.Box
	hx        *hermes.Client
	now       func() time.Time
	backoff   func(int) time.Duration
	heartbeat time.Duration
	notifier  Notifier

	base context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
	mu   sync.Mutex
	live map[string]*liveRun
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Box == nil || o.Hermes == nil {
		return nil, errors.New("chat: database, secret box and Hermes client are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Backoff == nil {
		o.Backoff = func(n int) time.Duration { return time.Duration(1<<n) * time.Second }
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 15 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{db: o.DB, box: o.Box, hx: o.Hermes, now: o.Now, backoff: o.Backoff, heartbeat: o.Heartbeat, notifier: o.Notifier,
		base: ctx, stop: cancel, live: map[string]*liveRun{}}, nil
}

// Close stops every worker and waits for them. Runs left in flight are
// DISCONNECTED on the next start (store.RecoverRuns), not completed.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

// gate is the default-deny check for a Hermes call (security-invariants §1):
// the account must be APPROVED with a READY, verified binding right now, in the
// database, with no cache. The session check happened in the HTTP guard; a
// background worker repeats the part that can change under it. A "no" is
// TENANT_NOT_READY and no request is made.
func (s *Service) gate(ctx context.Context, userID string) (hermes.Binding, string, error) {
	cred, err := store.ActiveBinding(ctx, s.db, userID)
	if err != nil {
		if errors.Is(err, store.ErrGate) {
			return hermes.Binding{}, "", &hermes.Error{Code: hermes.CodeTenantNotReady}
		}
		return hermes.Binding{}, "", err
	}
	key, err := s.box.Open(cred.Ciphertext, cred.BindingID)
	if err != nil {
		return hermes.Binding{}, "", &hermes.Error{Code: hermes.CodeTenantNotReady}
	}
	return hermes.Binding{Profile: cred.Profile, Key: key}, cred.BindingID, nil
}

func (s *Service) liveFor(runID string) *liveRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, l := range s.live { // sweep finished runs past their replay window
		l.mu.Lock()
		old := l.done && time.Since(l.doneAt) > retainFinished
		l.mu.Unlock()
		if old {
			delete(s.live, id)
		}
	}
	return s.live[runID]
}

func (s *Service) register(l *liveRun) {
	s.mu.Lock()
	s.live[l.runID] = l
	s.mu.Unlock()
}

// session key stable per account (PRD §6.2 step 4); derived here, never read
// from a request.
func sessionKey(userID string) string { return "chatui:user:" + userID }

// start launches the worker for a freshly admitted run.
func (s *Service) start(run store.Run, input string) *liveRun {
	l := newLive(run.ID, run.UserID, run.ChatID)
	ctx, cancel := context.WithCancel(s.base)
	l.cancelWorker = cancel
	s.register(l)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.runNew(ctx, l, run, input)
	}()
	return l
}

func (s *Service) runNew(ctx context.Context, l *liveRun, run store.Run, input string) {
	b, _, err := s.gate(ctx, run.UserID)
	if err != nil {
		s.finish(l, store.RunFailed, hermes.CodeOf(err), "")
		return
	}
	hid, err := s.hx.CreateRun(ctx, b, hermes.RunRequest{
		SessionID: run.HermesSession, Input: input, SessionKey: sessionKey(run.UserID), IdempotencyKey: "antares-" + run.ID,
	})
	if err != nil {
		if ctx.Err() != nil { // cancelled while admitting
			s.finish(l, store.RunCancelled, "", "")
			return
		}
		code := hermes.CodeOf(err)
		if code == hermes.CodeUpstreamTimeout {
			// Hermes may have started it: not "failed to start", and not safe to replay.
			s.finish(l, store.RunTimedOut, code, "")
			return
		}
		s.finish(l, store.RunFailed, code, "")
		return
	}
	moved, err := store.MoveRun(s.base, s.db, run.UserID, run.ID, []string{store.RunPending}, store.RunRunning, hid, "", s.now())
	if err != nil || !moved {
		// Cancelled (or otherwise ended) while Hermes was accepting it: make sure
		// the upstream run does not outlive the decision.
		s.stopUpstream(run.UserID, hid)
		s.finish(l, "", "", "")
		return
	}
	l.emit("run.started", map[string]string{"runId": run.ID, "chatId": run.ChatID}, false)
	s.follow(ctx, l, run.UserID, run.ID, b, hid)
}

// follow relays Hermes' event stream into the journal until the run ends, the
// stream breaks for good, or the run is cancelled. A stream that ends without
// a terminal frame proves nothing: the run's state is read back from Hermes.
func (s *Service) follow(ctx context.Context, l *liveRun, userID, runID string, b hermes.Binding, hid string) {
	var terminal *hermes.Normalized
	needsApproval := false
	for attempt := 0; ; attempt++ {
		err := s.hx.StreamEvents(ctx, b, hid, func(ev hermes.Event) error {
			n := hermes.Normalize(ev)
			switch n.Kind {
			case hermes.KindDelta:
				l.emit("assistant.delta", map[string]string{"text": n.Text}, false)
			case hermes.KindToolStarted:
				l.emit("tool.started", map[string]string{"toolCallId": n.ToolID, "label": n.Label}, false)
			case hermes.KindToolCompleted:
				l.emit("tool.completed", map[string]string{"toolCallId": n.ToolID, "summary": n.Summary}, false)
			case hermes.KindApproval:
				needsApproval = true
				return errStop
			case hermes.KindTerminal:
				t := n
				terminal = &t
				return errStop
			}
			return nil
		})
		switch {
		case ctx.Err() != nil || l.wasCancelled():
			return // the cancel path owns the outcome
		case needsApproval:
			// Hermes is waiting for a consent this build has no way to collect. Nothing
			// is approved or denied on the user's behalf: the run is stopped and ends
			// failed, visibly.
			s.stopUpstream(userID, hid)
			s.finish(l, store.RunFailed, hermes.CodeCapabilityNA, "")
			return
		case terminal != nil:
			s.finishFromUpstream(l, hermes.RunStatus{State: terminal.State, Output: terminal.Output, Interrupted: terminal.Intr})
			return
		}
		_ = err // the code is not needed: the state is read back below
		st, rerr := s.reconcile(ctx, userID, hid)
		switch {
		case rerr == nil && st.Terminal():
			s.finishFromUpstream(l, st)
			return
		case ctx.Err() != nil:
			return
		case errors.Is(rerr, hermes.ErrRunGone):
			s.finish(l, store.RunFailed, hermes.CodeHermesUnavailable, "") // Hermes no longer knows the run
			return
		case attempt < reattachTries-1 && rerr == nil && st.State == hermes.StateRunning:
			l.emit("run.resumed", map[string]any{"runId": runID, "replace": true}, false)
			select {
			case <-time.After(s.backoff(attempt)):
			case <-ctx.Done():
				return
			}
			var gerr error
			if b, _, gerr = s.gate(ctx, userID); gerr != nil {
				s.finish(l, store.RunFailed, hermes.CodeOf(gerr), "")
				return
			}
		default:
			// Cannot tell: not running for certain, not finished. Leave it resumable.
			s.disconnect(l, userID, runID)
			return
		}
	}
}

var errStop = errors.New("stop")

// reconcile reads the run's state from Hermes with a fresh credential check.
func (s *Service) reconcile(ctx context.Context, userID, hid string) (hermes.RunStatus, error) {
	b, _, err := s.gate(ctx, userID)
	if err != nil {
		return hermes.RunStatus{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	return s.hx.GetRun(cctx, b, hid)
}

func (s *Service) finishFromUpstream(l *liveRun, st hermes.RunStatus) {
	switch st.State {
	case hermes.StateCompleted:
		s.finish(l, store.RunCompleted, "", st.Output)
	case hermes.StateCancelled:
		s.finish(l, store.RunCancelled, "", "")
	default: // failed, interrupted
		code := "RUN_FAILED"
		if st.Interrupted {
			code = "RUN_INTERRUPTED"
		}
		s.finish(l, store.RunFailed, code, "")
	}
}

// finish ends the run. The database decides what happened: the transition only
// applies to a run that is not already terminal, and the event sent is the
// status that is actually stored — so a late "completed" after a cancel is
// reported as cancelled, never as completed. to == "" just reports the stored
// state.
func (s *Service) finish(l *liveRun, to, code, output string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.base), 10*time.Second)
	defer cancel()
	if to != "" {
		if _, err := store.MoveRun(ctx, s.db, l.userID, l.runID, []string{store.RunPending, store.RunRunning, store.RunDisconnected}, to, "", code, s.now()); err != nil {
			log.Printf("chat: run %s: could not record %s: %v", l.runID, to, err)
		}
	}
	run, err := store.GetRun(ctx, s.db, l.userID, l.runID)
	if err != nil {
		log.Printf("chat: run %s: could not read back its status: %v", l.runID, err)
		l.emit("run.failed", map[string]string{"runId": l.runID, "status": store.RunFailed, "code": hermes.CodeHermesUnavailable}, true)
		return
	}
	if s.notifier != nil && store.RunTerminal(run.Status) {
		s.notifier.RunFinished(run.UserID, run.ID, run.ChatID, run.Status) // the stored status, so a cancel is never a completion
	}
	if store.RunTerminal(run.Status) { // the transcript gained turns: its search index is stale
		if err := store.InvalidateChatIndex(ctx, s.db, run.UserID, run.ChatID, s.now()); err != nil {
			log.Printf("chat: run %s: could not mark search index stale: %v", l.runID, err)
		}
	}
	l.emit(terminalEvent(run.Status), terminalPayload(run, output), store.RunTerminal(run.Status))
	if !store.RunTerminal(run.Status) { // read back a non-terminal state: leave it resumable
		l.markDetached()
	}
}

func terminalEvent(status string) string {
	switch status {
	case store.RunCompleted:
		return "run.completed"
	case store.RunCancelled:
		return "run.cancelled"
	case store.RunFailed, store.RunTimedOut:
		return "run.failed"
	}
	return "run.disconnected"
}

func terminalPayload(run store.Run, output string) map[string]any {
	p := map[string]any{"runId": run.ID, "status": run.Status}
	if run.ErrorCode != "" {
		p["code"] = run.ErrorCode
	}
	if run.Status == store.RunCompleted && output != "" {
		p["output"] = output
	}
	return p
}

// disconnect leaves the run resumable: stored as DISCONNECTED, journal open,
// worker gone. A reconnect reattaches.
func (s *Service) disconnect(l *liveRun, userID, runID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.base), 10*time.Second)
	defer cancel()
	_, _ = store.MoveRun(ctx, s.db, userID, runID, []string{store.RunRunning}, store.RunDisconnected, "", "", s.now())
	l.emit("run.disconnected", map[string]string{"runId": runID, "status": store.RunDisconnected}, false)
	l.markDetached()
}

// stopUpstream asks Hermes to stop a run, best effort, behind the gate.
func (s *Service) stopUpstream(userID, hid string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.base), 15*time.Second)
	defer cancel()
	b, _, err := s.gate(ctx, userID)
	if err != nil || hid == "" {
		return false
	}
	if err := s.hx.StopRun(ctx, b, hid); err != nil {
		log.Printf("chat: upstream stop failed: %s", hermes.CodeOf(err))
		return false
	}
	return true
}

// Cancel ends the user's run. It is recorded CANCELLED first (so no later
// upstream event can turn it into anything else), the worker's upstream
// connection is closed, and Hermes is asked to stop. upstreamStopped says
// whether that last request was accepted; a false means the run may keep
// running on Hermes' side and the user is told so.
func (s *Service) Cancel(ctx context.Context, userID, runID string) (run store.Run, upstreamStopped bool, err error) {
	run, err = store.GetRun(ctx, s.db, userID, runID)
	if err != nil {
		return store.Run{}, false, err
	}
	if store.RunTerminal(run.Status) {
		return run, false, nil
	}
	moved, err := store.MoveRun(ctx, s.db, userID, runID, []string{store.RunPending, store.RunRunning, store.RunDisconnected}, store.RunCancelled, "", "", s.now())
	if err != nil {
		return store.Run{}, false, err
	}
	if !moved { // it ended on its own between the read and the write: report what happened
		run, err = store.GetRun(ctx, s.db, userID, runID)
		return run, false, err
	}
	l := s.liveFor(runID)
	if l != nil {
		if stop := l.markCancelled(); stop != nil {
			stop() // closes the upstream stream
		}
	}
	if run.HermesRunID != "" {
		upstreamStopped = s.stopUpstream(userID, run.HermesRunID)
	} else {
		upstreamStopped = true // never reached Hermes as far as we recorded; runNew stops it if it did
	}
	if run, err = store.GetRun(ctx, s.db, userID, runID); err != nil {
		return store.Run{}, false, err
	}
	if l != nil {
		l.emit(terminalEvent(run.Status), terminalPayload(run, ""), store.RunTerminal(run.Status))
	}
	return run, upstreamStopped, nil
}

// attach returns the journal for a run, creating the worker that fills it when
// there is none: after a restart, after the worker gave up, or for a run that
// is already terminal (a one-event journal built from the stored status).
func (s *Service) attach(ctx context.Context, run store.Run) (*liveRun, error) {
	if l := s.liveFor(run.ID); l != nil && !l.isDetached() {
		return l, nil
	}
	l := newLive(run.ID, run.UserID, run.ChatID)
	if store.RunTerminal(run.Status) {
		out := ""
		if run.Status == store.RunCompleted {
			if st, err := s.reconcile(ctx, run.UserID, run.HermesRunID); err == nil && st.State == hermes.StateCompleted {
				out = st.Output
			}
		}
		l.emit(terminalEvent(run.Status), terminalPayload(run, out), true)
		s.register(l)
		return l, nil
	}
	if run.HermesRunID == "" {
		// Never confirmed by Hermes. Not replayed (it may have side effects).
		s.finish(l, store.RunFailed, hermes.CodeHermesUnavailable, "")
		s.register(l)
		return l, nil
	}
	b, _, err := s.gate(ctx, run.UserID)
	if err != nil {
		return nil, err
	}
	st, err := s.reconcile(ctx, run.UserID, run.HermesRunID)
	if errors.Is(err, hermes.ErrRunGone) {
		s.finish(l, store.RunFailed, hermes.CodeHermesUnavailable, "")
		s.register(l)
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	switch {
	case st.Terminal():
		s.finishFromUpstream(l, st)
		s.register(l)
		return l, nil
	case st.State != hermes.StateRunning:
		// unknown: neither finished nor failed (RUN-001). Stay resumable.
		l.emit("run.disconnected", map[string]string{"runId": run.ID, "status": store.RunDisconnected}, false)
		l.markDetached()
		s.register(l)
		return l, nil
	}
	if _, err := store.MoveRun(ctx, s.db, run.UserID, run.ID, []string{store.RunDisconnected}, store.RunRunning, "", "", s.now()); err != nil {
		return nil, err
	}
	l.emit("run.resumed", map[string]any{"runId": run.ID, "replace": true}, false)
	wctx, cancel := context.WithCancel(s.base)
	l.cancelWorker = cancel
	s.register(l)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.follow(wctx, l, run.UserID, run.ID, b, run.HermesRunID)
	}()
	return l, nil
}
