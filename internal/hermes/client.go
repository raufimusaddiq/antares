package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/raufimusaddiq/antares/internal/obs"
)

// This file is the chat adapter (PRD §6, §9A.2, §9A.3): the only code that
// sends a conversation to Hermes. Every request goes to
// <configured origin>/p/<profile>/<path>, where the profile and the bearer key
// come from the caller's Binding — which the server derives from the
// authenticated account. Nothing a browser sent reaches a URL, header or body
// here except the message text itself.
//
// The run, session and message shapes below are the DOCUMENTED ones (PRD
// §9A); Phase 0 did not capture them on the wire (docs/hermes/PHASE0-FINDINGS.md
// §2). So every decoder is strict about what it needs and treats anything else
// as an unusable answer (HERMES_UNAVAILABLE), never as a guessed success.

// API error codes (PRD §9.1). An *Error carries only one of these: never
// upstream text, a URL or a credential.
const (
	CodeTenantNotReady    = "TENANT_NOT_READY"
	CodeHermesUnavailable = "HERMES_UNAVAILABLE"
	CodeUpstreamTimeout   = "UPSTREAM_TIMEOUT"
	CodeRunConflict       = "RUN_CONFLICT"
	CodeRateLimited       = "RATE_LIMITED"
	CodeCapabilityNA      = "CAPABILITY_UNAVAILABLE"
	CodeSteerRefused      = "STEER_REFUSED"
	CodePromptStale       = "PROMPT_STALE"
)

const (
	maxLine            = 256 << 10 // one SSE line
	maxEventData       = 128 << 10 // one SSE frame
	maxOutput          = 1 << 20   // run output kept from a status read
	maxAPIResponse     = 1 << 20
	maxMessagesBody    = 24 << 20 // a long conversation: PRD §14.2 asks for 1,000+ turns
	maxTranscriptMsgs  = 20000    // beyond this the read fails visibly rather than truncating
	maxMessagesPerPage = 200
	maxTranscriptReads = 2 // concurrent full-transcript reads, so memory stays bounded
	maxMessageText     = 200 << 10
	maxSessionKeyLen   = 128
	maxIdempotencyLen  = 128
	callTimeout        = 30 * time.Second
	headerTimeout      = 30 * time.Second
	defaultStreamIdle  = 90 * time.Second
	defaultStreamMax   = 30 * time.Minute
)

// Error is a failed Hermes call, reduced to a code. Retryable tells the caller
// whether another attempt could succeed; the adapter itself never retries.
type Error struct{ Code string }

func (e *Error) Error() string { return "hermes: " + e.Code }

// CodeOf returns the code of an adapter error, or HERMES_UNAVAILABLE for
// anything else (an unexpected error is never allowed to describe itself).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeUpstreamTimeout
	}
	return CodeHermesUnavailable
}

func fail(code string) error { return &Error{Code: code} }

// Binding is the server-resolved identity of one Hermes call. Its fields are
// never printed: String, GoString and Format all redact, so a stray %v or %+v
// cannot put the key in a log line.
type Binding struct {
	Profile string
	Key     string
}

func (Binding) String() string   { return "hermes.Binding{redacted}" }
func (Binding) GoString() string { return "hermes.Binding{redacted}" }

// Format makes every fmt verb print the redacted form.
func (b Binding) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(b.String()))
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidID reports an upstream identifier that is safe to place in a URL path.
// Identifiers come from Hermes' own responses and are re-checked before use:
// they are never concatenated raw.
func ValidID(s string) bool { return idRE.MatchString(s) }

// Client talks to the Hermes API origin.
type Client struct {
	base        *url.URL
	http        *http.Client
	idle        time.Duration
	call        time.Duration
	maxStream   time.Duration
	transcripts chan struct{} // bounds concurrent full-transcript reads
	capture     *Capture      // sanitized wire fixtures for synthetic smoke profiles (#169); nil = off
}

// ClientOptions tunes timeouts; zero values mean the production defaults.
type ClientOptions struct {
	HTTPClient  *http.Client // transport override for tests
	StreamIdle  time.Duration
	CallTimeout time.Duration
	StreamMax   time.Duration
	// Capture records sanitized fixtures for the profiles it allows (ANTARES_WIRE_CAPTURE_DIR, #169). Nil is off.
	Capture *Capture
}

// NewClient builds a Client for the Hermes origin (e.g. http://hermes:8642).
func NewClient(baseURL string, o ClientOptions) (*Client, error) {
	u, err := parseOrigin(baseURL)
	if err != nil {
		return nil, errors.New("hermes: base URL " + err.Error())
	}
	tr := http.RoundTripper(&http.Transport{
		Proxy:                 nil, // the bearer key never goes through a proxy
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: headerTimeout,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
	})
	if o.HTTPClient != nil && o.HTTPClient.Transport != nil {
		tr = o.HTTPClient.Transport
	}
	c := &Client{base: &url.URL{Scheme: u.Scheme, Host: u.Host}, idle: o.StreamIdle, maxStream: o.StreamMax, transcripts: make(chan struct{}, maxTranscriptReads), capture: o.Capture}
	c.call = o.CallTimeout
	if c.call <= 0 {
		c.call = callTimeout
	}
	if c.idle <= 0 {
		c.idle = defaultStreamIdle
	}
	if c.maxStream <= 0 {
		c.maxStream = defaultStreamMax
	}
	// No client-level Timeout: it would cut a healthy stream. Unary calls get a
	// context deadline instead; the stream gets an idle watchdog.
	c.http = &http.Client{Transport: tr, CheckRedirect: noRedirect}
	return c, nil
}

// do sends one request for the binding. A redirect, a transport failure or an
// unexpected status is mapped to an error code; the response body is only read
// by the caller on success.
func (c *Client) do(ctx context.Context, b Binding, method, path string, hdr map[string]string, body any) (*http.Response, error) {
	began := time.Now()
	resp, err := c.send(ctx, b, method, path, hdr, body)
	obs.Upstream(time.Since(began)) // to the response headers; a stream's own length is not latency
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return nil, statusError(resp.StatusCode)
}

// send is do without the status mapping: it returns whatever Hermes answered, so a caller that
// must tell two failures with the same status apart (a delete's 404) can read the body. The caller
// closes the body. A transport failure is an error; a redirect is not followed (noRedirect) and arrives as an
// ordinary 3xx response, which a caller maps like any other unexpected status (DeleteSession: HERMES_UNAVAILABLE).
func (c *Client) send(ctx context.Context, b Binding, method, path string, hdr map[string]string, body any) (*http.Response, error) {
	if !ValidSlug(b.Profile) || b.Key == "" {
		return nil, fail(CodeTenantNotReady)
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fail(CodeHermesUnavailable)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+"/p/"+b.Profile+path, rd)
	if err != nil {
		return nil, fail(CodeHermesUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+b.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	return resp, nil
}

// transportError classifies a failed round trip without ever formatting err:
// a *url.Error carries the full request URL.
func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ctx.Err() // the caller cancelled; not an upstream fault
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fail(CodeUpstreamTimeout)
	}
	return fail(CodeHermesUnavailable)
}

// statusError is the single status-to-code table (PRD §9.1).
func statusError(status int) error {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		// Our own key was refused: the binding is not usable, whatever the
		// database believes.
		return fail(CodeTenantNotReady)
	case status == http.StatusTooManyRequests:
		return fail(CodeRateLimited)
	case status == http.StatusConflict:
		return fail(CodeRunConflict)
	case status == http.StatusRequestTimeout, status == http.StatusGatewayTimeout:
		return fail(CodeUpstreamTimeout)
	case status == http.StatusNotFound, status == http.StatusMethodNotAllowed, status == http.StatusNotImplemented:
		return fail(CodeCapabilityNA)
	default:
		return fail(CodeHermesUnavailable)
	}
}

// ErrRunGone is a run lookup that Hermes answers with 404: the run is unknown
// upstream (restarted, expired). It is not a capability problem.
var ErrRunGone = errors.New("hermes: run not found upstream")

func readJSON(resp *http.Response, v any, limit int64) error {
	return readJSONThen(resp, v, limit, nil)
}

// readJSONThen is readJSON that also hands the raw body to seen (the fixture capture) before decoding it.
func readJSONThen(resp *http.Response, v any, limit int64, seen func([]byte)) error {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return fail(CodeHermesUnavailable)
	}
	if seen != nil {
		seen(raw)
	}
	if json.Unmarshal(raw, v) != nil {
		return fail(CodeHermesUnavailable)
	}
	return nil
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.call)
}

// CreateSession opens a Hermes session under the binding's profile (PRD
// §9A.2 POST /api/sessions) and returns its id.
func (c *Client) CreateSession(ctx context.Context, b Binding) (string, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodPost, "/api/sessions", nil, map[string]any{})
	if err != nil {
		return "", err
	}
	var d struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		Session   struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := readJSON(resp, &d, maxAPIResponse); err != nil {
		return "", err
	}
	for _, id := range []string{d.ID, d.SessionID, d.Session.ID} {
		if ValidID(id) {
			return id, nil
		}
	}
	return "", fail(CodeHermesUnavailable)
}

// ErrSessionGone is a session delete that Hermes answers with its own session_not_found: the
// session does not exist upstream (already deleted). It is not a missing route or a bad binding.
var ErrSessionGone = errors.New("hermes: session not found upstream")

// SessionDeleteAdvertised reports whether this Hermes lists DELETE /api/sessions/{session_id} as
// "session_delete" in /v1/capabilities. Anything unclear is false, and so is "no": an upstream
// delete is only ever sent to a route Hermes itself advertised.
func (c *Client) SessionDeleteAdvertised(ctx context.Context, b Binding) (bool, error) {
	caps, ok, tenant := c.discoveryDoc(ctx, b)
	if tenant {
		return false, fail(CodeTenantNotReady)
	}
	return ok && caps.hasRoute("session_delete", http.MethodDelete, "/api/sessions/{session_id}"), nil
}

// DeleteSession deletes the session upstream (DELETE /api/sessions/{id}; gateway/platforms/api_server.py
// _handle_delete_session). Hermes answers 200 {"object":"hermes.session.deleted","deleted":bool},
// 404 session_not_found for an id it does not hold, 409 session_active_turn while a turn is writing
// to it, and 503 when its session database is unavailable. Success is only a 200 that says
// deleted:true. A 404 with Hermes' session_not_found code is ErrSessionGone; any other 404 (an
// unknown profile route, say) is not evidence the session is gone and is CAPABILITY_UNAVAILABLE.
// A 409 is RUN_CONFLICT. A 200 that does not say deleted:true is HERMES_UNAVAILABLE, never success.
func (c *Client) DeleteSession(ctx context.Context, b Binding, sessionID string) error {
	if !ValidID(sessionID) {
		return fail(CodeHermesUnavailable)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.send(ctx, b, http.MethodDelete, "/api/sessions/"+url.PathEscape(sessionID), nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		var d struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if readJSON(resp, &d, 4096) == nil && d.Error.Code == "session_not_found" {
			return ErrSessionGone
		}
		return fail(CodeCapabilityNA)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return statusError(resp.StatusCode)
	}
	var d struct {
		Deleted bool `json:"deleted"`
	}
	if err := readJSON(resp, &d, 4096); err != nil || !d.Deleted {
		return fail(CodeHermesUnavailable)
	}
	return nil
}

// RunRequest is the server-built body of POST /v1/runs. SessionKey is derived
// by the server from the account; IdempotencyKey is generated per run.
type RunRequest struct {
	SessionID      string
	Input          string
	SessionKey     string
	IdempotencyKey string
	// Optional per-run model choice, sent as body provider/model/model_options (the fields
	// _request_agent_overrides reads). Empty means the profile's default.
	Provider, Model, Reasoning, ServiceTier string
	// Instructions are the user's own per-run instructions (#111), sent as body "instructions": Hermes appends them
	// to the profile's system prompt for this run only. Empty sends nothing.
	Instructions string
	// Images are the user's own staged images for this turn, already validated and owner-resolved
	// by the caller. They travel inline as data URLs (gateway/platforms/api_server.py accepts
	// data:image/... only); Hermes receives nothing it can fetch from Antares.
	Images []Image
}

// Image is one validated raster image (png, jpeg or gif) to send with the turn's text.
type Image struct {
	MediaType string
	Data      []byte
}

// CreateRun starts a run (PRD §6.2 step 4) and returns Hermes' run id.
func (c *Client) CreateRun(ctx context.Context, b Binding, r RunRequest) (string, error) {
	if !ValidID(r.SessionID) || r.Input == "" || len(r.SessionKey) > maxSessionKeyLen || len(r.IdempotencyKey) > maxIdempotencyLen {
		return "", fail(CodeHermesUnavailable)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodPost, "/v1/runs", map[string]string{
		"Idempotency-Key":      r.IdempotencyKey,
		"X-Hermes-Session-Key": r.SessionKey,
	}, runBody(r))
	if err != nil {
		return "", err
	}
	var d struct {
		RunID string `json:"run_id"`
	}
	if err := readJSONThen(resp, &d, maxAPIResponse, func(raw []byte) { c.capture.body(ctx, b, "run-create", raw) }); err != nil {
		return "", err
	}
	if !ValidID(d.RunID) {
		return "", fail(CodeHermesUnavailable)
	}
	return d.RunID, nil
}

// Upstream run states, reduced. Unknown is not completed and not failed
// (PRD RUN-001): anything unrecognised lands here.
const (
	StateRunning   = "running"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateUnknown   = "unknown"
)

// RunStatus is Hermes' own view of a run.
type RunStatus struct {
	State       string
	Output      string
	Interrupted bool // terminal, but not by completing
	Usage       *Usage
}

// Terminal reports a state that will not change.
func (s RunStatus) Terminal() bool {
	return s.State == StateCompleted || s.State == StateFailed || s.State == StateCancelled
}

func reduceState(raw string) (state string, interrupted bool) {
	switch strings.ToLower(raw) {
	case "queued", "pending", "started", "running", "in_progress", "stopping", "waiting", "waiting_for_approval":
		return StateRunning, false
	case "completed", "complete", "succeeded", "success":
		return StateCompleted, false
	case "failed", "error", "errored":
		return StateFailed, false
	case "interrupted":
		return StateFailed, true
	case "cancelled", "canceled", "stopped":
		return StateCancelled, false
	}
	return StateUnknown, false
}

// GetRun reads the run's terminal-or-not state (GET /v1/runs/{id}). It is the
// source of truth after a dropped stream (PRD §6.2 step 6).
func (c *Client) GetRun(ctx context.Context, b Binding, runID string) (RunStatus, error) {
	if !ValidID(runID) {
		return RunStatus{}, fail(CodeHermesUnavailable)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodGet, "/v1/runs/"+url.PathEscape(runID), nil, nil)
	if err != nil {
		if CodeOf(err) == CodeCapabilityNA {
			return RunStatus{}, ErrRunGone
		}
		return RunStatus{}, err
	}
	var d struct {
		Status  string `json:"status"`
		Output  any    `json:"output"`
		Usage   any    `json:"usage"` // any: a malformed block must not fail the status read
		Runtime any    `json:"runtime"`
	}
	if err := readJSON(resp, &d, maxOutput+maxAPIResponse); err != nil {
		return RunStatus{}, err
	}
	st, intr := reduceState(d.Status)
	out, _ := d.Output.(string)
	if len(out) > maxOutput {
		out = ""
	}
	return RunStatus{State: st, Output: out, Interrupted: intr, Usage: parseUsage(asObject(d.Usage), asObject(d.Runtime))}, nil
}

// StopRun asks Hermes to stop the run (POST /v1/runs/{id}/stop). Stopping is
// not instantly terminal upstream; the caller records its own state.
func (c *Client) StopRun(ctx context.Context, b Binding, runID string) error {
	if !ValidID(runID) {
		return fail(CodeHermesUnavailable)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/stop", nil, map[string]any{})
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.Body.Close()
}

// SteerRun sends guidance into a running run (POST /v1/runs/{id}/steer). Hermes
// answers 409 when the run is not accepting steer text (finished, stopping, or the
// agent declined it); that is STEER_REFUSED, not a run conflict. The text is the
// user's own and is sent as "input", the first field Hermes reads.
func (c *Client) SteerRun(ctx context.Context, b Binding, runID, text string) error {
	if !ValidID(runID) {
		return fail(CodeHermesUnavailable)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/steer", nil, map[string]string{"input": text})
	if err != nil {
		if CodeOf(err) == CodeRunConflict {
			return fail(CodeSteerRefused)
		}
		return err
	}
	var out struct {
		Accepted bool `json:"accepted"`
	}
	err = readJSON(resp, &out, maxAPIResponse)
	if err != nil || !out.Accepted {
		return fail(CodeSteerRefused)
	}
	return nil
}

// RespondApproval delivers the user's decision on a pending approval (POST /v1/runs/{id}/approval).
// requestID and generation are the exact values Hermes sent with the prompt; Hermes' authority mode
// accepts exactly {request_id, execution_generation, choice}, its legacy mode {choice, request_id}, so
// the generation is sent only when the prompt carried one. 409 (not pending, stale generation) is
// PROMPT_STALE: the decision is gone upstream, nothing was applied.
func (c *Client) RespondApproval(ctx context.Context, b Binding, runID, requestID string, generation *int64, choice string) error {
	body := map[string]any{"request_id": requestID, "choice": choice}
	if generation != nil {
		body["execution_generation"] = *generation
	}
	return c.respond(ctx, b, runID, "approval", body)
}

// RespondClarify delivers the user's answer to a pending clarification (POST /v1/runs/{id}/clarify).
// Hermes accepts it only with the exact {request_id, execution_generation, answer}, so a prompt that
// carried no generation cannot be answered and the call is refused before anything is sent.
func (c *Client) RespondClarify(ctx context.Context, b Binding, runID, requestID string, generation *int64, answer string) error {
	if generation == nil {
		return fail(CodeCapabilityNA)
	}
	return c.respond(ctx, b, runID, "clarify", map[string]any{"request_id": requestID, "execution_generation": *generation, "answer": answer})
}

func (c *Client) respond(ctx context.Context, b Binding, runID, route string, body map[string]any) error {
	if !ValidID(runID) || !ValidID(body["request_id"].(string)) {
		return fail(CodeHermesUnavailable)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/"+route, nil, body)
	if err != nil {
		if CodeOf(err) == CodeRunConflict {
			return fail(CodePromptStale)
		}
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.Body.Close()
}

// Advertises reports whether Hermes lists a POST route under the endpoint name in /v1/capabilities.
// Anything unclear is false: an answer is only ever sent to a route Hermes itself advertised, with one
// explicit exception (provenRoutes): a route the deployed Hermes serves without listing it, accepted only
// once a probe has shown Hermes' own handler answering on it.
func (c *Client) Advertises(ctx context.Context, b Binding, endpoint string) (bool, error) {
	caps, ok, tenant := c.discoveryDoc(ctx, b)
	if tenant {
		return false, fail(CodeTenantNotReady)
	}
	if ok && caps.hasPost(endpoint) {
		return true, nil
	}
	return ok && c.proven(ctx, b, endpoint), nil
}

// provenRoutes are POST routes Hermes v0.22.5 registers but leaves out of /v1/capabilities' endpoint table
// (gateway/platforms/api_server_runs.py registers POST /v1/runs/{run_id}/clarify; api_server.py's
// _CAPABILITY_ENDPOINTS has no run_clarify). Only these are ever probed.
var provenRoutes = map[string]string{"run_clarify": "clarify"}

// proveRunID names no run: it only has to pass Hermes' own run lookup, which answers 404 run_not_found.
const proveRunID = "run_0000000000000000antaresprobe"

// proven asks the route about a run that cannot exist. Hermes' own handler answers 404 with
// {"error":{"code":"run_not_found"}}; a route Hermes does not serve answers a bare 404 or 405, which is not proof.
// Nothing can be changed by it: there is no such run.
func (c *Client) proven(ctx context.Context, b Binding, endpoint string) bool {
	tail, ok := provenRoutes[endpoint]
	if !ok {
		return false
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.send(ctx, b, http.MethodPost, "/v1/runs/"+proveRunID+"/"+tail, nil, map[string]any{})
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusNotFound && json.Unmarshal(raw, &e) == nil && e.Error.Code == "run_not_found"
}

// Event is one upstream SSE frame, undecoded.
type Event struct {
	Name string // the SSE "event:" field, if any
	Data []byte // the joined "data:" lines
}

// StreamEvents reads GET /v1/runs/{id}/events and calls fn for each frame
// until the stream ends, fn returns an error, or ctx is cancelled. It returns
// nil on a clean end of stream (which is NOT evidence the run finished: the
// caller reconciles with GetRun). Reads are bounded (line, frame), guarded by
// an idle watchdog and a hard overall limit.
func (c *Client) StreamEvents(ctx context.Context, b Binding, runID string, fn func(Event) error) error {
	if !ValidID(runID) {
		return fail(CodeHermesUnavailable)
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, c.maxStream)
	defer cancel()
	idle := time.AfterFunc(c.idle, cancel)
	defer idle.Stop()

	resp, err := c.do(ctx, b, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/events", map[string]string{"Accept": "text/event-stream"}, nil)
	if err != nil {
		return streamErr(parent, ctx, err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return fail(CodeHermesUnavailable)
	}
	rd := bufio.NewReaderSize(resp.Body, 32<<10)
	var name string
	var data []byte
	flush := func() error {
		if name == "" && data == nil {
			return nil
		}
		ev := Event{Name: name, Data: bytes.TrimSuffix(data, []byte("\n"))}
		name, data = "", nil
		c.capture.frame(parent, b, ev)
		return fn(ev)
	}
	for {
		line, err := readLine(rd)
		idle.Reset(c.idle)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return streamErr(parent, ctx, err)
		}
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			chunk := strings.TrimPrefix(line[len("data:"):], " ")
			if len(data)+len(chunk)+1 > maxEventData {
				return fail(CodeHermesUnavailable)
			}
			data = append(data, chunk...)
			data = append(data, '\n')
		}
	}
}

// streamErr maps a failed read. The idle watchdog and the overall limit cancel
// the derived ctx only, so the parent tells "the caller stopped it" (returned
// as the context error, for the worker to recognise) from "it went quiet or
// broke" (a timeout or unavailable code).
func streamErr(parent, ctx context.Context, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if ctx.Err() != nil {
		return fail(CodeUpstreamTimeout)
	}
	return transportError(ctx, err)
}

// readLine returns one line without its terminator, refusing lines over
// maxLine so a hostile or broken upstream cannot grow memory without bound.
func readLine(rd *bufio.Reader) (string, error) {
	var buf []byte
	for {
		part, isPrefix, err := rd.ReadLine()
		if err != nil {
			return "", err
		}
		if len(buf)+len(part) > maxLine {
			return "", fail(CodeHermesUnavailable)
		}
		buf = append(buf, part...)
		if !isPrefix {
			return string(buf), nil
		}
	}
}

// Message is one transcript turn, reduced to what the chat UI shows.
type Message struct {
	// Seq is the message's position in the session transcript (0 = oldest). It
	// is the stable turn identity the history cursor is built on.
	Seq       int    `json:"seq"`
	ID        string `json:"id,omitempty"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt,omitempty"`
	// Ref is the turn hash the history API stamps on a page (never read from Hermes). A
	// client quotes it back to name this exact turn; see chat.turnHash.
	Ref string `json:"ref,omitempty"`
	// Images counts the image parts of a user turn (Hermes renders each as "[image]" in the
	// history read). Attachments are the owner's assets matched to them by the chat layer.
	Images      int          `json:"images,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is one image the user sent with a turn. Available is false once the staged
// asset was deleted: the turn keeps its record, the preview is gone.
type Attachment struct {
	AssetID   string `json:"assetId"`
	MediaType string `json:"mediaType"`
	Available bool   `json:"available"`
}

// ErrHistoryTooLarge means the transcript exceeds what the adapter will hold.
// It is an error, never a silent truncation (PRD HIST-003).
var ErrHistoryTooLarge = fail(CodeHermesUnavailable)

// Messages returns the newest limit turns of the session (see Transcript).
func (c *Client) Messages(ctx context.Context, b Binding, sessionID string, limit int) (msgs []Message, truncated bool, err error) {
	if limit <= 0 || limit > maxMessagesPerPage {
		limit = 50
	}
	all, err := c.Transcript(ctx, b, sessionID)
	if err != nil {
		return nil, false, err
	}
	if len(all) > limit {
		return all[len(all)-limit:], true, nil
	}
	return all, false, nil
}

// Transcript reads the whole session transcript (GET /api/sessions/{id}/messages)
// and keeps only user/assistant text, in upstream order, numbered from 0. Hermes
// has no cursor of its own, so paging is the caller's job, over this bounded
// read. A body or message count over the limits is an error, not a truncation.
// At most maxTranscriptReads run at once.
func (c *Client) Transcript(ctx context.Context, b Binding, sessionID string) ([]Message, error) {
	if !ValidID(sessionID) {
		return nil, fail(CodeHermesUnavailable)
	}
	select {
	case c.transcripts <- struct{}{}:
		defer func() { <-c.transcripts }()
	case <-ctx.Done():
		return nil, fail(CodeUpstreamTimeout)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, b, http.MethodGet, "/api/sessions/"+url.PathEscape(sessionID)+"/messages?include_compacted=true&inline_images=false", nil, nil)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMessagesBody+1))
	resp.Body.Close()
	if err != nil || len(raw) > maxMessagesBody {
		return nil, fail(CodeHermesUnavailable)
	}
	c.capture.body(ctx, b, "session-messages", raw)
	var list []json.RawMessage
	var wrapped struct {
		Messages []json.RawMessage `json:"messages"`
		Data     []json.RawMessage `json:"data"`
	}
	switch {
	case json.Unmarshal(raw, &list) == nil && list != nil:
	case json.Unmarshal(raw, &wrapped) == nil && wrapped.Messages != nil:
		list = wrapped.Messages
	case wrapped.Data != nil:
		list = wrapped.Data
	default:
		return nil, fail(CodeHermesUnavailable)
	}
	raw = nil
	var msgs []Message
	for _, item := range list {
		var m struct {
			ID        any    `json:"id"`
			Role      string `json:"role"`
			Content   any    `json:"content"`
			CreatedAt any    `json:"created_at"`
			Timestamp any    `json:"timestamp"`
		}
		if json.Unmarshal(item, &m) != nil || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		if len(msgs) >= maxTranscriptMsgs {
			return nil, ErrHistoryTooLarge
		}
		out := Message{Seq: len(msgs), Role: m.Role, Text: contentText(m.Content)}
		if s, ok := m.ID.(string); ok && ValidID(s) {
			out.ID = s
		}
		for _, t := range []any{m.CreatedAt, m.Timestamp} {
			if s, ok := t.(string); ok && len(s) <= 64 {
				out.CreatedAt = s
				break
			}
		}
		msgs = append(msgs, out)
	}
	return msgs, nil
}

// contentText takes a plain string, or the text parts of a content array.
// Anything else (images, files) becomes empty: shown as nothing, never guessed.
func contentText(v any) string {
	var s string
	switch c := v.(type) {
	case string:
		s = c
	case []any:
		var parts []string
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		s = strings.Join(parts, "\n")
	}
	if len(s) > maxMessageText {
		s = s[:maxMessageText]
	}
	return strings.ToValidUTF8(s, "�")
}

// runBody builds the POST /v1/runs body. The model fields are added only when set, so a
// chat with no choice sends exactly what it always did.
func runBody(r RunRequest) map[string]any {
	body := map[string]any{"session_id": r.SessionID, "input": r.Input}
	if r.Instructions != "" {
		body["instructions"] = r.Instructions
	}
	if len(r.Images) > 0 {
		// POST /v1/runs reads the last message's content and hands it to the agent without
		// normalising it (api_server_runs.py _run_user_message), so the canonical parts the
		// agent pipeline understands are sent directly: "text" and "image_url".
		parts := []map[string]any{{"type": "text", "text": r.Input}}
		for _, im := range r.Images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{
				"url": "data:" + im.MediaType + ";base64," + base64.StdEncoding.EncodeToString(im.Data)}})
		}
		body["input"] = []map[string]any{{"role": "user", "content": parts}}
	}
	if r.Model != "" {
		body["model"] = r.Model
		if r.Provider != "" {
			body["provider"] = r.Provider
		}
	}
	opts := map[string]any{}
	if r.Reasoning != "" {
		opts["reasoning"] = map[string]any{"enabled": true, "effort": r.Reasoning}
	}
	if r.ServiceTier != "" {
		opts["service_tier"] = r.ServiceTier
	}
	if len(opts) > 0 {
		body["model_options"] = opts
	}
	return body
}
