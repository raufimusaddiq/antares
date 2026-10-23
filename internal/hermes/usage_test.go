package hermes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// A malformed usage or runtime member must not fail the status read: the run's
// state is still the truth, and the usage is simply unknown.
func TestGetRunToleratesMalformedUsage(t *testing.T) {
	for _, body := range []string{
		`{"status":"completed","output":"x","usage":["a"],"runtime":"m"}`,
		`{"status":"completed","output":"x","usage":"lots","runtime":7}`,
		`{"status":"completed","output":"x","usage":null}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c, err := NewClient(srv.URL, ClientOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := c.GetRun(context.Background(), bind(), "run1")
		srv.Close()
		if err != nil || st.State != StateCompleted || st.Usage != nil {
			t.Fatalf("%s: %+v %v", body, st, err)
		}
	}
}
