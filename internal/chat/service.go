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
	"slices"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/kenari"
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
	// Kenari is the model catalogue image input is checked against. Nil means image input is
	// reported unavailable (CATALOGUE_NOT_CONFIGURED), never assumed.
	Kenari *kenari.Catalogue
	// Notifier, if set, is told when a run reaches a terminal status (UXP-002).
	Notifier Notifier
	// Jobs turns the scheduled-task routes on (PRD §12 rule 6: only once owner isolation and suspension are proven on
	// the deployed Hermes). Off, the routes stay the 501 placeholder and the capability is reported unsupported;
	// suspension still pauses any jobs an account has.
	Jobs bool
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
	kenari    *kenari.Catalogue

	// queueConflictHook, when set by a test, runs after a queued message was refused for a busy chat and
	// before its claim is released (queue.go), to place the run's end inside that window.
	queueConflictHook func()

	streams streamSet // open run streams, for sign-outs to stop at once (revocations.go)
	// streamOpenHook, when set by a test, runs in a run stream after the guard admitted it and before the stream
	// registers, to place a sign-out exactly in that window.
	streamOpenHook func()
	// admitHook, when set by a test, runs in a send after the chat was read and before the admission transaction, to
	// finish the chat's run (and reach a limit) exactly in that window.
	admitHook func()

	jobs          bool
	jobLocks      sync.Map // user id -> *sync.Mutex (jobs.go jobLock)
	jobRetryAfter string   // where the next pending job shutdown retry starts; only the retry loop touches it
	// jobHook, when set by a test, runs inside a job change after its gate, holding the account's job lock.
	jobHook func()

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
	s := &Service{db: o.DB, box: o.Box, hx: o.Hermes, now: o.Now, backoff: o.Backoff, heartbeat: o.Heartbeat, notifier: o.Notifier, kenari: o.Kenari, jobs: o.Jobs,
		base: ctx, stop: cancel, live: map[string]*liveRun{}}
	s.wg.Add(1)
	go s.jobShutdownLoop()
	return s, nil
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
func (s *Service) start(run store.Run, input string, imgs *turnImages) *liveRun {
	l := newLive(run.ID, run.UserID, run.ChatID)
	ctx, cancel := context.WithCancel(s.base)
	l.cancelWorker = cancel
	s.register(l)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.runNew(ctx, l, run, input, imgs)
	}()
	return l
}

func (s *Service) runNew(ctx context.Context, l *liveRun, run store.Run, input string, imgs *turnImages) {
	b, _, err := s.gate(ctx, run.UserID)
	if err != nil {
		s.finish(l, store.RunFailed, hermes.CodeOf(err), "")
		return
	}
	choice, err := store.GetChatModel(ctx, s.db, run.UserID, run.ChatID)
	if err != nil {
		s.finish(l, store.RunFailed, hermes.CodeHermesUnavailable, "")
		return
	}
	personality, err := store.Personality(ctx, s.db, run.UserID)
	if err != nil {
		s.finish(l, store.RunFailed, hermes.CodeHermesUnavailable, "")
		return
	}
	req := hermes.RunRequest{
		SessionID: run.HermesSession, Input: input, SessionKey: sessionKey(run.UserID), IdempotencyKey: "antares-" + run.ID,
		Provider: choice.Provider, Model: choice.Model, Reasoning: choice.Reasoning, ServiceTier: choice.ServiceTier,
		Instructions: runInstructions(personality),
	}
	if imgs != nil {
		// The model that was checked for image input is the model that runs: named explicitly,
		// so a profile default that changed in between cannot swap in one that was not checked.
		req.Provider, req.Model, req.Images = imgs.provider, imgs.model, imgs.hermesImages()
	}
	// Last gate before dispatch: policy applies to the model that will actually run. A chat on the
	// profile default has it resolved and named explicitly, so a default changed or blocked since
	// admission can neither run nor be swapped for another.
	if blocked, err := store.BlockedModels(ctx, s.db); err != nil {
		s.finish(l, store.RunFailed, hermes.CodeHermesUnavailable, "")
		return
	} else if len(blocked) > 0 {
		if req.Model == "" {
			p, m, err := s.effectiveDefault(ctx, b)
			if err != nil {
				s.finish(l, store.RunFailed, "MODEL_UNAVAILABLE", "")
				return
			}
			req.Provider, req.Model = p, m
		}
		if slices.Contains(blocked, req.Model) {
			s.finish(l, store.RunFailed, "MODEL_UNAVAILABLE", "")
			return
		}
	}
	hid, err := s.hx.CreateRun(ctx, b, req)
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
	failClosed := false
	for attempt := 0; ; attempt++ {
		err := s.hx.StreamEvents(ctx, b, hid, func(ev hermes.Event) error {
			n := hermes.Normalize(ev)
			switch n.Kind {
			case hermes.KindDelta:
				l.emit("assistant.delta", map[string]string{"text": n.Text}, false)
			case hermes.KindToolStarted:
				l.emit("tool.started", map[string]string{"toolCallId": n.ToolID, "label": n.Label}, false)
			case hermes.KindToolCompleted:
				l.emit("tool.completed", map[string]any{"toolCallId": n.ToolID, "summary": n.Summary, "failed": n.Failed, "durationMs": n.DurationMs}, false)
			case hermes.KindThinking:
				l.emit("run.thinking", map[string]any{}, false)
			case hermes.KindApproval, hermes.KindClarify:
				if !s.holdPrompt(ctx, l, userID, runID, b, n) {
					failClosed = true
					return errStop
				}
			case hermes.KindPromptSettled:
				if n.PromptID != "" {
					if err := store.SettlePrompt(s.base, s.db, userID, runID, n.PromptID, s.now()); err != nil {
						log.Printf("chat: run %s: could not settle a prompt: %v", runID, err)
					}
				}
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
		case failClosed:
			// Hermes is waiting for a decision Antares cannot collect (no request id to answer,
			// a route Hermes does not advertise, nothing the user may safely choose). Nothing is
			// approved or denied on the user's behalf: the run is stopped and ends failed, visibly.
			s.stopUpstream(userID, hid)
			s.finish(l, store.RunFailed, hermes.CodeCapabilityNA, "")
			return
		case terminal != nil:
			u := terminal.Usage
			if u == nil { // the frame carried none: the poll is the documented source
				if st, rerr := s.reconcile(ctx, userID, hid); rerr == nil && st.Terminal() {
					u = st.Usage
				}
			}
			s.recordUsage(userID, runID, u)
			s.finishFromUpstream(l, hermes.RunStatus{State: terminal.State, Output: terminal.Output, Interrupted: terminal.Intr})
			return
		}
		_ = err // the code is not needed: the state is read back below
		st, rerr := s.reconcile(ctx, userID, hid)
		switch {
		case rerr == nil && st.Terminal():
			s.recordUsage(userID, runID, st.Usage)
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
	logRunState(run)
	if s.notifier != nil && store.RunTerminal(run.Status) {
		s.notifier.RunFinished(run.UserID, run.ID, run.ChatID, run.Status) // the stored status, so a cancel is never a completion
	}
	if store.RunTerminal(run.Status) { // no decision can still be pending on a run that is over
		if err := store.SettlePrompts(ctx, s.db, run.UserID, run.ID, s.now()); err != nil {
			log.Printf("chat: run %s: could not settle its prompts: %v", l.runID, err)
		}
	}
	if store.RunTerminal(run.Status) { // the transcript gained turns: its search index is stale
		if err := store.InvalidateChatIndex(ctx, s.db, run.UserID, run.ChatID, s.now()); err != nil {
			log.Printf("chat: run %s: could not mark search index stale: %v", l.runID, err)
		}
	}
	// The queue moves before the browser hears the end, so a client that reads the chat on run.completed
	// already sees the run its next queued message became.
	s.afterRun(run)
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
	if run, err := store.GetRun(ctx, s.db, userID, runID); err == nil {
		logRunState(run)
	}
	if err := store.PauseQueueIfWaiting(ctx, s.db, userID, l.chatID, store.RunDisconnected); err != nil { // unknown state: the owner decides
		log.Printf("chat: run %s: could not pause its chat's queue: %v", runID, err)
	}
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
	if err := store.SettlePrompts(ctx, s.db, userID, runID, s.now()); err != nil {
		log.Printf("chat: run %s: could not settle its prompts: %v", runID, err)
	}
	if err := store.PauseQueueIfWaiting(ctx, s.db, userID, run.ChatID, store.RunCancelled); err != nil { // a stop is not "go on"
		log.Printf("chat: run %s: could not pause its chat's queue: %v", runID, err)
	}
	l := s.liveFor(runID)
	if l != nil {
		if stop := l.markCancelled(); stop != nil {
			stop() // closes the upstream stream
		}
	}
	if run.HermesRunID != "" {
		upstreamStopped = s.stopUpstream(userID, run.HermesRunID)
		// What the run spent before it stopped still counts against the budget:
		// read Hermes' usage once. Nothing reported records nothing.
		if st, rerr := s.reconcile(ctx, userID, run.HermesRunID); rerr == nil {
			s.recordUsage(userID, runID, st.Usage)
		}
	} else {
		upstreamStopped = true // never reached Hermes as far as we recorded; runNew stops it if it did
	}
	if run, err = store.GetRun(ctx, s.db, userID, runID); err != nil {
		return store.Run{}, false, err
	}
	logRunState(run)
	if l != nil {
		l.emit(terminalEvent(run.Status), terminalPayload(run, ""), store.RunTerminal(run.Status))
	}
	return run, upstreamStopped, nil
}

// ErrNotSteerable is a steer for a run that is already over or never reached Hermes.
var ErrNotSteerable = errors.New("chat: run is not steerable")

// Steer sends the user's guidance into their own running run. Ownership comes
// first (a foreign run is ErrNoChat, before Hermes is contacted), then the stored
// status, then the gate. Success is Hermes' own acceptance and is announced on the
// run's journal as run.steered; nothing is appended to the transcript, and the
// guidance text is not echoed (Hermes records what it did with it).
func (s *Service) Steer(ctx context.Context, userID, runID, text string) error {
	run, err := store.GetRun(ctx, s.db, userID, runID)
	if err != nil {
		return err
	}
	if store.RunTerminal(run.Status) || run.HermesRunID == "" {
		return ErrNotSteerable
	}
	b, _, err := s.gate(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.hx.SteerRun(ctx, b, run.HermesRunID, text); err != nil {
		return err
	}
	if l := s.liveFor(runID); l != nil {
		l.emit("run.steered", map[string]any{"runId": runID, "accepted": true}, false)
	}
	return nil
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
		s.recordUsage(run.UserID, run.ID, st.Usage)
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

// logRunState writes the run's stored state with the PRD §13.2 identifiers (issue #114): internal user, chat and run
// ids, the state, the error code, and token usage when Hermes reported it. A run whose outcome is not known is
// DISCONNECTED and logged as "unknown", never as success. No prompt, output or key is logged.
func logRunState(run store.Run) {
	outcome := "unknown"
	switch run.Status {
	case store.RunCompleted:
		outcome = "success"
	case store.RunFailed, store.RunTimedOut:
		outcome = "failure"
	case store.RunCancelled:
		outcome = "cancelled"
	case store.RunPending, store.RunRunning:
		outcome = "running"
	}
	code := run.ErrorCode
	if code == "" {
		code = "-"
	}
	log.Printf("run user=%s chat=%s run=%s binding=%s state=%s outcome=%s error=%s", run.UserID, run.ChatID, run.ID, run.BindingID, run.Status, outcome, code)
}
