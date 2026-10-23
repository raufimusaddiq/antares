package hermes

import (
	"encoding/json"
	"testing"
)

func parse(t *testing.T, s string) *Usage {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatal(err)
	}
	u, _ := d["usage"].(map[string]any)
	r, _ := d["runtime"].(map[string]any)
	return parseUsage(u, r)
}

func TestParseUsage(t *testing.T) {
	got := parse(t, `{"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"cache_read_tokens":4,"cache_write_tokens":1},"runtime":{"model":"m"}}`)
	if got == nil || *got != (Usage{Input: 10, Output: 3, CacheRead: 4, CacheWrite: 1, Model: "m"}) {
		t.Fatalf("got %+v", got)
	}
	for name, s := range map[string]string{
		"absent":    `{}`,
		"empty":     `{"usage":{}}`,
		"all zero":  `{"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		"negative":  `{"usage":{"input_tokens":-5,"output_tokens":-1}}`,
		"strings":   `{"usage":{"input_tokens":"10","output_tokens":"3"}}`,
		"too large": `{"usage":{"input_tokens":1e30,"output_tokens":1e30}}`,
	} {
		if u := parse(t, s); u != nil {
			t.Errorf("%s: want nil (not reported), got %+v", name, u)
		}
	}
	ev := Normalize(Event{Name: "run.completed", Data: []byte(`{"usage":{"input_tokens":2,"output_tokens":1}}`)})
	if ev.Usage == nil || ev.Usage.Input != 2 {
		t.Fatalf("run.completed usage: %+v", ev.Usage)
	}
}
