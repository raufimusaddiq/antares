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
	KindApproval      // Hermes is waiting for the user's consent (approval.request)
	KindClarify       // Hermes is waiting for the user's answer to a question (clarify.request)
	KindPromptSettled // Hermes closed a pending approval or question (…settled, approval.responded)
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
	Usage   *Usage // KindTerminal: what Hermes reported, nil when it reported none

	// KindApproval / KindClarify / KindPromptSettled. PromptID is the exact upstream request id (empty
	// when the frame carried none, which makes the prompt unanswerable); Gen is the execution
	// generation when Hermes sent one. Text is the redacted command or the question.
	PromptID string
	Gen      *int64
	Detail   string
	Choices  []string
}

const (
	maxDelta   = 64 << 10
	maxLabel   = 200
	maxSummary = 2000
	maxPrompt  = 2000
	maxChoices = 8
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
	usage := func() *Usage {
		u, _ := d["usage"].(map[string]any)
		r, _ := d["runtime"].(map[string]any)
		return parseUsage(u, r)
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
	case "approval.request", "approval.required", "approval.requested", "run.approval_required":
		n := promptFrame(KindApproval, d)
		n.Text, n.Detail = clip(firstString(d, "command"), maxPrompt), clip(firstString(d, "description"), maxPrompt)
		return n
	case "clarify.request":
		n := promptFrame(KindClarify, d)
		n.Text = clip(firstString(d, "question"), maxPrompt)
		return n
	case "approval.settled", "clarify.settled", "approval.responded":
		return promptFrame(KindPromptSettled, d)
	case "run.completed":
		return Normalized{Kind: KindTerminal, State: StateCompleted, Output: clip(firstString(d, "output", "text"), maxOutput), Usage: usage()}
	case "run.failed":
		return Normalized{Kind: KindTerminal, State: StateFailed, Usage: usage()}
	case "run.cancelled", "run.canceled":
		return Normalized{Kind: KindTerminal, State: StateCancelled, Usage: usage()}
	case "run.interrupted":
		return Normalized{Kind: KindTerminal, State: StateFailed, Intr: true, Usage: usage()}
	}
	return Normalized{Kind: KindIgnore}
}

// promptFrame reads the fields every approval and clarification frame shares. Hermes' run stream names
// the id "request_id" or, for the session authority's prompts, "prompt_id".
func promptFrame(k Kind, d map[string]any) Normalized {
	n := Normalized{Kind: k}
	for _, key := range []string{"request_id", "prompt_id"} {
		if s, ok := d[key].(string); ok && ValidID(s) {
			n.PromptID = s
			break
		}
	}
	if g, ok := d["execution_generation"].(float64); ok && g >= 0 && g == float64(int64(g)) {
		v := int64(g)
		n.Gen = &v
	}
	if raw, ok := d["choices"].([]any); ok {
		for _, c := range raw {
			if s, ok := c.(string); ok && len(n.Choices) < maxChoices && s != "" {
				n.Choices = append(n.Choices, clip(s, maxLabel))
			}
		}
	}
	return n
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
