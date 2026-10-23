package smoke

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Status of one step. A step that did not run is Skip, never Pass.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Verdict of the whole run and the process exit code that goes with it.
const (
	VerdictPass       = "PASS"       // exit 0: every step ran and passed
	VerdictFail       = "FAIL"       // exit 1: at least one step failed
	VerdictIncomplete = "INCOMPLETE" // exit 2: nothing failed, but something did not run
)

// StepResult is one line of the report.
type StepResult struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     Status `json:"status"`
	Detail     string `json:"detail"`
	BlockedBy  string `json:"blockedBy,omitempty"`
	DurationMS int64  `json:"durationMs"`
}

// Report is the machine-readable result. It carries no credential: every string
// is passed through the Redactor before it is stored.
type Report struct {
	Target       string       `json:"target"`
	RunID        string       `json:"runId"`
	StartedAt    string       `json:"startedAt"`
	FinishedAt   string       `json:"finishedAt"`
	Verdict      string       `json:"verdict"`
	FirstFailure string       `json:"firstFailure,omitempty"`
	Pass         int          `json:"pass"`
	Fail         int          `json:"fail"`
	Skip         int          `json:"skip"`
	Steps        []StepResult `json:"steps"`
}

// ExitCode is 0 only for a run in which every step passed.
func (r Report) ExitCode() int {
	switch r.Verdict {
	case VerdictPass:
		return 0
	case VerdictFail:
		return 1
	}
	return 2
}

// SkipError makes a step skipped rather than failed: the precondition for
// running it is absent, which is not the same as the product being broken.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return e.Reason }

// Skipped is how a step reports that it could not run.
func Skipped(format string, a ...any) error { return &SkipError{Reason: fmt.Sprintf(format, a...)} }

// Runner executes steps in order, records each outcome and blocks a step whose
// prerequisites did not pass.
type Runner struct {
	Red    *Redactor
	Now    func() time.Time
	Log    io.Writer // live progress; nil for none
	steps  []StepResult
	status map[string]Status
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run executes one step. needs lists step ids that must have passed; if any did
// not, fn is not called and the step is recorded as skipped, naming the blocker.
func (r *Runner) Run(id, title string, needs []string, fn func() (string, error)) Status {
	if r.status == nil {
		r.status = map[string]Status{}
	}
	for _, n := range needs {
		if st := r.status[n]; st != Pass {
			why := string(st)
			if st == "" {
				why = "never ran"
			}
			return r.record(StepResult{ID: id, Title: title, Status: Skip, BlockedBy: n,
				Detail: fmt.Sprintf("not run: depends on %s (%s)", n, why)})
		}
	}
	start := r.now()
	detail, err := r.call(fn)
	res := StepResult{ID: id, Title: title, Status: Pass, Detail: detail, DurationMS: r.now().Sub(start).Milliseconds()}
	var skip *SkipError
	switch {
	case errors.As(err, &skip):
		res.Status, res.Detail = Skip, "not run: "+skip.Reason
	case err != nil:
		res.Status, res.Detail = Fail, err.Error()
	}
	return r.record(res)
}

// call converts a panic in a step into a failure of that step: one bad response
// shape must not hide the steps after it.
func (r *Runner) call(fn func() (string, error)) (detail string, err error) {
	defer func() {
		if p := recover(); p != nil {
			detail, err = "", fmt.Errorf("step crashed: %v", p)
		}
	}()
	return fn()
}

func (r *Runner) record(res StepResult) Status {
	res.Detail = r.Red.Clean(res.Detail)
	res.Title = r.Red.Clean(res.Title)
	r.steps = append(r.steps, res)
	r.status[res.ID] = res.Status
	if r.Log != nil {
		fmt.Fprintf(r.Log, "[ %-4s ] %-26s %7s  %s\n", strings.ToUpper(string(res.Status)), res.ID, dur(res.DurationMS), res.Detail)
	}
	return res.Status
}

func dur(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return (time.Duration(ms) * time.Millisecond).Round(time.Millisecond).String()
}

// Report assembles the final result.
func (r *Runner) Report(target, runID string, started time.Time) Report {
	rep := Report{Target: r.Red.Clean(target), RunID: runID, StartedAt: started.UTC().Format(time.RFC3339),
		FinishedAt: r.now().UTC().Format(time.RFC3339), Steps: append([]StepResult(nil), r.steps...)}
	for _, s := range r.steps {
		switch s.Status {
		case Pass:
			rep.Pass++
		case Fail:
			rep.Fail++
			if rep.FirstFailure == "" {
				rep.FirstFailure = s.ID
			}
		case Skip:
			rep.Skip++
		}
	}
	switch {
	case rep.Fail > 0:
		rep.Verdict = VerdictFail
	case rep.Skip > 0 || rep.Pass == 0:
		rep.Verdict = VerdictIncomplete
	default:
		rep.Verdict = VerdictPass
	}
	return rep
}

// JSON is the machine-readable form.
func (r Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// Summary is the last line of the human output.
func (r Report) Summary() string {
	s := fmt.Sprintf("RESULT: %s (%d passed, %d failed, %d skipped)", r.Verdict, r.Pass, r.Fail, r.Skip)
	if r.FirstFailure != "" {
		s += " — first failure: " + r.FirstFailure
	}
	if r.Verdict == VerdictIncomplete {
		s += " — skipped steps were NOT verified; this is not a pass"
	}
	return s
}
