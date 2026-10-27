package chat

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Scheduled jobs (PRD §12, issue #105). Every call goes to the caller's own Hermes profile with that profile's key
// (s.gate), where Hermes keeps that profile's jobs: a job id from another account is "not found" there, and the
// app never takes a profile, key or delivery target from a request. A job delivers locally (no messaging channel),
// runs at most once an hour, and an account has at most maxJobs. Suspending an account pauses its jobs upstream
// (PauseUserJobs); one that cannot be paused is marked SUSPENSION_PENDING_JOB_SHUTDOWN and retried.

const (
	maxJobs         = 10
	maxJobName      = 100
	maxJobSchedule  = 100
	maxJobPrompt    = 4000
	jobRetryEvery   = 5 * time.Minute
	jobRetryPerTick = 20
)

type jobView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prompt     string  `json:"prompt"`
	Schedule   string  `json:"schedule"`
	State      string  `json:"state"` // scheduled | paused | running | completed | error, as Hermes says
	Paused     bool    `json:"paused"`
	NextRunAt  *string `json:"nextRunAt"`
	LastRunAt  *string `json:"lastRunAt"`
	LastStatus *string `json:"lastStatus"`
}

func viewJob(j hermes.Job) jobView {
	return jobView{ID: j.ID, Name: j.Name, Prompt: j.Prompt, Schedule: j.ScheduleDisplay, State: j.State, Paused: !j.Enabled,
		NextRunAt: j.NextRunAt, LastRunAt: j.LastRunAt, LastStatus: j.LastStatus}
}

var cronMinute = regexp.MustCompile(`^[0-5]?[0-9]$`)

// hourlyAtMost is the allowed-schedule rule: a one-off time, an interval of an hour or more, or a five-field cron
// expression with one fixed minute (so at most once an hour).
func hourlyAtMost(s hermes.JobSchedule) bool {
	switch s.Kind {
	case "once":
		return true
	case "interval":
		return s.Minutes >= 60
	case "cron":
		f := strings.Fields(s.Expr)
		return len(f) == 5 && cronMinute.MatchString(f[0])
	}
	return false
}

// ranWithinTheHour reads a job's last_run_at (any execution, scheduled or manual). An unreadable time counts as recent:
// unknown is not "long ago".
func ranWithinTheHour(last *string, now time.Time) bool {
	if last == nil || *last == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, *last)
	if err != nil {
		return true
	}
	return now.Sub(t) < time.Hour
}

// dueWithinTheHour reads a job's next_run_at: a manual run now and the scheduled one would be under an hour apart.
// An unreadable time counts as due.
func dueWithinTheHour(next *string, now time.Time) bool {
	if next == nil || *next == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, *next)
	if err != nil {
		return true
	}
	return t.Sub(now) < time.Hour
}

func jobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermes.ErrJobGone):
		notFound(w)
	case errors.Is(err, hermes.ErrJobRefused):
		apiError(w, http.StatusBadRequest, "JOB_REFUSED", "The assistant did not accept this task. Check the schedule (for example \"every 2h\" or \"0 9 * * *\") and the instructions.")
	default:
		upstreamError(w, err)
	}
}

// jobLock serialises one account's job changes with each other and with its suspension (PauseUserJobs): a change
// takes the lock, then checks the account is still allowed (s.gate), so it either finishes before the suspension
// lists and pauses the jobs, or is refused. In-process only: Antares is one container. Creation's count-then-create
// is atomic under it too, so the account limit holds against concurrent creates.
func (s *Service) jobLock(userID string) func() {
	m, _ := s.jobLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// jobHandlers are the scheduled-task routes, mounted only when Options.Jobs is on.
func (s *Service) jobHandlers() map[string]http.Handler {
	if !s.jobs {
		return nil
	}
	return map[string]http.Handler{
		"GET /api/jobs":                 http.HandlerFunc(s.listJobs),
		"POST /api/jobs":                http.HandlerFunc(s.createJob),
		"GET /api/jobs/{jobId}":         http.HandlerFunc(s.getJob),
		"DELETE /api/jobs/{jobId}":      http.HandlerFunc(s.deleteJob),
		"POST /api/jobs/{jobId}/pause":  s.jobAction("pause"),
		"POST /api/jobs/{jobId}/resume": s.jobAction("resume"),
		"POST /api/jobs/{jobId}/run":    s.jobAction("run"),
	}
}

func (s *Service) listJobs(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		upstreamError(w, err)
		return
	}
	jobs, err := s.hx.ListJobs(r.Context(), b)
	if err != nil {
		jobError(w, err)
		return
	}
	out := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, viewJob(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "max": maxJobs})
}

type jobText struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

func (t *jobText) valid() bool {
	t.Name, t.Prompt = strings.TrimSpace(t.Name), strings.TrimSpace(t.Prompt)
	return t.Name != "" && utf8.RuneCountInString(t.Name) <= maxJobName && t.Prompt != "" && utf8.RuneCountInString(t.Prompt) <= maxJobPrompt
}

func (s *Service) createJob(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var in struct {
		jobText
		Schedule string `json:"schedule"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Schedule = strings.TrimSpace(in.Schedule)
	if !in.valid() || in.Schedule == "" || utf8.RuneCountInString(in.Schedule) > maxJobSchedule {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "A task needs a name (up to 100 characters), a schedule and instructions (up to 4,000 characters)")
		return
	}
	defer s.jobLock(uid)()
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		upstreamError(w, err)
		return
	}
	jobs, err := s.hx.ListJobs(r.Context(), b)
	if err != nil {
		jobError(w, err)
		return
	}
	if len(jobs) >= maxJobs {
		apiError(w, http.StatusConflict, "JOB_LIMIT", "You have the maximum of 10 scheduled tasks; delete one first")
		return
	}
	// Created paused, so nothing runs before the parsed schedule is checked; resumed only when it passes.
	if s.jobHook != nil {
		s.jobHook()
	}
	j, err := s.hx.CreateJob(r.Context(), b, in.Name, in.Schedule, in.Prompt)
	if err != nil {
		jobError(w, err)
		return
	}
	if !hourlyAtMost(j.Schedule) {
		if err := s.hx.DeleteJob(r.Context(), b, j.ID); err != nil {
			log.Printf("chat: a refused job could not be removed (left paused): %s", hermes.CodeOf(err))
		}
		apiError(w, http.StatusBadRequest, "SCHEDULE_TOO_FREQUENT", "A task can run at most once an hour (for example \"every 2h\", \"0 9 * * *\" or a single time)")
		return
	}
	if j, err = s.hx.JobAction(r.Context(), b, j.ID, "resume"); err != nil {
		jobError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewJob(j))
}

// jobRoute resolves the caller's binding and the job id under the account's job lock; a malformed id is the same
// 404 as an unknown one. The caller runs the returned unlock when done.
func (s *Service) jobRoute(w http.ResponseWriter, r *http.Request) (hermes.Binding, string, func(), bool) {
	uid, _ := user(r)
	id := r.PathValue("jobId")
	if !hermes.ValidJobID(id) {
		notFound(w)
		return hermes.Binding{}, "", nil, false
	}
	unlock := s.jobLock(uid)
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		unlock()
		upstreamError(w, err)
		return hermes.Binding{}, "", nil, false
	}
	return b, id, unlock, true
}

func (s *Service) getJob(w http.ResponseWriter, r *http.Request) {
	b, id, unlock, ok := s.jobRoute(w, r)
	if !ok {
		return
	}
	defer unlock()
	j, err := s.hx.GetJob(r.Context(), b, id)
	if err != nil {
		jobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewJob(j))
}

func (s *Service) deleteJob(w http.ResponseWriter, r *http.Request) {
	b, id, unlock, ok := s.jobRoute(w, r)
	if !ok {
		return
	}
	defer unlock()
	if err := s.hx.DeleteJob(r.Context(), b, id); err != nil {
		jobError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) jobAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, id, unlock, ok := s.jobRoute(w, r)
		if !ok {
			return
		}
		defer unlock()
		if s.jobHook != nil {
			s.jobHook()
		}
		if action == "resume" { // the stored schedule is checked again: a refused task whose delete failed stays paused
			cur, err := s.hx.GetJob(r.Context(), b, id)
			if err != nil {
				jobError(w, err)
				return
			}
			if !hourlyAtMost(cur.Schedule) {
				apiError(w, http.StatusBadRequest, "SCHEDULE_TOO_FREQUENT", "This task runs more than once an hour, so it cannot be resumed. Delete it and add it again with a slower schedule.")
				return
			}
		}
		if action == "run" { // a manual run counts against the same hourly bound, read from Hermes' own record of the job
			cur, err := s.hx.GetJob(r.Context(), b, id)
			if err != nil {
				jobError(w, err)
				return
			}
			if cur.Running() || ranWithinTheHour(cur.LastRunAt, s.now()) || dueWithinTheHour(cur.NextRunAt, s.now()) {
				apiError(w, http.StatusTooManyRequests, hermes.CodeRateLimited, "This task ran less than an hour ago, is running now, or is due within the hour")
				return
			}
		}
		j, err := s.hx.JobAction(r.Context(), b, id, action)
		if err != nil {
			jobError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, viewJob(j))
	}
}

// PauseUserJobs pauses every running job of an account that is being suspended, through its own binding whatever
// the binding's status now, and then reads the list back: success means Hermes reports none still enabled. An
// account with no binding, or a Hermes without jobs, has nothing to pause.
func (s *Service) PauseUserJobs(ctx context.Context, userID string) error {
	defer s.jobLock(userID)()
	cred, err := store.BindingCredential(ctx, s.db, userID)
	if errors.Is(err, store.ErrNoBinding) {
		return nil
	} else if err != nil {
		return err
	}
	key, err := s.box.Open(cred.Ciphertext, cred.BindingID)
	if err != nil {
		return err
	}
	b := hermes.Binding{Profile: cred.Profile, Key: key}
	jobs, err := s.hx.ListJobs(ctx, b)
	if hermes.CodeOf(err) == hermes.CodeCapabilityNA {
		return nil
	} else if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Enabled {
			if _, err := s.hx.JobAction(ctx, b, j.ID, "pause"); err != nil && !errors.Is(err, hermes.ErrJobGone) {
				return err
			}
		}
	}
	if jobs, err = s.hx.ListJobs(ctx, b); err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Enabled {
			return errors.New("chat: a job is still enabled after pausing")
		}
		// Hermes cannot cancel an execution in flight, so the shutdown is verified only once none is running;
		// until then the account stays marked and the retry checks again.
		if j.Running() {
			return errors.New("chat: a job execution is still running")
		}
	}
	return nil
}

// retryJobShutdowns pauses the jobs of suspended accounts marked SUSPENSION_PENDING_JOB_SHUTDOWN and clears the
// mark once Hermes reports them paused.
func (s *Service) retryJobShutdowns(ctx context.Context) {
	// Rotate through the marked accounts, so ones that keep failing cannot starve the rest.
	ids, err := store.PendingJobShutdowns(ctx, s.db, s.jobRetryAfter, jobRetryPerTick)
	if err == nil && len(ids) < jobRetryPerTick && s.jobRetryAfter != "" {
		var more []string
		more, err = store.PendingJobShutdowns(ctx, s.db, "", jobRetryPerTick-len(ids))
		for _, id := range more {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if err != nil {
		log.Printf("chat: pending job shutdowns: %v", err)
		return
	}
	if len(ids) > 0 {
		s.jobRetryAfter = ids[len(ids)-1]
	}
	for _, id := range ids {
		if err := s.PauseUserJobs(ctx, id); err != nil {
			log.Printf("chat: user %s: jobs still not paused: %s", id, hermes.CodeOf(err))
			continue
		}
		if err := store.ClearJobShutdownPending(ctx, s.db, id); err != nil {
			log.Printf("chat: user %s: could not clear the pending job shutdown: %v", id, err)
			continue
		}
		if err := store.AuditSystem(ctx, s.db, "system.suspend_jobs", id, "ok", s.now()); err != nil {
			log.Printf("chat: audit write failed: %v", err)
		}
	}
}

func (s *Service) jobShutdownLoop() {
	defer s.wg.Done()
	t := time.NewTicker(jobRetryEvery)
	defer t.Stop()
	for {
		select {
		case <-s.base.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(s.base, time.Minute)
			s.retryJobShutdowns(ctx)
			cancel()
		}
	}
}
