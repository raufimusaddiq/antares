package chat

import (
	"context"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

type fakeSummarizer struct{ question, answer, reply string }

func (f *fakeSummarizer) Summary(_ context.Context, q, a string) (string, error) {
	f.question, f.answer = q, a
	return f.reply, nil
}

// #261: the picture of a long answer carries a short summary: a headline and points, from the turn and the question
// before it in the caller's own transcript. Another person's turn is refused like an artifact (threat_test.go).
func TestSummaryOfATurnForAPicture(t *testing.T) {
	f := &fakeSummarizer{reply: "Sure! Here it is.\n# Cheapest .dev renewal\n- Cloudflare: Rp 221.400\n- Dewaweb: Rp 300.000 in rupiah\nHope this helps"}
	a := newApp(t, appOpts{summary: f})
	alice := a.user("alice")
	id := alice.newChat()
	c, err := store.GetChat(a.svc.base, a.db, alice.id, id)
	if err != nil {
		t.Fatal(err)
	}
	a.hx.mu.Lock()
	a.hx.msgs[c.HermesSessionID] = []map[string]any{{"role": "user", "content": "cheapest .dev?"}, {"role": "assistant", "content": "a long answer"}}
	a.hx.mu.Unlock()
	ref := ""
	for _, m := range decodeBody[struct{ Messages []hermesMsg }](t, alice.get("/api/chats/"+id+"/messages")).Messages {
		if m.Seq == 1 {
			ref = m.Ref
		}
	}
	w := alice.do("POST", "/api/chats/"+id+"/messages/1/summary?ref="+ref, nil, nil)
	if w.Code != 200 {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	if got := decodeBody[struct{ Summary string }](t, w).Summary; got != "# Cheapest .dev renewal\n- Cloudflare: Rp 221.400\n- Dewaweb: Rp 300.000 in rupiah" {
		t.Errorf("summary %q", got)
	}
	if f.question != "cheapest .dev?" || f.answer != "a long answer" {
		t.Errorf("model was given %q / %q", f.question, f.answer)
	}
	if w := alice.do("POST", "/api/chats/"+id+"/messages/1/summary?ref=0000000000000000", nil, nil); w.Code != 404 {
		t.Errorf("a stale ref: %d", w.Code)
	}
	f.reply = "I cannot summarize this."
	if w := alice.do("POST", "/api/chats/"+id+"/messages/1/summary?ref="+ref, nil, nil); w.Code != 503 {
		t.Errorf("an unusable summary: %d", w.Code)
	}
}
