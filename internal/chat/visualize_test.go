package chat

import (
	"strings"
	"testing"
)

// #153, LLM-008: Visualize is per chat. Its runs carry the versioned OpenUI instruction (before the user's own
// preferences); another chat's runs, and another user's, carry none. Another user cannot turn it on.
func TestVisualizeIsPerChatAndReachesOnlyItsRuns(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	on, off := alice.newChat(), alice.newPlainChat()
	// A new chat starts with Visualize on, and says so in the create response the client renders, not only on a read.
	if w := alice.do("POST", "/api/chats", nil, map[string]string{"title": "t"}); w.Code != 201 || !strings.Contains(w.Body.String(), `"visualize":true`) {
		t.Fatalf("create response: %d %s", w.Code, w.Body.String())
	}
	if w := alice.get("/api/chats/" + on); !strings.Contains(w.Body.String(), `"visualize":true`) {
		t.Fatalf("a new chat is not visualized: %s", w.Body.String())
	}
	if w := bob.do("PATCH", "/api/chats/"+on, nil, map[string]bool{"visualize": true}); w.Code != 404 {
		t.Fatalf("bob turns on alice's chat: %d", w.Code)
	}
	if w := alice.do("PATCH", "/api/chats/"+on, nil, map[string]bool{"visualize": true}); w.Code != 200 || !strings.Contains(w.Body.String(), `"visualize":true`) {
		t.Fatalf("turn on: %d %s", w.Code, w.Body.String())
	}
	if w := alice.do("PATCH", "/api/chats/"+on, nil, map[string]any{"visualize": true, "pinned": true}); w.Code != 400 {
		t.Errorf("two changes at once: %d", w.Code)
	}
	alice.do("PUT", "/api/me/personality", nil, map[string]string{"text": "Be brief."})

	first := alice.send(on, "visual question")
	instr, _ := runBodyFor(t, a, "visual question")["instructions"].(string)
	vi, pi := strings.Index(instr, "[antares-openui v2]"), strings.Index(instr, "Be brief.")
	if vi != 0 || pi < vi {
		t.Errorf("instructions with visualize on: %q", instr)
	}
	alice.send(off, "plain question")
	instr, _ = runBodyFor(t, a, "plain question")["instructions"].(string)
	if strings.Contains(instr, "openui") || !strings.HasSuffix(instr, "Be brief.") {
		t.Errorf("instructions with visualize off: %q", instr)
	}
	bob.send(bob.newPlainChat(), "bob question")
	if b := runBodyFor(t, a, "bob question"); b["instructions"] != nil {
		t.Errorf("bob's run: %v", b["instructions"])
	}
	_, fr := a.hx.runWithInput(t, "visual question") // finish the chat's first run before sending into it again
	st := alice.open(first, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", `{"output":"ok"}`)
	st.waitFor(t, "run.completed")
	alice.do("PATCH", "/api/chats/"+on, nil, map[string]bool{"visualize": false})
	alice.do("PUT", "/api/me/personality", nil, map[string]string{"text": ""})
	alice.send(on, "after turning off")
	if b := runBodyFor(t, a, "after turning off"); b["instructions"] != nil {
		t.Errorf("after turning it off: %v", b["instructions"])
	}
}
