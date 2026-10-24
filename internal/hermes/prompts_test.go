package hermes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func i64(v int64) *int64 { return &v }

// The frames follow Hermes 0.22.5 (api_server._approval_request_event, session_pending_controls); the
// values are fabricated.
func TestNormalizePromptFrames(t *testing.T) {
	for name, tc := range map[string]struct {
		in   Event
		want Normalized
	}{
		"run-stream approval": {
			Event{Name: "approval.request", Data: []byte(`{"event":"approval.request","run_id":"r","request_id":"q1","command":"rm x","description":"d","choices":["once","session","always","deny"]}`)},
			Normalized{Kind: KindApproval, PromptID: "q1", Text: "rm x", Detail: "d", Choices: []string{"once", "session", "always", "deny"}},
		},
		"authority approval": {
			Event{Name: "approval.request", Data: []byte(`{"kind":"approval","prompt_id":"p9","execution_generation":3,"command":"c","choices":["once","deny"]}`)},
			Normalized{Kind: KindApproval, PromptID: "p9", Gen: i64(3), Text: "c", Choices: []string{"once", "deny"}},
		},
		"clarify": {
			Event{Name: "clarify.request", Data: []byte(`{"kind":"clarify","prompt_id":"k1","execution_generation":0,"question":"which?","choices":["a"],"multi_select":false}`)},
			Normalized{Kind: KindClarify, PromptID: "k1", Gen: i64(0), Text: "which?", Choices: []string{"a"}},
		},
		"settled":               {Event{Name: "clarify.settled", Data: []byte(`{"prompt_id":"k1","execution_generation":1}`)}, Normalized{Kind: KindPromptSettled, PromptID: "k1", Gen: i64(1)}},
		"id with a bad char":    {Event{Name: "approval.request", Data: []byte(`{"request_id":"a b","command":"x"}`)}, Normalized{Kind: KindApproval, Text: "x"}},
		"fractional generation": {Event{Name: "clarify.request", Data: []byte(`{"prompt_id":"k","execution_generation":1.5,"question":"q"}`)}, Normalized{Kind: KindClarify, PromptID: "k", Text: "q"}},
	} {
		if got := Normalize(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}

// The body is exactly what Hermes validates: authority prompts take the three keys and nothing else,
// the legacy approval takes two; a 409 is PROMPT_STALE, a clarification without a generation never leaves.
func TestRespondBodiesAndErrors(t *testing.T) {
	var path string
	var body map[string]any
	status := http.StatusOK
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path = r.URL.Path
		body = nil
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"resolved":1}`)
	}), ClientOptions{})
	ctx := context.Background()

	if err := c.RespondApproval(ctx, bind(), "run_1", "q1", nil, "once"); err != nil {
		t.Fatal(err)
	}
	if path != "/p/"+testProfile+"/v1/runs/run_1/approval" || !reflect.DeepEqual(body, map[string]any{"request_id": "q1", "choice": "once"}) {
		t.Errorf("legacy approval: %s %v", path, body)
	}
	if err := c.RespondApproval(ctx, bind(), "run_1", "p9", i64(3), "deny"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, map[string]any{"request_id": "p9", "execution_generation": float64(3), "choice": "deny"}) {
		t.Errorf("authority approval: %v", body)
	}
	if err := c.RespondClarify(ctx, bind(), "run_1", "k1", i64(0), "b"); err != nil {
		t.Fatal(err)
	}
	if path != "/p/"+testProfile+"/v1/runs/run_1/clarify" || !reflect.DeepEqual(body, map[string]any{"request_id": "k1", "execution_generation": float64(0), "answer": "b"}) {
		t.Errorf("clarify: %s %v", path, body)
	}

	path = ""
	if err := c.RespondClarify(ctx, bind(), "run_1", "k1", nil, "b"); CodeOf(err) != CodeCapabilityNA || path != "" {
		t.Errorf("clarify without a generation: %v, sent to %q", err, path)
	}
	if err := c.RespondApproval(ctx, bind(), "run_1", "../x", nil, "once"); err == nil || path != "" {
		t.Errorf("a malformed request id was sent: %v %q", err, path)
	}
	status = http.StatusConflict
	if err := c.RespondApproval(ctx, bind(), "run_1", "q1", nil, "once"); CodeOf(err) != CodePromptStale {
		t.Errorf("409: %v", err)
	}
}
