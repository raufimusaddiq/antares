package blog

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// fakeHermes is the part of Hermes a check talks to, scripted: what each run emits, and what fails. It records every call
// with the binding it was made under, so a test can prove whose assistant did the work.
type fakeHermes struct {
	mu       sync.Mutex
	sessions int
	runs     []hermes.RunRequest
	bindings []hermes.Binding
	stopped  []string
	nextRun  int

	// script is what a run's event stream does. nil: finish at once with output.
	script func(ctx context.Context, emit func(name string, data map[string]any) error) error
	// output is the final text of a run that completes.
	output string

	createSessionErr, createRunErr, getRunErr, discoverErr error
	status                                                 hermes.RunStatus
	toolsets                                               []hermes.Toolset
}

func newFake() *fakeHermes {
	return &fakeHermes{toolsets: []hermes.Toolset{{Name: "web", Status: hermes.StatusAvailable}}}
}

func (f *fakeHermes) note(b hermes.Binding) {
	f.bindings = append(f.bindings, b)
}

func (f *fakeHermes) CreateSession(ctx context.Context, b hermes.Binding) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note(b)
	if f.createSessionErr != nil {
		return "", f.createSessionErr
	}
	f.sessions++
	return "sess" + string(rune('a'+f.sessions)) + "00000000", nil
}

func (f *fakeHermes) CreateRun(ctx context.Context, b hermes.Binding, r hermes.RunRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note(b)
	if f.createRunErr != nil {
		return "", f.createRunErr
	}
	f.runs = append(f.runs, r)
	f.nextRun++
	return "run" + string(rune('a'+f.nextRun)) + "00000000", nil
}

func (f *fakeHermes) StreamEvents(ctx context.Context, b hermes.Binding, runID string, fn func(hermes.Event) error) error {
	f.mu.Lock()
	f.note(b)
	script, output := f.script, f.output
	f.mu.Unlock()
	emit := func(name string, data map[string]any) error {
		data["event"] = name
		raw, _ := json.Marshal(data)
		return fn(hermes.Event{Name: name, Data: raw})
	}
	if script != nil {
		return script(ctx, emit)
	}
	_ = emit("tool.started", map[string]any{"tool": "web_search", "preview": "a query", "tool_call_id": "c1"})
	_ = emit("tool.completed", map[string]any{"tool": "web_search", "preview": "", "error": false, "tool_call_id": "c1"})
	return emit("run.completed", map[string]any{"output": output})
}

func (f *fakeHermes) GetRun(ctx context.Context, b hermes.Binding, runID string) (hermes.RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note(b)
	return f.status, f.getRunErr
}

func (f *fakeHermes) StopRun(ctx context.Context, b hermes.Binding, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note(b)
	f.stopped = append(f.stopped, runID)
	return nil
}

func (f *fakeHermes) Discover(ctx context.Context, b hermes.Binding) (hermes.Inventory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note(b)
	if f.discoverErr != nil {
		return hermes.Inventory{}, f.discoverErr
	}
	return hermes.Inventory{Toolsets: f.toolsets}, nil
}

func (f *fakeHermes) snapshot() (sessions int, runs []hermes.RunRequest, bindings []hermes.Binding, stopped []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions, append([]hermes.RunRequest(nil), f.runs...), append([]hermes.Binding(nil), f.bindings...), append([]string(nil), f.stopped...)
}

var errBoom = errors.New("boom")
