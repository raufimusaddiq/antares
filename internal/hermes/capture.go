package hermes

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Capture records Hermes wire data as sanitized test fixtures (issue #169). It is off unless the operator sets
// ANTARES_WIRE_CAPTURE_DIR, and even then it records only calls whose profile Allow accepts: in production that is a
// profile owned by a synthetic smoke account, so the capture rides the normal smoke run and never sees a real user.
//
// What is written is shape, not content, by allowlist: an object keeps only keys Antares knows from the Hermes contract
// (knownKeys); any other keys are dropped and counted as "<redacted keys>": N, because a key can itself be data (tool
// output keyed by a secret). A string becomes "<string>" unless it sits under an enum key and is one of the values listed
// for it (knownValues). Numbers become 0, booleans and nulls stay, arrays keep at most three sanitized elements.
// Each process start writes into its own timestamped subdirectory, so an old or interrupted capture is never mixed in.
type Capture struct {
	Dir   string
	Allow func(ctx context.Context, profile string) bool
	mu    sync.Mutex
	run   string // the subdirectory of this process's capture, made on first write
}

// knownKeys are the members Antares reads or documents in Hermes' run stream, run, session and message payloads.
var knownKeys = setOf("event", "type", "object", "status", "role", "run_id", "session_id", "id", "data", "messages",
	"delta", "text", "content", "output", "tool", "tool_call_id", "duration", "error", "preview", "already_streamed",
	"usage", "runtime", "input_tokens", "output_tokens", "total_tokens", "cache_read_tokens", "cache_write_tokens",
	"provider", "model", "request_id", "prompt_id", "execution_generation", "choices", "multi_select", "command",
	"description", "question", "accepted", "message", "created_at", "timestamp", "last_event", "pending_steer")

// knownValues are the enum values that may be kept verbatim, per key.
var knownValues = map[string]map[string]bool{
	"event": setOf("message.delta", "message.interim", "tool.started", "tool.completed", "reasoning.available",
		"run.queued", "run.started", "run.completed", "run.failed", "run.cancelled", "run.interrupted", "run.steered",
		"run.stopping", "subagent.start", "subagent.complete", "approval.request", "approval.required",
		"approval.requested", "approval.settled", "approval.responded", "clarify.request", "clarify.settled"),
	"status": setOf("queued", "pending", "started", "running", "in_progress", "stopping", "waiting", "completed",
		"failed", "cancelled", "interrupted", "succeeded"),
	"role":   setOf("user", "assistant", "tool", "system"),
	"object": setOf("hermes.run", "hermes.run.steer", "list"),
}

func setOf(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Sanitize returns the shape of a JSON document as described on Capture. Anything that is not JSON becomes a marker.
func Sanitize(raw []byte) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage(`"<not json>"`)
	}
	out, _ := json.Marshal(shape("", v))
	return out
}

func shape(key string, v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		dropped := 0
		for k, e := range x {
			if knownKeys[k] {
				m[k] = shape(k, e)
			} else {
				dropped++
			}
		}
		if dropped > 0 {
			m["<redacted keys>"] = dropped
		}
		return m
	case []any:
		n := min(len(x), 3)
		a := make([]any, 0, n)
		for _, e := range x[:n] {
			a = append(a, shape(key, e))
		}
		return a
	case string:
		if knownValues[cmp.Or(map[string]string{"type": "event"}[key], key)][x] {
			return x
		}
		return "<string>"
	case float64:
		return 0
	default: // bool, nil
		return x
	}
}

func (c *Capture) on(ctx context.Context, b Binding) bool {
	return c != nil && c.Dir != "" && c.Allow != nil && c.Allow(ctx, b.Profile)
}

// frame appends one run-stream event to run-events.jsonl.
func (c *Capture) frame(ctx context.Context, b Binding, ev Event) {
	if !c.on(ctx, b) {
		return
	}
	name := ev.Name
	if name == "" { // the name rides inside the data, as Normalize also accepts
		var d struct {
			Event string `json:"event"`
			Type  string `json:"type"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		name = cmp.Or(d.Event, d.Type)
	}
	line, _ := json.Marshal(map[string]any{"event": shape("event", name), "data": Sanitize(ev.Data)})
	c.write("run-events.jsonl", append(line, '\n'), true)
}

// body records the sanitized answer of one route under name (overwriting the previous capture of that route).
func (c *Capture) body(ctx context.Context, b Binding, name string, raw []byte) {
	if !c.on(ctx, b) {
		return
	}
	c.write(name+".json", append(Sanitize(raw), '\n'), false)
}

func (c *Capture) write(name string, data []byte, appendTo bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.run == "" {
		c.run = filepath.Join(c.Dir, time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(c.run, 0o700); err != nil {
		return
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(filepath.Join(c.run, name), flag, 0o600)
	if err != nil {
		return // a capture is best effort: it never fails a user's request
	}
	defer f.Close()
	_, _ = f.Write(data)
}
