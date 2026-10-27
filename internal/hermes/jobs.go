package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Scheduled jobs (PRD §12, issue #105). Hermes keeps each profile's jobs in that profile's own store and runs them
// under that profile, so a job id is only ever looked up in the binding's profile: another user's job is "not found"
// there by construction. Antares builds every request body itself; no caller-chosen field reaches Hermes.

// Job is the part of a Hermes job record Antares reads. The record has more (model, script, workdir, delivery
// targets, origin); none of it is decoded, so none of it can be shown or forwarded.
type Job struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Prompt          string      `json:"prompt"`
	Schedule        JobSchedule `json:"schedule"`
	ScheduleDisplay string      `json:"schedule_display"`
	Enabled         bool        `json:"enabled"`
	State           string      `json:"state"`
	NextRunAt       *string     `json:"next_run_at"`
	LastRunAt       *string     `json:"last_run_at"`
	LastStatus      *string     `json:"last_status"`
	Deliver         string      `json:"deliver"`
	// LatestExecution is the job's most recent execution (cron/executions.py), when Hermes reports one.
	LatestExecution *struct {
		Status string `json:"status"` // claimed | running | completed | failed | unknown
	} `json:"latest_execution"`
}

// Running reports whether the job's latest execution has not finished. Hermes has no route to cancel one.
func (j Job) Running() bool {
	return j.LatestExecution != nil && (j.LatestExecution.Status == "claimed" || j.LatestExecution.Status == "running")
}

// Settled reports that the job has no execution in flight that anyone can see: none at all, or a latest one that
// finished (completed or failed). An unknown or unrecognised status is not settled: it cannot be shown to have stopped.
func (j Job) Settled() bool {
	return j.LatestExecution == nil || j.LatestExecution.Status == "completed" || j.LatestExecution.Status == "failed"
}

// JobSchedule is how Hermes parsed the schedule text.
type JobSchedule struct {
	Kind    string `json:"kind"` // once | interval | cron
	Minutes int    `json:"minutes"`
	Expr    string `json:"expr"`
}

// ErrJobGone is a job id this profile does not have.
var ErrJobGone = errors.New("hermes: job not found")

var jobIDRE = regexp.MustCompile(`^[a-f0-9]{12}$`)

// ValidJobID is the shape Hermes gives a job id; anything else is never sent.
func ValidJobID(s string) bool { return jobIDRE.MatchString(s) }

// ListJobs returns every job of the binding's profile, paused ones included.
func (c *Client) ListJobs(ctx context.Context, b Binding) ([]Job, error) {
	var out struct {
		Jobs *[]json.RawMessage `json:"jobs"`
	}
	if err := c.jobCall(ctx, b, http.MethodGet, "/api/jobs?include_disabled=true", nil, &out); err != nil {
		return nil, err
	}
	// Suspension relies on this list, so it fails closed: a success without a list, or with a job missing its id, its
	// "enabled" flag or its "latest_execution" (Hermes' list sets it, null when the job never ran), is an error, never
	// "no jobs" or "disabled".
	if out.Jobs == nil {
		return nil, fail(CodeHermesUnavailable)
	}
	jobs := make([]Job, 0, len(*out.Jobs))
	for _, raw := range *out.Jobs {
		var j Job
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &j) != nil || json.Unmarshal(raw, &keys) != nil || !ValidJobID(j.ID) {
			return nil, fail(CodeHermesUnavailable)
		}
		var enabled bool
		if e, ok := keys["enabled"]; !ok || json.Unmarshal(e, &enabled) != nil {
			return nil, fail(CodeHermesUnavailable)
		}
		if _, ok := keys["latest_execution"]; !ok {
			return nil, fail(CodeHermesUnavailable)
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// CreateJob creates a job that delivers locally (no messaging channel) and starts paused: the caller checks how
// Hermes parsed the schedule before resuming it.
func (c *Client) CreateJob(ctx context.Context, b Binding, name, schedule, prompt string) (Job, error) {
	body := map[string]any{"name": name, "schedule": schedule, "prompt": prompt, "deliver": "local", "paused": true,
		"paused_reason": "Checking the schedule"}
	return c.jobOne(ctx, b, http.MethodPost, "/api/jobs", body)
}

// GetJob reads one job of the binding's profile.
func (c *Client) GetJob(ctx context.Context, b Binding, id string) (Job, error) {
	return c.jobOne(ctx, b, http.MethodGet, jobPath(id, ""), nil)
}

// JobAction is pause, resume or run.
func (c *Client) JobAction(ctx context.Context, b Binding, id, action string) (Job, error) {
	switch action {
	case "pause", "resume", "run":
	default:
		return Job{}, fail(CodeCapabilityNA)
	}
	return c.jobOne(ctx, b, http.MethodPost, jobPath(id, "/"+action), map[string]any{})
}

// DeleteJob removes a job.
func (c *Client) DeleteJob(ctx context.Context, b Binding, id string) error {
	return c.jobCall(ctx, b, http.MethodDelete, jobPath(id, ""), nil, &struct{}{})
}

func jobPath(id, tail string) string { return "/api/jobs/" + url.PathEscape(id) + tail }

func (c *Client) jobOne(ctx context.Context, b Binding, method, path string, body any) (Job, error) {
	var out struct {
		Job Job `json:"job"`
	}
	if err := c.jobCall(ctx, b, method, path, body, &out); err != nil {
		return Job{}, err
	}
	if !ValidJobID(out.Job.ID) {
		return Job{}, fail(CodeHermesUnavailable)
	}
	return out.Job, nil
}

// jobCall maps Hermes' answer: its own "Job not found" 404 is ErrJobGone; a 404 without it (no jobs route) and
// a 501 (no cron module) are CAPABILITY_UNAVAILABLE; a 400 is a schedule or prompt Hermes refused.
func (c *Client) jobCall(ctx context.Context, b Binding, method, path string, body, v any) error {
	if strings.HasPrefix(path, "/api/jobs/") {
		id := strings.SplitN(strings.TrimPrefix(path, "/api/jobs/"), "/", 2)[0]
		if !ValidJobID(id) {
			return ErrJobGone
		}
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.send(ctx, b, method, path, nil, body)
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return readJSON(resp, v, maxAPIResponse)
	case resp.StatusCode == http.StatusNotFound:
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if strings.Contains(string(raw), "Job not found") {
			return ErrJobGone
		}
		return fail(CodeCapabilityNA)
	case resp.StatusCode == http.StatusBadRequest:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return ErrJobRefused
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return statusError(resp.StatusCode)
	}
}

// ErrJobRefused is a job Hermes would not accept as given (an unparseable schedule, a prompt its scanner refused).
// Its message is not passed on.
var ErrJobRefused = errors.New("hermes: job refused")
