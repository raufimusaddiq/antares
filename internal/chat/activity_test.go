package chat

import (
	"strings"
	"testing"
)

// #163: the browser learns that the assistant is thinking, never what it thinks, and a tool's end
// carries Hermes' failure flag and duration with its redacted preview.
func TestActivityEventsCarryStageAndToolOutcomeButNoReasoning(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "look it up")
	_, fr := a.hx.runWithInput(t, "look it up")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "reasoning.available", `{"text":"PRIVATE-REASONING-81c2"}`)
	a.hx.push(fr, "tool.started", `{"tool":"web_search","preview":"q","tool_call_id":"c1"}`)
	a.hx.push(fr, "tool.completed", `{"tool":"web_search","duration":2.5,"error":true,"preview":"timed out","tool_call_id":"c1"}`)
	a.hx.push(fr, "run.completed", `{"output":"sorry"}`)
	s.waitFor(t, "run.completed")
	<-s.done

	if got := strings.Join(s.names(), ","); got != "run.started,run.thinking,tool.started,tool.completed,run.completed" {
		t.Errorf("events %s", got)
	}
	for _, e := range s.events() {
		if strings.Contains(e.Data, "PRIVATE-REASONING") {
			t.Fatalf("reasoning text reached the browser: %s %s", e.Name, e.Data)
		}
		if e.Name == "tool.completed" && (!strings.Contains(e.Data, `"failed":true`) || !strings.Contains(e.Data, `"durationMs":2500`) || !strings.Contains(e.Data, `"summary":"timed out"`)) {
			t.Errorf("tool.completed = %s", e.Data)
		}
	}
}
