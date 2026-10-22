package hermes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// All keys, ids and bodies below are fabricated. The wire shapes follow the
// documented API (PRD §9A); they were not captured from the deployed Hermes.
const (
	testProfile = "u_testprofile1"
	testKey     = "fabricated-profile-key-0123456789"
)

func bind() Binding { return Binding{Profile: testProfile, Key: testKey} }

func newTestClient(t *testing.T, h http.Handler, o ClientOptions) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, o)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestBindingNeverPrintsItsKey(t *testing.T) {
	b := bind()
	for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		if out := fmt.Sprintf(f, b); strings.Contains(out, testKey) || strings.Contains(out, testProfile) {
			t.Errorf("%s printed the binding: %s", f, out)
		}
	}
	if out := fmt.Sprint(&b, []Binding{b}, map[string]Binding{"k": b}); strings.Contains(out, testKey) {
		t.Errorf("nested print leaked: %s", out)
	}
}

func TestRequestsCarryTheBindingAndNothingElse(t *testing.T) {
	var mu sync.Mutex
	var got []*http.Request
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/sessions") {
			fmt.Fprint(w, `{"id":"sess_1"}`)
			return
		}
		fmt.Fprint(w, `{"run_id":"run_1","status":"started"}`)
	}), ClientOptions{})
	ctx := context.Background()
	if _, err := c.CreateSession(ctx, bind()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateRun(ctx, bind(), RunRequest{SessionID: "sess_1", Input: "hi", SessionKey: "chatui:user:abc", IdempotencyKey: "antares-r1"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if !strings.HasPrefix(r.URL.Path, "/p/"+testProfile+"/") || r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("%s %s: wrong route or credential (auth %q)", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
	}
	run := got[1]
	if run.Header.Get("X-Hermes-Session-Key") != "chatui:user:abc" || run.Header.Get("Idempotency-Key") != "antares-r1" {
		t.Errorf("run headers: %v", run.Header)
	}
}

func TestErrorMappingAndNoLeak(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{401, CodeTenantNotReady}, {403, CodeTenantNotReady},
		{429, CodeRateLimited}, {409, CodeRunConflict},
		{504, CodeUpstreamTimeout}, {408, CodeUpstreamTimeout},
		{404, CodeCapabilityNA}, {501, CodeCapabilityNA},
		{500, CodeHermesUnavailable}, {502, CodeHermesUnavailable}, {503, CodeHermesUnavailable}, {400, CodeHermesUnavailable},
		{302, CodeHermesUnavailable},
	}
	for _, tc := range cases {
		c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An upstream error body that echoes secrets must never come back out.
			w.Header().Set("Location", "http://elsewhere.invalid/")
			w.WriteHeader(tc.status)
			fmt.Fprint(w, `{"error":"bad key `+testKey+` at `+r.Host+`"}`)
		}), ClientOptions{})
		_, err := c.CreateSession(context.Background(), bind())
		if CodeOf(err) != tc.code {
			t.Errorf("status %d -> %q, want %q", tc.status, CodeOf(err), tc.code)
		}
		if err != nil && (strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), testProfile)) {
			t.Errorf("status %d: error text leaks: %q", tc.status, err)
		}
	}
}

func TestTransportFailuresAreMappedWithoutTheURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused
	c, _ := NewClient(url, ClientOptions{})
	_, err := c.CreateSession(context.Background(), bind())
	if CodeOf(err) != CodeHermesUnavailable || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), testProfile) {
		t.Errorf("refused: %v", err)
	}

	slow, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}), ClientOptions{CallTimeout: 50 * time.Millisecond})
	_, err = slow.CreateSession(context.Background(), bind())
	if CodeOf(err) != CodeUpstreamTimeout {
		t.Errorf("slow upstream -> %q, want UPSTREAM_TIMEOUT", CodeOf(err))
	}
}

func TestRejectsABindingThatIsNotAProvisionedProfile(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was made for an invalid binding: %s", r.URL.Path)
	}), ClientOptions{})
	for _, b := range []Binding{{Profile: "default", Key: testKey}, {Profile: "u_x/../default", Key: testKey}, {Profile: testProfile}, {}} {
		if _, err := c.CreateSession(context.Background(), b); CodeOf(err) != CodeTenantNotReady {
			t.Errorf("%v: %v", b.Profile, err)
		}
	}
	// Upstream-supplied ids are re-validated before they enter a path.
	for _, id := range []string{"../x", "a/b", "a b", "", strings.Repeat("a", 200), "x?y=1"} {
		if _, err := c.GetRun(context.Background(), bind(), id); err == nil {
			t.Errorf("run id %q accepted", id)
		}
	}
}

func TestCreateSessionAndRunRejectUnusableShapes(t *testing.T) {
	for _, body := range []string{`{}`, `[]`, `{"id":"../etc"}`, `{"id":7}`, `not json`, ``} {
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }), ClientOptions{})
		if id, err := c.CreateSession(context.Background(), bind()); err == nil || id != "" {
			t.Errorf("session body %q accepted as %q", body, id)
		}
		if id, err := c.CreateRun(context.Background(), bind(), RunRequest{SessionID: "s1", Input: "x"}); err == nil || id != "" {
			t.Errorf("run body %q accepted as %q", body, id)
		}
	}
}

func TestGetRunReducesStatesAndUnknownIsNeverCompleted(t *testing.T) {
	for raw, want := range map[string]string{
		"completed": StateCompleted, "failed": StateFailed, "cancelled": StateCancelled, "canceled": StateCancelled,
		"running": StateRunning, "started": StateRunning, "stopping": StateRunning, "interrupted": StateFailed,
		"unknown": StateUnknown, "": StateUnknown, "something-new": StateUnknown,
	} {
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"status":%q,"output":"done"}`, raw)
		}), ClientOptions{})
		st, err := c.GetRun(context.Background(), bind(), "run_1")
		if err != nil || st.State != want {
			t.Errorf("status %q -> %q (%v), want %q", raw, st.State, err, want)
		}
	}
	c, _ := newTestClient(t, http.NotFoundHandler(), ClientOptions{})
	if _, err := c.GetRun(context.Background(), bind(), "run_1"); !errors.Is(err, ErrRunGone) {
		t.Errorf("404 on a run = %v, want ErrRunGone", err)
	}
}

func sseHandler(frames string, hold bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, frames)
		w.(http.Flusher).Flush()
		if hold {
			<-r.Context().Done()
		}
	})
}

func TestStreamParsesFramesAndBoundsThem(t *testing.T) {
	frames := ": hello\n\nevent: assistant.delta\ndata: {\"delta\":\"a\"}\n\n" +
		"data: {\"type\":\"tool.started\",\n" + "data: \"id\":\"t1\"}\n\n" + // multi-line data joins with \n, still JSON
		"event: run.completed\r\ndata: {}\r\n\r\n"
	c, _ := newTestClient(t, sseHandler(frames, false), ClientOptions{})
	var got []Event
	if err := c.StreamEvents(context.Background(), bind(), "run_1", func(e Event) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "assistant.delta" || got[2].Name != "run.completed" || !strings.Contains(string(got[1].Data), "tool.started") {
		t.Fatalf("frames: %+v", got)
	}
	if Normalize(got[1]).Kind != KindToolStarted {
		t.Errorf("multi-line data frame not understood: %q", got[1].Data)
	}

	huge := "event: x\ndata: " + strings.Repeat("a", maxLine+10) + "\n\n"
	c, _ = newTestClient(t, sseHandler(huge, false), ClientOptions{})
	if err := c.StreamEvents(context.Background(), bind(), "run_1", func(Event) error { return nil }); CodeOf(err) != CodeHermesUnavailable {
		t.Errorf("oversized line: %v", err)
	}
	var many strings.Builder
	many.WriteString("event: x\n")
	for i := 0; i < 20; i++ {
		many.WriteString("data: " + strings.Repeat("b", 10<<10) + "\n")
	}
	many.WriteString("\n")
	c, _ = newTestClient(t, sseHandler(many.String(), false), ClientOptions{})
	if err := c.StreamEvents(context.Background(), bind(), "run_1", func(Event) error { return nil }); CodeOf(err) != CodeHermesUnavailable {
		t.Errorf("oversized frame: %v", err)
	}
}

func TestStreamRefusesANonStreamAnswer(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"completed"}`)
	}), ClientOptions{})
	if err := c.StreamEvents(context.Background(), bind(), "run_1", func(Event) error { return nil }); CodeOf(err) != CodeHermesUnavailable {
		t.Errorf("json body treated as a stream: %v", err)
	}
}

func TestStreamIdleTimeoutIsATimeoutAndCallerCancelIsNot(t *testing.T) {
	c, _ := newTestClient(t, sseHandler(": hi\n\n", true), ClientOptions{StreamIdle: 80 * time.Millisecond})
	err := c.StreamEvents(context.Background(), bind(), "run_1", func(Event) error { return nil })
	if CodeOf(err) != CodeUpstreamTimeout {
		t.Errorf("idle stream -> %v, want UPSTREAM_TIMEOUT", err)
	}

	c, _ = newTestClient(t, sseHandler(": hi\n\n", true), ClientOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err = c.StreamEvents(ctx, bind(), "run_1", func(Event) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("caller cancel -> %v, want context.Canceled (not an upstream fault)", err)
	}
}

func TestMessagesKeepsOnlyUserAndAssistantTextAndTheNewest(t *testing.T) {
	body := `{"messages":[
	  {"id":"m1","role":"system","content":"hidden"},
	  {"id":"m2","role":"user","content":"one"},
	  {"id":"m3","role":"tool","content":"tool output"},
	  {"id":"m4","role":"assistant","content":[{"type":"text","text":"two"},{"type":"image","url":"x"}]},
	  {"id":"../m5","role":"user","content":"three","created_at":"2026-01-01T00:00:00Z"}]}`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "include_compacted=true") {
			t.Errorf("query: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, body)
	}), ClientOptions{})
	msgs, trunc, err := c.Messages(context.Background(), bind(), "sess_1", 50)
	if err != nil || trunc || len(msgs) != 3 || msgs[1].Text != "two" || msgs[2].ID != "" || msgs[2].CreatedAt == "" {
		t.Fatalf("%+v trunc=%v err=%v", msgs, trunc, err)
	}
	msgs, trunc, _ = c.Messages(context.Background(), bind(), "sess_1", 2)
	if !trunc || len(msgs) != 2 || msgs[0].Text != "two" {
		t.Errorf("newest two: %+v trunc=%v", msgs, trunc)
	}
	for _, bad := range []string{`{}`, `7`, `"x"`, `not json`} {
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, bad) }), ClientOptions{})
		if _, _, err := c.Messages(context.Background(), bind(), "sess_1", 10); err == nil {
			t.Errorf("transcript shape %q accepted", bad)
		}
	}
}

func TestNormalize(t *testing.T) {
	ev := func(name, data string) Event { return Event{Name: name, Data: []byte(data)} }
	cases := []struct {
		in   Event
		want Normalized
	}{
		{ev("assistant.delta", `{"delta":"hi"}`), Normalized{Kind: KindDelta, Text: "hi"}},
		{ev("", `{"type":"message.delta","text":"yo"}`), Normalized{Kind: KindDelta, Text: "yo"}},
		{ev("tool.started", `{"tool_call_id":"t1","name":"Web search"}`), Normalized{Kind: KindToolStarted, ToolID: "t1", Label: "Web search"}},
		{ev("tool.completed", `{"id":"t1","summary":"3 results"}`), Normalized{Kind: KindToolCompleted, ToolID: "t1", Summary: "3 results"}},
		{ev("approval.required", `{}`), Normalized{Kind: KindApproval}},
		{ev("run.completed", `{"output":"final"}`), Normalized{Kind: KindTerminal, State: StateCompleted, Output: "final"}},
		{ev("run.failed", `{}`), Normalized{Kind: KindTerminal, State: StateFailed}},
		{ev("run.cancelled", `{}`), Normalized{Kind: KindTerminal, State: StateCancelled}},
		{ev("run.interrupted", `{}`), Normalized{Kind: KindTerminal, State: StateFailed, Intr: true}},
		// Anything else is dropped, and in particular never read as completion.
		{ev("run.unknown", `{}`), Normalized{Kind: KindIgnore}},
		{ev("assistant.delta", `{"delta":7}`), Normalized{Kind: KindIgnore}},
		{ev("", `not json`), Normalized{Kind: KindIgnore}},
		{ev("run.completed.maybe", `{}`), Normalized{Kind: KindIgnore}},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("%s %s -> %+v, want %+v", tc.in.Name, tc.in.Data, got, tc.want)
		}
	}
	if n := Normalize(ev("tool.started", `{"tool_call_id":"../x","name":"`+strings.Repeat("é", 500)+`"}`)); n.ToolID != "" || len(n.Label) > maxLabel {
		t.Errorf("unbounded or unsafe fields survived: %+v", n)
	}
}
