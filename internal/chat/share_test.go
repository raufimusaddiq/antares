package chat

import (
	"strings"
	"testing"
)

type sharedView struct {
	Title    string      `json:"title"`
	SharedAt string      `json:"sharedAt"`
	Turns    []shareTurn `json:"turns"`
}

func shareChat(t *testing.T, p *person, chatID string) string {
	t.Helper()
	w := p.do("POST", "/api/chats/"+chatID+"/share", nil, nil)
	if w.Code != 201 {
		t.Fatalf("share = %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ Token string }](t, w).Token
}

// A link shows the chat as it was when shared, to anyone, with nothing that names the owner, the chat or a file.
func TestShareLinkIsAFrozenCopyAnyoneCanRead(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	sid := decodeSession(t, a, alice, chat)
	a.hx.mu.Lock()
	a.hx.msgs[sid] = []map[string]any{
		{"role": "user", "content": "draw a cat"},
		{"role": "assistant", "content": "Here it is: MEDIA:/opt/data/profiles/" + alice.profile + "/cache/cat.png. Enjoy"},
	}
	a.hx.mu.Unlock()
	token := shareChat(t, alice, chat)

	a.hx.mu.Lock()
	a.hx.msgs[sid] = append(a.hx.msgs[sid], map[string]any{"role": "user", "content": "said after sharing"})
	a.hx.mu.Unlock()
	w := a.anon("GET", "/api/shared/"+token)
	if w.Code != 200 || w.Header().Get("X-Robots-Tag") == "" {
		t.Fatalf("public read = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	got := decodeBody[sharedView](t, w)
	if len(got.Turns) != 2 || got.Turns[0] != (shareTurn{"user", "draw a cat"}) || got.Turns[1].Text != "Here it is: [media not shared]. Enjoy" {
		t.Fatalf("shared turns = %+v", got.Turns)
	}
	for _, leak := range []string{alice.id, alice.profile, alice.sub + "@", chat, sid, "/opt/data", "said after sharing"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("the public read carries %q: %s", leak, w.Body.String())
		}
	}
	if st := decodeBody[shareStatusView](t, alice.get("/api/chats/"+chat+"/share")); !st.Shared || st.Turns != 2 {
		t.Errorf("owner's status = %+v", st)
	}
}

// Every way a link stops working answers the same 404 as a link that never existed.
func TestShareLinkDies(t *testing.T) {
	cases := map[string]func(t *testing.T, a *app, p *person, chat string){
		"stopped by the owner": func(t *testing.T, a *app, p *person, chat string) {
			if w := p.do("DELETE", "/api/chats/"+chat+"/share", nil, nil); w.Code != 204 {
				t.Fatalf("stop = %d", w.Code)
			}
		},
		"replaced by a new link": func(t *testing.T, a *app, p *person, chat string) {
			if fresh := shareChat(t, p, chat); a.anon("GET", "/api/shared/"+fresh).Code != 200 {
				t.Fatal("the new link does not work")
			}
		},
		"chat deleted": func(t *testing.T, a *app, p *person, chat string) {
			if w := p.do("DELETE", "/api/chats/"+chat, nil, nil); w.Code != 200 {
				t.Fatalf("delete = %d %s", w.Code, w.Body.String())
			}
			if a.count(t, `SELECT COUNT(*) FROM chat_shares`) != 0 {
				t.Error("the deleted chat's snapshot was kept")
			}
		},
		"account suspended": func(t *testing.T, a *app, p *person, chat string) {
			if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, p.id); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, kill := range cases {
		t.Run(name, func(t *testing.T) {
			a := newApp(t)
			deleteAdvertised(t, a)
			alice := a.user("alice")
			chat := alice.newChat()
			seedTurns(t, a, alice, chat, 1)
			token := shareChat(t, alice, chat)
			kill(t, a, alice, chat)
			got, never := a.anon("GET", "/api/shared/"+token), a.anon("GET", "/api/shared/"+strings.Repeat("A", 43))
			if got.Code != 404 || !sameAnswer(got, never) {
				t.Fatalf("dead link = %d %s, unknown link = %d %s", got.Code, got.Body.String(), never.Code, never.Body.String())
			}
		})
	}
}

// The session-less read is throttled for everyone together, and says when to come back.
func TestShareReadsAreThrottled(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	seedTurns(t, a, alice, chat, 1)
	token := shareChat(t, alice, chat)
	for i := 0; i < 60; i++ {
		if w := a.anon("GET", "/api/shared/"+token); w.Code != 200 {
			t.Fatalf("read %d = %d", i+1, w.Code)
		}
	}
	w := a.anon("GET", "/api/shared/"+token)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("read 61 = %d %v", w.Code, w.Header())
	}
}
