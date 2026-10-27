package chat

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Acceptance scenarios (issue #154; PRD UXP-ACC-003/004/014/016). Each runs the whole path through the HTTP API against
// the fake Hermes and asserts the outcome, not only that something happened. The approval-across-reload scenario is
// TestRunNeedingApprovalDoesNotProceedUntilApproved (prompts_test.go): it drops the stream and the in-memory journal
// and reads the pending approval back from the database.

func createRuns(a *app, input string) int {
	n := 0
	for _, r := range a.hx.requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/v1/runs") && strings.Contains(r.Body, input) {
			n++
		}
	}
	return n
}

// UXP-ACC-003: the account is suspended while its reply is finishing. The open stream ends at once (disconnected, not
// completed), the run still finishes upstream and is recorded once; after reinstatement the reattached stream shows it
// completed exactly once, and no second run was created.
func TestAcceptanceSuspendedWhileFinishingThenReattach(t *testing.T) {
	a := newApp(t, appOpts{heartbeat: 20 * time.Millisecond})
	u := a.user("alice")
	run := u.send(u.newChat(), "finish me")
	_, fr := a.hx.runWithInput(t, "finish me")
	s1 := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.push(fr, "assistant.delta", `{"delta":"almost "}`)
	s1.waitFor(t, "assistant.delta")
	last := s1.events()[len(s1.events())-1].ID

	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s1.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived the suspension")
	}
	if !s1.has("run.disconnected") || s1.has("run.completed") {
		t.Fatalf("suspended stream: %v", s1.names())
	}
	// While suspended nothing private is reachable: no reattach to the run, no transcript, no run status.
	for _, path := range []string{"/api/runs/" + run + "/events", "/api/runs/" + run, "/api/chats"} {
		if w := u.get(path); w.Code != 403 || errCode(t, w) != "ACCOUNT_SUSPENDED" {
			t.Fatalf("suspended %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	a.hx.push(fr, "assistant.delta", `{"delta":"done"}`)
	a.hx.push(fr, "run.completed", `{"output":"almost done"}`)
	eventually(t, func() bool {
		var st string
		_ = a.db.QueryRow(`SELECT status FROM runs WHERE id = ?`, run).Scan(&st)
		return st == store.RunCompleted
	}, "the run to finish upstream while the account is suspended")

	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'APPROVED' WHERE id = ?`, u.id); err != nil {
		t.Fatal(err)
	}
	s2 := u.open(run, last)
	<-s2.done
	completed := 0
	for _, n := range s2.names() {
		if n == "run.completed" {
			completed++
		}
	}
	if completed != 1 || u.runStatus(run) != store.RunCompleted {
		t.Fatalf("reattached: %v, status %s", s2.names(), u.runStatus(run))
	}
	if n := createRuns(a, "finish me"); n != 1 {
		t.Fatalf("Hermes was asked to start %d runs", n)
	}
}

// UXP-ACC-004: a 1000-turn transcript as Hermes returns it with include_compacted=true (hermes.Client.Transcript always
// asks for it): the first 600 turns are compacted (flagged, as Hermes marks them) and tool-role messages are interleaved,
// which Antares does not show. Search finds a compacted turn; paging end to end returns exactly the 1000 user and
// assistant turns, once each, in chronological order, which is what export reads (web/src/ExportChat.test.tsx exports
// the same shape to Markdown).
func TestAcceptanceThousandTurnCompactedSearchAndPaging(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	chat := u.newChat()
	var list []map[string]any
	for i := 0; i < 1000; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		text := fmt.Sprintf("turn %04d body", i)
		if i == 3 {
			text = "turn 0003 mentions the rare word zanzibarite"
		}
		m := map[string]any{"role": role, "content": text}
		if i < 600 {
			m["compacted"] = true
		}
		list = append(list, m)
		if i%50 == 7 {
			list = append(list, map[string]any{"role": "tool", "content": "tool output that is not a turn", "compacted": i < 600})
		}
	}
	a.hx.mu.Lock()
	a.hx.msgs[decodeSession(t, a, u, chat)] = list
	a.hx.mu.Unlock()

	r := doSearch(t, u, "/api/search/chats?q=zanzibarite", 200)
	if len(r.Results) != 1 || r.Results[0].ChatID != chat || r.Results[0].Turn == nil || *r.Results[0].Turn != 3 {
		t.Fatalf("search in a compacted turn: %+v", r.Results)
	}
	if r := doSearch(t, u, "/api/search/chats?q=tool+output", 200); len(r.Results) != 0 {
		t.Fatalf("a tool message was indexed as a turn: %+v", r.Results)
	}

	var texts []string
	seen := map[int]bool{}
	cursor := ""
	for page := 0; page < 100; page++ {
		path := "/api/chats/" + chat + "/messages"
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		w := u.get(path)
		if w.Code != 200 {
			t.Fatalf("page %d: %d %s", page, w.Code, w.Body.String())
		}
		p := decodeBody[struct {
			Messages []struct {
				Seq  int
				Text string
			}
			NextCursor string
			Total      int
		}](t, w)
		if p.Total != 1000 {
			t.Fatalf("total %d", p.Total)
		}
		var chunk []string
		for _, m := range p.Messages {
			if seen[m.Seq] {
				t.Fatalf("turn %d returned twice", m.Seq)
			}
			seen[m.Seq] = true
			chunk = append(chunk, m.Text)
		}
		texts = append(chunk, texts...) // pages run newest first; each page is in order
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(texts) != 1000 {
		t.Fatalf("paged %d of 1000 turns", len(texts))
	}
	for i, txt := range texts {
		if !strings.HasPrefix(txt, fmt.Sprintf("turn %04d ", i)) {
			t.Fatalf("turn %d out of order: %q", i, txt)
		}
	}
}

// UXP-ACC-014/016: ten disconnect/reconnect cycles while the run streams tool events and an OpenUI block. Every cycle
// resumes after the last event the client saw: the assembled answer is exact, no event arrives twice, all ten tool
// starts are seen, and Hermes was asked to start one run only (the logical run is not duplicated by reconnects).
func TestAcceptanceTenReconnectCyclesWithToolsAndOpenUI(t *testing.T) {
	a := newApp(t)
	u := a.user("alice")
	run := u.send(u.newChat(), "cycle")
	_, fr := a.hx.runWithInput(t, "cycle")
	s := u.open(run, "")
	recv(t, fr.opens, "upstream stream")
	var all []sseEv
	var want strings.Builder
	for i := 0; i < 10; i++ {
		a.hx.push(fr, "tool.started", fmt.Sprintf(`{"tool_call_id":"t%d","tool":"web_search"}`, i))
		a.hx.push(fr, "tool.completed", fmt.Sprintf(`{"tool_call_id":"t%d","duration":0.1,"error":false,"preview":"ok"}`, i))
		piece := fmt.Sprintf("part %d ", i)
		if i == 5 {
			piece = "```openui\nroot = Stack([Text(\"hi\")])\n```\n"
		}
		frame, _ := json.Marshal(map[string]string{"delta": piece})
		a.hx.push(fr, "assistant.delta", string(frame))
		want.WriteString(piece)
		eventually(t, func() bool {
			for _, e := range s.events() {
				if e.Name == "assistant.delta" && strings.Contains(e.Data, strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%q", piece), `"`), `"`)) {
					return true
				}
			}
			return false
		}, "this cycle's own delta")
		s.close()
		evs := s.events() // after close: nothing more can arrive on this stream
		all = append(all, evs...)
		s = u.open(run, evs[len(evs)-1].ID)
	}
	a.hx.push(fr, "run.completed", `{}`)
	<-s.done
	all = append(all, s.events()...)

	seen := map[string]bool{}
	var text strings.Builder
	starts := 0
	for _, e := range all {
		if e.ID != "" && seen[e.ID] {
			t.Fatalf("event %s (%s) delivered twice", e.ID, e.Name)
		}
		seen[e.ID] = true
		switch e.Name {
		case "assistant.delta":
			var d struct{ Text string }
			_ = jsonUnmarshal(e.Data, &d)
			text.WriteString(d.Text)
		case "tool.started":
			starts++
		}
	}
	if text.String() != want.String() {
		t.Errorf("assembled %q, want %q", text.String(), want.String())
	}
	if starts != 10 || all[len(all)-1].Name != "run.completed" {
		t.Errorf("tool starts %d, last event %s", starts, all[len(all)-1].Name)
	}
	if n := createRuns(a, `"cycle"`); n != 1 {
		t.Fatalf("Hermes was asked to start %d runs", n)
	}
	if u.runStatus(run) != store.RunCompleted {
		t.Errorf("status %s", u.runStatus(run))
	}
}
