package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #169: what the capture writes is shape only. Ids, text, keys and URLs never survive; event and status names do.
func TestSanitizeKeepsShapeAndDropsContent(t *testing.T) {
	raw := `{"event":"tool.completed","run_id":"run_SECRET1","tool":"web_search","duration":2.5,"error":true,"preview":"sk-SECRET2 https://x.example/a",
		"usage":{"input_tokens":120,"status":"completed"},"items":[{"role":"user","content":"SECRET3"},{"role":"assistant"},{"role":"tool"},{"role":"user"}],"none":null,
		"status":"Some Free Text"}`
	got := string(Sanitize([]byte(raw)))
	for _, secret := range []string{"SECRET1", "SECRET2", "SECRET3", "x.example", "web_search", "Some Free Text"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q survived: %s", secret, got)
		}
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	if v["event"] != "tool.completed" || v["duration"] != float64(0) || v["error"] != true || v["preview"] != "<string>" || v["none"] != nil || v["status"] != "<string>" {
		t.Errorf("shape: %s", got)
	}
	if u := v["usage"].(map[string]any); u["status"] != "completed" || u["input_tokens"] != float64(0) {
		t.Errorf("nested: %v", u)
	}
	if items := v["items"].([]any); len(items) != 3 || items[0].(map[string]any)["role"] != "user" {
		t.Errorf("array: %v", items)
	}
	if string(Sanitize([]byte("not json"))) != `"<not json>"` {
		t.Error("non-JSON must become a marker")
	}
}

// Only an allowed profile is recorded; a nil or unconfigured capture records nothing and never panics.
func TestCaptureRecordsOnlyAllowedProfiles(t *testing.T) {
	dir := t.TempDir()
	c := &Capture{Dir: dir, Allow: func(_ context.Context, p string) bool { return p == "u_synthetic" }}
	ev := Event{Name: "message.delta", Data: []byte(`{"delta":"REAL-USER-TEXT"}`)}
	c.frame(context.Background(), Binding{Profile: "u_realuser"}, ev)
	if _, err := os.Stat(filepath.Join(dir, "run-events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a real user's frame was recorded: %v", err)
	}
	c.frame(context.Background(), Binding{Profile: "u_synthetic"}, ev)
	c.body(context.Background(), Binding{Profile: "u_synthetic"}, "run-create", []byte(`{"run_id":"run_X","status":"queued"}`))
	got, err := os.ReadFile(filepath.Join(dir, "run-events.jsonl"))
	if err != nil || strings.Contains(string(got), "REAL-USER-TEXT") || !strings.Contains(string(got), `"event":"message.delta"`) {
		t.Errorf("frame: %s %v", got, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "run-create.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("body file: %v %v", fi, err)
	}
	var off *Capture
	off.frame(context.Background(), Binding{Profile: "u_synthetic"}, ev) // nil: off
	(&Capture{}).body(context.Background(), Binding{Profile: "u_synthetic"}, "x", []byte(`{}`))
}

// The contract check (#169): once docs/hermes/fixtures/wire/run-events.jsonl is captured from a smoke run, every field the
// normalizer reads must be there with the type it expects, and the events the composer, activity timeline and run
// lifecycle rely on must all appear. A missing event or a changed type fails here, loudly, before any UI is built on it.
func TestRecordedWireFramesMatchWhatNormalizeReads(t *testing.T) {
	raw, err := os.ReadFile("../../docs/hermes/fixtures/wire/run-events.jsonl")
	if os.IsNotExist(err) {
		t.Skip("no recorded wire fixtures yet: capture them from a smoke run (docs/runbooks/wire-fixtures.md)")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range wireDrift(raw) {
		t.Error(p)
	}
}

// The checker itself, on a recorded-looking run and on drifted ones, so it is proven before real fixtures exist.
func TestWireDriftCheck(t *testing.T) {
	good := `{"event":"message.delta","data":{"delta":"<string>"}}
{"event":"tool.started","data":{"tool":"<string>","preview":"<string>","tool_call_id":"<string>"}}
{"event":"tool.completed","data":{"tool":"<string>","duration":0,"error":false,"preview":"<string>"}}
{"event":"run.completed","data":{"output":"<string>"}}`
	if p := wireDrift([]byte(good)); len(p) != 0 {
		t.Fatalf("a good capture reported drift: %v", p)
	}
	for name, bad := range map[string]string{
		"duration became a string": strings.Replace(good, `"duration":0`, `"duration":"<string>"`, 1),
		"error dropped":            strings.Replace(good, `"error":false,`, ``, 1),
		"no tool events":           strings.Join(strings.Split(good, "\n")[:1], "\n") + "\n" + strings.Split(good, "\n")[3],
		"not a frame":              good + "\nnot json",
	} {
		if p := wireDrift([]byte(bad)); len(p) == 0 {
			t.Errorf("%s: no drift reported", name)
		}
	}
}

// wireDrift lists every way a recorded run differs from what Normalize reads; empty means no drift.
func wireDrift(raw []byte) []string {
	// event -> member -> JSON type after sanitizing; "?" marks a member Hermes may omit.
	want := map[string]map[string]string{
		"message.delta":       {"delta": "string"},
		"tool.started":        {"tool": "string", "tool_call_id?": "string"},
		"tool.completed":      {"tool": "string", "duration": "number", "error": "bool", "preview": "string", "tool_call_id?": "string"},
		"reasoning.available": {"text": "string"},
		"run.completed":       {"output?": "string"},
	}
	var problems []string
	seen := map[string]bool{}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var f struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		var data map[string]any
		if json.Unmarshal([]byte(line), &f) != nil || json.Unmarshal(f.Data, &data) != nil {
			problems = append(problems, fmt.Sprintf("line %d is not a recorded frame", i+1))
			continue
		}
		seen[f.Event] = true
		if want[f.Event] != nil && Normalize(Event{Name: f.Event, Data: f.Data}).Kind == KindIgnore {
			problems = append(problems, fmt.Sprintf("line %d: Normalize ignores %s", i+1, f.Event))
		}
		for member, typ := range want[f.Event] {
			name, optional := strings.CutSuffix(member, "?")
			v, ok := data[name]
			if !ok {
				if !optional {
					problems = append(problems, fmt.Sprintf("line %d: %s lacks %q, which Normalize reads", i+1, f.Event, name))
				}
				continue
			}
			if got := jsonType(v); got != typ {
				problems = append(problems, fmt.Sprintf("line %d: %s.%s is %s, Normalize expects %s", i+1, f.Event, name, got, typ))
			}
		}
	}
	for _, e := range []string{"message.delta", "tool.started", "tool.completed", "run.completed"} {
		if !seen[e] {
			problems = append(problems, "the recorded run never sent "+e+": re-capture with a prompt that makes the assistant use a tool")
		}
	}
	return problems
}

func jsonType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	case []any:
		return "array"
	}
	return "object"
}
