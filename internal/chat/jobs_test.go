package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// serveJobs models Hermes' per-profile job store (gateway/platforms/api_server.py _handle_*_job, cron/jobs.py):
// each profile sees only its own jobs, an unknown id is 404 "Job not found", a job is created paused when asked,
// and the schedule is parsed into once / interval / cron. It records deliver so a test can see what was sent.
func (f *fakeHermes) serveJobs(w http.ResponseWriter, method, profile, path string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs == nil {
		f.jobs = map[string]map[string]map[string]any{}
	}
	mine := f.jobs[profile]
	if mine == nil {
		mine = map[string]map[string]any{}
		f.jobs[profile] = mine
	}
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if path == "/api/jobs" {
		switch method {
		case "GET":
			if f.jobsList != "" {
				_, _ = w.Write([]byte(f.jobsList))
				return
			}
			list := []map[string]any{}
			for _, j := range mine {
				list = append(list, j)
			}
			out(map[string]any{"jobs": list})
		case "POST":
			sched, ok := parseFakeSchedule(fmt.Sprint(in["schedule"]))
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				out(map[string]any{"error": "Invalid schedule"})
				return
			}
			f.n++
			id := fmt.Sprintf("%012x", f.n)
			paused := in["paused"] == true
			state := "scheduled"
			if paused {
				state = "paused"
			}
			mine[id] = map[string]any{"id": id, "name": in["name"], "prompt": in["prompt"], "schedule": sched, "schedule_display": sched["display"],
				"enabled": !paused, "state": state, "deliver": in["deliver"], "next_run_at": nil, "last_run_at": nil, "last_status": nil,
				"model": "secret-model", "workdir": "/opt/data/profiles/" + profile}
			out(map[string]any{"job": mine[id]})
		}
		return
	}
	rest := strings.TrimPrefix(path, "/api/jobs/")
	id, action, _ := strings.Cut(rest, "/")
	j := mine[id]
	if j == nil {
		w.WriteHeader(http.StatusNotFound)
		out(map[string]any{"error": "Job not found"})
		return
	}
	switch {
	case method == "GET":
	case method == "DELETE":
		delete(mine, id)
		out(map[string]any{"ok": true})
		return
	case action == "pause":
		j["enabled"], j["state"] = false, "paused"
	case action == "resume":
		j["enabled"], j["state"] = true, "scheduled"
	case action == "run":
		j["last_status"], j["last_run_at"] = "ok", time.Now().UTC().Format(time.RFC3339Nano)
	}
	out(map[string]any{"job": j})
}

func parseFakeSchedule(s string) (map[string]any, bool) {
	if m := regexp.MustCompile(`^every (\d+)([mh])$`).FindStringSubmatch(s); m != nil {
		var n int
		fmt.Sscan(m[1], &n)
		if m[2] == "h" {
			n *= 60
		}
		return map[string]any{"kind": "interval", "minutes": n, "display": fmt.Sprintf("every %dm", n)}, true
	}
	if len(strings.Fields(s)) >= 5 {
		return map[string]any{"kind": "cron", "expr": s, "display": s}, true
	}
	if strings.HasPrefix(s, "2026-") {
		return map[string]any{"kind": "once", "run_at": s, "display": "once at " + s}, true
	}
	return nil, false
}

func (f *fakeHermes) jobsOf(profile string) map[string]map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]any{}
	for id, j := range f.jobs[profile] {
		c := map[string]any{}
		for k, v := range j {
			c[k] = v
		}
		out[id] = c
	}
	return out
}

type jobAnswer struct {
	ID, Name, Prompt, Schedule, State string
	Paused                            bool
}

func createJob(t *testing.T, p *person, name, schedule string) jobAnswer {
	t.Helper()
	w := p.do("POST", "/api/jobs", nil, map[string]string{"name": name, "schedule": schedule, "prompt": "summarise the news"})
	if w.Code != 201 {
		t.Fatalf("create %q: %d %s", schedule, w.Code, w.Body.String())
	}
	return decodeBody[jobAnswer](t, w)
}

// jobRoutes carry a Hermes job id; their cross-user probes are TestJobsAreOwnerScoped.
var jobRoutes = []string{"GET /api/jobs/{jobId}", "DELETE /api/jobs/{jobId}",
	"POST /api/jobs/{jobId}/pause", "POST /api/jobs/{jobId}/resume", "POST /api/jobs/{jobId}/run"}

// #105, PRD §12: one user cannot see or act on another user's job. Every job call goes to the caller's own profile,
// so alice's job id is "not found" for bob on every route, and alice's job is unchanged.
func TestJobsAreOwnerScoped(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	job := createJob(t, alice, "alice-private-job", "every 2h")
	before := a.hx.jobsOf(alice.profile)[job.ID]
	for _, rt := range jobRoutes {
		method, path, _ := strings.Cut(rt, " ")
		w := bob.do(method, strings.Replace(path, "{jobId}", job.ID, 1), nil, map[string]string{"name": "x", "prompt": "y"})
		if w.Code != 404 || strings.Contains(w.Body.String(), "alice") {
			t.Errorf("bob %s: %d %s", rt, w.Code, w.Body.String())
		}
	}
	if w := bob.get("/api/jobs"); w.Code != 200 || strings.Contains(w.Body.String(), job.ID) {
		t.Errorf("bob's list: %d %s", w.Code, w.Body.String())
	}
	after := a.hx.jobsOf(alice.profile)[job.ID]
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("alice's job changed: %v -> %v", before, after)
	}
	// A malformed id is the same 404 and never reaches Hermes.
	n := len(a.hx.reqs)
	if w := alice.get("/api/jobs/../../api/jobs"); w.Code == 200 {
		t.Errorf("traversal: %d", w.Code)
	}
	if w := alice.get("/api/jobs/NOT-A-JOB"); w.Code != 404 || len(a.hx.reqs) != n {
		t.Errorf("malformed id: %d, %d upstream calls", w.Code, len(a.hx.reqs)-n)
	}
	// The owner can act on it, and the view carries none of the record's internals.
	w := alice.get("/api/jobs/" + job.ID)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret-model") || strings.Contains(w.Body.String(), "/opt/data") {
		t.Errorf("alice's own job: %d %s", w.Code, w.Body.String())
	}
	if w := alice.do("POST", "/api/jobs/"+job.ID+"/pause", nil, map[string]any{}); w.Code != 200 || !decodeBody[jobAnswer](t, w).Paused {
		t.Errorf("pause: %d %s", w.Code, w.Body.String())
	}
	if w := alice.do("DELETE", "/api/jobs/"+job.ID, nil, nil); w.Code != 204 || len(a.hx.jobsOf(alice.profile)) != 0 {
		t.Errorf("delete: %d", w.Code)
	}
}

// A job delivers locally, runs at most once an hour, starts only after its parsed schedule passes, takes no
// caller-chosen field, and an account has at most ten.
func TestJobCreationRules(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	for _, ok := range []string{"every 2h", "every 60m", "0 9 * * *", "2026-12-01T09:00:00"} {
		j := createJob(t, alice, "ok "+ok, ok)
		up := a.hx.jobsOf(alice.profile)[j.ID]
		if up["deliver"] != "local" || up["enabled"] != true || j.Paused {
			t.Errorf("%q upstream: deliver %v enabled %v; view paused %v", ok, up["deliver"], up["enabled"], j.Paused)
		}
	}
	held := len(a.hx.jobsOf(alice.profile))
	for _, bad := range []string{"every 30m", "*/5 * * * *", "0,30 9 * * *", "0 9 * * * 5"} {
		w := alice.do("POST", "/api/jobs", nil, map[string]string{"name": "too often", "schedule": bad, "prompt": "x"})
		if w.Code != 400 || !strings.Contains(w.Body.String(), "SCHEDULE_TOO_FREQUENT") {
			t.Errorf("%q: %d %s", bad, w.Code, w.Body.String())
		}
	}
	if got := len(a.hx.jobsOf(alice.profile)); got != held {
		t.Errorf("refused schedules left %d jobs upstream, want %d", got, held)
	}
	for name, body := range map[string]map[string]any{
		"deliver chosen": {"name": "x", "schedule": "every 2h", "prompt": "x", "deliver": "telegram"},
		"profile chosen": {"name": "x", "schedule": "every 2h", "prompt": "x", "profile": "u_other"},
		"no prompt":      {"name": "x", "schedule": "every 2h", "prompt": " "},
		"no schedule":    {"name": "x", "schedule": "", "prompt": "x"},
		"long name":      {"name": strings.Repeat("n", 101), "schedule": "every 2h", "prompt": "x"},
		"unparseable":    {"name": "x", "schedule": "whenever", "prompt": "x"},
	} {
		w := alice.do("POST", "/api/jobs", nil, body)
		if w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	for i := held; i < maxJobs; i++ {
		createJob(t, alice, fmt.Sprintf("n%d", i), "every 3h")
	}
	if w := alice.do("POST", "/api/jobs", nil, map[string]string{"name": "one more", "schedule": "every 3h", "prompt": "x"}); w.Code != 409 || !strings.Contains(w.Body.String(), "JOB_LIMIT") {
		t.Errorf("job %d: %d %s", maxJobs+1, w.Code, w.Body.String())
	}
}

func suspend(t *testing.T, a *app, target *person) map[string]any {
	t.Helper()
	admin := a.user("carol")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	w := admin.do("POST", "/api/admin/users/"+target.id+"/suspend", nil, map[string]string{"reason": "abuse"})
	if w.Code != 200 {
		t.Fatalf("suspend: %d %s", w.Code, w.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v
}

func auditOutcomes(t *testing.T, a *app, action, target string) []string {
	t.Helper()
	rows, err := a.db.Query(`SELECT outcome FROM audit_log WHERE action = ? AND target_id = ? ORDER BY created_at, id`, action, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		rows.Scan(&o)
		out = append(out, o)
	}
	return out
}

// PRD §12 rule 3: suspending a user pauses their jobs upstream, verified by reading them back, and the user's job
// routes are refused at once. Bob's jobs are untouched.
func TestSuspensionPausesTheUsersJobsUpstream(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	createJob(t, alice, "one", "every 2h")
	createJob(t, alice, "two", "0 9 * * *")
	createJob(t, bob, "bob's", "every 2h")
	v := suspend(t, a, alice)
	for id, j := range a.hx.jobsOf(alice.profile) {
		if j["enabled"] != false {
			t.Errorf("alice's job %s still enabled after suspension", id)
		}
	}
	for _, j := range a.hx.jobsOf(bob.profile) {
		if j["enabled"] != true {
			t.Error("bob's job was paused by alice's suspension")
		}
	}
	if got := auditOutcomes(t, a, "admin.suspend_jobs", alice.id); len(got) != 1 || got[0] != "ok" {
		t.Errorf("audit: %v", got)
	}
	if v["tenantDetail"] != "" {
		t.Errorf("detail after a clean shutdown: %v", v["tenantDetail"])
	}
	if w := alice.get("/api/jobs"); w.Code != 401 && w.Code != 403 {
		t.Errorf("suspended user's job list: %d", w.Code)
	}
}

// When Hermes cannot pause the jobs, the account is marked SUSPENSION_PENDING_JOB_SHUTDOWN (shown to the admin,
// audited as pending) and the retry pauses them and clears the mark once Hermes answers.
func TestSuspensionWithUnreachableJobsIsPendingThenRetried(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	createJob(t, alice, "one", "every 2h")
	a.hx.mu.Lock()
	a.hx.failNext["POST /api/jobs/"] = -503
	a.hx.mu.Unlock()
	v := suspend(t, a, alice)
	if v["tenantDetail"] != store.JobShutdownPending {
		t.Fatalf("admin view detail: %v", v["tenantDetail"])
	}
	if got := auditOutcomes(t, a, "admin.suspend_jobs", alice.id); len(got) != 1 || got[0] != "pending" {
		t.Errorf("audit: %v", got)
	}
	a.svc.retryJobShutdowns(context.Background()) // still failing: stays marked
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 1 {
		t.Fatalf("pending after a failed retry: %v", ids)
	}
	a.hx.mu.Lock()
	delete(a.hx.failNext, "POST /api/jobs/")
	a.hx.mu.Unlock()
	a.svc.retryJobShutdowns(context.Background())
	for _, j := range a.hx.jobsOf(alice.profile) {
		if j["enabled"] != false {
			t.Error("job still enabled after the retry")
		}
	}
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 0 {
		t.Errorf("still pending after a good retry: %v", ids)
	}
	if got := auditOutcomes(t, a, "system.suspend_jobs", alice.id); len(got) != 1 || got[0] != "ok" {
		t.Errorf("retry audit: %v", got)
	}
}

// #194 review: a job change that passed its gate before a suspension finishes before the suspension pauses the jobs
// (both hold the account's job lock), so a resume racing a suspension cannot leave a job enabled.
func TestResumeRacingASuspensionEndsPaused(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	job := createJob(t, alice, "one", "every 2h")
	if w := alice.do("POST", "/api/jobs/"+job.ID+"/pause", nil, map[string]any{}); w.Code != 200 {
		t.Fatalf("pause: %d", w.Code)
	}
	admin := a.user("carol")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	suspended := make(chan int, 1)
	a.svc.jobHook = func() {
		a.svc.jobHook = nil
		go func() {
			suspended <- admin.do("POST", "/api/admin/users/"+alice.id+"/suspend", nil, map[string]string{"reason": "abuse"}).Code
		}()
		select {
		case code := <-suspended:
			t.Errorf("the suspension finished (%d) while a job change held the lock", code)
			suspended <- code // for the read below
		case <-time.After(300 * time.Millisecond):
		}
	}
	if w := alice.do("POST", "/api/jobs/"+job.ID+"/resume", nil, map[string]any{}); w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	if code := <-suspended; code != 200 {
		t.Fatalf("suspend: %d", code)
	}
	if j := a.hx.jobsOf(alice.profile)[job.ID]; j["enabled"] != false {
		t.Errorf("the job resumed during the suspension is still enabled")
	}
	if got := auditOutcomes(t, a, "admin.suspend_jobs", alice.id); len(got) != 1 || got[0] != "ok" {
		t.Errorf("audit: %v", got)
	}
}

// #194 review: concurrent creates cannot take an account past its limit.
func TestConcurrentCreatesKeepTheLimit(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	var wg sync.WaitGroup
	codes := make(chan int, maxJobs+5)
	for i := 0; i < maxJobs+5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes <- alice.do("POST", "/api/jobs", nil, map[string]string{"name": fmt.Sprintf("j%d", i), "schedule": "every 2h", "prompt": "x"}).Code
		}(i)
	}
	wg.Wait()
	close(codes)
	created := 0
	for c := range codes {
		if c == 201 {
			created++
		} else if c != 409 {
			t.Errorf("unexpected status %d", c)
		}
	}
	if up := len(a.hx.jobsOf(alice.profile)); created != maxJobs || up != maxJobs {
		t.Errorf("created %d, upstream %d, want %d", created, up, maxJobs)
	}
}

// #194 review: accounts whose shutdown keeps failing cannot starve the others: retries rotate through all of them.
func TestPendingShutdownRetriesRotate(t *testing.T) {
	a := newApp(t)
	var people []*person
	for i := 0; i <= jobRetryPerTick; i++ {
		people = append(people, a.user(fmt.Sprintf("u%02d", i)))
	}
	sort.Slice(people, func(i, j int) bool { return people[i].id < people[j].id })
	good := people[len(people)-1] // the highest id: last in line
	for _, p := range people {
		if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED', tenant_detail = ? WHERE id = ?`, store.JobShutdownPending, p.id); err != nil {
			t.Fatal(err)
		}
		if p != good { // a credential that cannot be opened: this one fails on every retry
			if _, err := a.db.Exec(`UPDATE tenant_bindings SET credential_ciphertext = 'garbage' WHERE user_id = ?`, p.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx := context.Background()
	for tick := 0; tick < 2; tick++ {
		a.svc.retryJobShutdowns(ctx)
	}
	ids, _ := store.PendingJobShutdowns(ctx, a.db, "", 100)
	if slices.Contains(ids, good.id) || len(ids) != jobRetryPerTick {
		t.Errorf("after two ticks: %d pending, good one cleared %v", len(ids), !slices.Contains(ids, good.id))
	}
}

// #194 review: a too-frequent task whose delete failed stays paused upstream, and resuming it is refused on its stored
// schedule, so the hourly limit cannot be bypassed.
func TestATooFrequentTaskLeftBehindCannotBeResumed(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	a.hx.mu.Lock()
	a.hx.failNext["DELETE /api/jobs/"] = 500
	a.hx.mu.Unlock()
	if w := alice.do("POST", "/api/jobs", nil, map[string]string{"name": "fast", "schedule": "every 5m", "prompt": "x"}); w.Code != 400 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var left string
	for id, j := range a.hx.jobsOf(alice.profile) {
		if j["enabled"] != false {
			t.Fatalf("the refused task is enabled upstream")
		}
		left = id
	}
	if left == "" {
		t.Fatal("expected the refused task to remain after its delete failed")
	}
	w := alice.do("POST", "/api/jobs/"+left+"/resume", nil, map[string]any{})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "SCHEDULE_TOO_FREQUENT") {
		t.Errorf("resume: %d %s", w.Code, w.Body.String())
	}
	if a.hx.jobsOf(alice.profile)[left]["enabled"] != false {
		t.Error("the task was resumed")
	}
	if w := alice.do("DELETE", "/api/jobs/"+left, nil, nil); w.Code != 204 {
		t.Errorf("the owner can still delete it: %d", w.Code)
	}
}

// #194 review: the pending shutdown is recorded in the suspension's own transaction, so a process that stops between
// the suspension and the pause (simulated: the suspension alone, no pause) still leaves the account to the retry.
func TestSuspensionWithoutAPauseIsRecoveredByTheRetry(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	createJob(t, alice, "one", "every 2h")
	admin := a.user("carol")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Decide(context.Background(), a.db, admin.id, alice.id, store.ActionSuspend, "abuse", "req", time.Now()); err != nil {
		t.Fatal(err)
	}
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); !slices.Contains(ids, alice.id) {
		t.Fatalf("the suspension did not record the pending shutdown: %v", ids)
	}
	a.svc.retryJobShutdowns(context.Background()) // what the loop does after a restart
	for _, j := range a.hx.jobsOf(alice.profile) {
		if j["enabled"] != false {
			t.Error("job still enabled after the retry")
		}
	}
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 0 {
		t.Errorf("still pending: %v", ids)
	}
}

// PRD §12 rule 6 (#194 review): off by default. The routes stay the 501 placeholder and the capability says why.
func TestJobsAreOffUnlessSwitchedOn(t *testing.T) {
	a := newApp(t, appOpts{noJobs: true})
	alice := a.user("alice")
	if w := alice.get("/api/jobs"); w.Code != http.StatusNotImplemented {
		t.Errorf("GET /api/jobs while off: %d", w.Code)
	}
	if _, ok := a.svc.Handlers()["POST /api/jobs"]; ok {
		t.Error("job routes mounted while off")
	}
	w := alice.get("/api/capabilities")
	if !strings.Contains(w.Body.String(), `"id":"jobs"`) || !strings.Contains(w.Body.String(), "Switched off") {
		t.Errorf("capability while off: %s", w.Body.String())
	}
}

func (f *fakeHermes) setJob(profile, id, key string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[profile][id][key] = v
}

// #194 review: a manual run counts against the hourly bound, read from Hermes' own last_run_at, so a scheduled run
// counts too and a restart of Antares does not reset it; a running execution refuses another.
func TestManualRunIsLimitedToOncePerHour(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	job := createJob(t, alice, "one", "every 2h")
	run := func() int { return alice.do("POST", "/api/jobs/"+job.ID+"/run", nil, map[string]any{}).Code }
	if c := run(); c != 200 {
		t.Fatalf("first run: %d", c)
	}
	if c := run(); c != 429 {
		t.Errorf("second run within the hour: %d", c)
	}
	// A scheduled execution ten minutes ago (Hermes' record, which survives a restart of Antares).
	a.hx.setJob(alice.profile, job.ID, "last_run_at", time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339Nano))
	if c := run(); c != 429 {
		t.Errorf("after a scheduled run 10 minutes ago: %d", c)
	}
	a.hx.setJob(alice.profile, job.ID, "last_run_at", time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339Nano))
	a.hx.setJob(alice.profile, job.ID, "latest_execution", map[string]any{"status": "running"})
	if c := run(); c != 429 {
		t.Errorf("while an execution is running: %d", c)
	}
	a.hx.setJob(alice.profile, job.ID, "latest_execution", map[string]any{"status": "completed"})
	a.hx.setJob(alice.profile, job.ID, "next_run_at", time.Now().UTC().Add(20*time.Minute).Format(time.RFC3339Nano))
	if c := run(); c != 429 {
		t.Errorf("with the scheduled run due in 20 minutes: %d", c)
	}
	a.hx.setJob(alice.profile, job.ID, "next_run_at", time.Now().UTC().Add(90*time.Minute).Format(time.RFC3339Nano))
	if c := run(); c != 200 {
		t.Errorf("last run two hours ago, next in 90 minutes: %d", c)
	}
}

// #194 review: a job list Hermes answers without a list (or with a malformed job) is not "no jobs": the suspension
// stays pending.
func TestMalformedJobListKeepsTheShutdownPending(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	createJob(t, alice, "one", "every 2h")
	for _, body := range []string{`{}`, `{"jobs":null}`, `{"jobs":[{"id":"not-an-id"}]}`} {
		a.hx.mu.Lock()
		a.hx.jobsList = body
		a.hx.mu.Unlock()
		a.svc.retryJobShutdowns(context.Background())
	}
	a.hx.mu.Lock()
	a.hx.jobsList = `{}`
	a.hx.mu.Unlock()
	if v := suspend(t, a, alice); v["tenantDetail"] != store.JobShutdownPending {
		t.Fatalf("suspension with a malformed job list: detail %v", v["tenantDetail"])
	}
	a.svc.retryJobShutdowns(context.Background())
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 1 {
		t.Fatalf("cleared on a malformed list: %v", ids)
	}
	a.hx.mu.Lock()
	a.hx.jobsList = ""
	a.hx.mu.Unlock()
	a.svc.retryJobShutdowns(context.Background())
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 0 {
		t.Errorf("still pending with a good list: %v", ids)
	}
}

// #194 review: Hermes cannot cancel an execution in flight, so a suspension during one stays pending until it ends.
func TestSuspensionDuringAnExecutionStaysPendingUntilItEnds(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	job := createJob(t, alice, "one", "every 2h")
	a.hx.setJob(alice.profile, job.ID, "latest_execution", map[string]any{"status": "running"})
	if v := suspend(t, a, alice); v["tenantDetail"] != store.JobShutdownPending {
		t.Fatalf("suspended during a run: detail %v", v["tenantDetail"])
	}
	a.svc.retryJobShutdowns(context.Background())
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 1 {
		t.Fatalf("cleared while still running: %v", ids)
	}
	a.hx.setJob(alice.profile, job.ID, "latest_execution", map[string]any{"status": "completed"})
	a.svc.retryJobShutdowns(context.Background())
	if ids, _ := store.PendingJobShutdowns(context.Background(), a.db, "", 10); len(ids) != 0 {
		t.Errorf("still pending after the execution ended: %v", ids)
	}
}

// #194 review: an account whose jobs are not yet verified paused cannot be reactivated, so the retry never loses it.
func TestReactivationWaitsForTheJobShutdown(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	createJob(t, alice, "one", "every 2h")
	a.hx.mu.Lock()
	a.hx.failNext["POST /api/jobs/"] = -503
	a.hx.mu.Unlock()
	suspend(t, a, alice)
	admin := a.users["carol"]
	if w := admin.do("POST", "/api/admin/users/"+alice.id+"/reactivate", nil, map[string]any{}); w.Code != 409 {
		t.Fatalf("reactivate with jobs still running: %d %s", w.Code, w.Body.String())
	}
	a.hx.mu.Lock()
	delete(a.hx.failNext, "POST /api/jobs/")
	a.hx.mu.Unlock()
	a.svc.retryJobShutdowns(context.Background())
	if w := admin.do("POST", "/api/admin/users/"+alice.id+"/reactivate", nil, map[string]any{}); w.Code != 200 {
		t.Fatalf("reactivate after the shutdown: %d %s", w.Code, w.Body.String())
	}
}
