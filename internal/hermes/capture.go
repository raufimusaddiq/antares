package hermes

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// Capture records Hermes wire data as sanitized test fixtures (issue #169). It is off unless the operator sets
// ANTARES_WIRE_CAPTURE_DIR, and even then it records only calls whose profile Allow accepts: in production that is a
// profile owned by a synthetic smoke account, so the capture rides the normal smoke run and never sees a real user.
//
// What is written is shape, not content: every object key is kept, every string value becomes "<string>" except the
// enum-like members that name an event, a type, a status or a role, which keep a short lowercase value. Numbers become
// 0, booleans and nulls stay, arrays keep at most three sanitized elements. Ids, text, keys and URLs never survive.
type Capture struct {
	Dir   string
	Allow func(ctx context.Context, profile string) bool
	mu    sync.Mutex
}

var enumKeys = map[string]bool{"event": true, "type": true, "object": true, "status": true, "role": true, "method": true, "kind": true}
var enumValue = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

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
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = shape(k, e)
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
		if enumKeys[key] && enumValue.MatchString(x) {
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
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(filepath.Join(c.Dir, name), flag, 0o600)
	if err != nil {
		return // a capture is best effort: it never fails a user's request
	}
	defer f.Close()
	_, _ = f.Write(data)
}
