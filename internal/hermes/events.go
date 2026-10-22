package hermes

import (
	"encoding/json"
	"strings"
)

// Kind is what an upstream frame means to Antares.
type Kind int

const (
	KindIgnore Kind = iota // not understood, or not for the browser
	KindDelta
	KindToolStarted
	KindToolCompleted
	KindApproval // Hermes is waiting for a consent this build cannot collect
	KindTerminal
)

// Normalized is one upstream frame in Antares' vocabulary (PRD §6.3). Only
// bounded, plain-text fields survive; raw upstream JSON is never forwarded.
type Normalized struct {
	Kind    Kind
	Text    string // KindDelta
	ToolID  string // KindToolStarted / KindToolCompleted
	Label   string // KindToolStarted
	Summary string // KindToolCompleted
	State   string // KindTerminal: one of the State* constants
	Output  string // KindTerminal: final text, when Hermes sends it
	Intr    bool   // KindTerminal: ended by interruption
}

const (
	maxDelta   = 64 << 10
	maxLabel   = 200
	maxSummary = 2000
)

// Normalize maps one frame. The event type is the SSE event name, or the
// "event"/"type" member of the data object. Frames it does not recognise are
// KindIgnore: they are dropped, never guessed into something else — in
// particular an unrecognised frame can never be read as completion, because
// completion is only ever a recognised terminal frame or a GetRun reconcile.
func Normalize(ev Event) Normalized {
	var d map[string]any
	if len(ev.Data) > 0 {
		_ = json.Unmarshal(ev.Data, &d) // not an object: d stays nil, the name may still be enough
	}
	typ := strings.ToLower(ev.Name)
	if typ == "" {
		for _, k := range []string{"event", "type"} {
			if s, ok := d[k].(string); ok {
				typ = strings.ToLower(s)
				break
			}
		}
	}
	switch typ {
	case "assistant.delta", "message.delta", "response.output_text.delta":
		if t := firstString(d, "delta", "text", "content"); t != "" {
			return Normalized{Kind: KindDelta, Text: clip(t, maxDelta)}
		}
	case "tool.started", "tool_call.started", "hermes.tool.started":
		return Normalized{Kind: KindToolStarted, ToolID: toolID(d), Label: clip(firstString(d, "label", "name", "tool"), maxLabel)}
	case "tool.completed", "tool_call.completed", "hermes.tool.completed":
		return Normalized{Kind: KindToolCompleted, ToolID: toolID(d), Summary: clip(firstString(d, "summary", "result", "output"), maxSummary)}
	case "approval.required", "approval.requested", "run.approval_required":
		return Normalized{Kind: KindApproval}
	case "run.completed":
		return Normalized{Kind: KindTerminal, State: StateCompleted, Output: clip(firstString(d, "output", "text"), maxOutput)}
	case "run.failed":
		return Normalized{Kind: KindTerminal, State: StateFailed}
	case "run.cancelled", "run.canceled":
		return Normalized{Kind: KindTerminal, State: StateCancelled}
	case "run.interrupted":
		return Normalized{Kind: KindTerminal, State: StateFailed, Intr: true}
	}
	return Normalized{Kind: KindIgnore}
}

func toolID(d map[string]any) string {
	for _, k := range []string{"tool_call_id", "toolCallId", "id"} {
		if s, ok := d[k].(string); ok && ValidID(s) {
			return s
		}
	}
	return ""
}

func firstString(d map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := d[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// clip cuts to n bytes on a rune boundary and drops invalid UTF-8.
func clip(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "�")
}
