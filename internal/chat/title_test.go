package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTitler struct {
	mu    sync.Mutex
	calls []string
	reply string
	err   error
}

func (f *fakeTitler) Title(_ context.Context, first string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, first)
	return f.reply, f.err
}

func (f *fakeTitler) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// The chat's title as its owner reads it, once the background job has had its chance.
func titleOf(t *testing.T, p *person, chatID string, want func(string) bool) string {
	t.Helper()
	var got string
	for i := 0; i < 200; i++ {
		got = decodeBody[struct{ Title string }](t, p.get("/api/chats/"+chatID)).Title
		if want(got) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

func untitledChat(t *testing.T, p *person) string {
	t.Helper()
	w := p.do("POST", "/api/chats", nil, map[string]string{})
	if w.Code != 201 {
		t.Fatalf("create chat: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ ID string }](t, w).ID
}

// Like ChatGPT: a chat's first message gets it a short title, written by the title model from that message alone.
func TestFirstMessageTitlesAnUntitledChat(t *testing.T) {
	f := &fakeTitler{reply: "\"Budget Student Laptop.\"\n(more text)"}
	a := newApp(t, appOpts{titler: f})
	alice := a.user("alice")
	id := untitledChat(t, alice)
	alice.send(id, "which laptop is best for a student under 600 dollars")
	if got := titleOf(t, alice, id, func(s string) bool { return s != "" }); got != "Budget Student Laptop" {
		t.Fatalf("title %q", got)
	}
	if f.count() != 1 || f.calls[0] != "which laptop is best for a student under 600 dollars" {
		t.Fatalf("titler calls %q", f.calls)
	}
	// The title is searchable like a typed one.
	if w := alice.get("/api/search/chats?q=Budget"); !strings.Contains(w.Body.String(), id) {
		t.Errorf("auto title not in search: %s", w.Body.String())
	}
}

// A title the user chose, at creation or later, is never replaced; a titled chat costs no model call.
func TestATitledChatKeepsItsTitle(t *testing.T) {
	f := &fakeTitler{reply: "Model Title"}
	a := newApp(t, appOpts{titler: f})
	alice := a.user("alice")
	named := alice.newChat() // created with title "t"
	alice.send(named, "hello there")
	time.Sleep(100 * time.Millisecond)
	if got := titleOf(t, alice, named, func(s string) bool { return true }); got != "t" || f.count() != 0 {
		t.Fatalf("title %q after %d model calls", got, f.count())
	}
}

// Without a model, or when it fails or answers nothing usable, the title is the message's first words.
func TestTitleFallsBackToTheFirstWords(t *testing.T) {
	for name, f := range map[string]*fakeTitler{"model error": {err: errors.New("down")}, "empty answer": {reply: " \n "}, "no model": nil} {
		t.Run(name, func(t *testing.T) {
			opt := appOpts{}
			if f != nil {
				opt.titler = f
			}
			a := newApp(t, opt)
			bob := a.user("bobby")
			id := untitledChat(t, bob)
			bob.send(id, "## Tolong buatkan resep rendang daging sapi yang empuk dan gurih")
			if got := titleOf(t, bob, id, func(s string) bool { return s != "" }); got != "Tolong buatkan resep rendang daging sapi" {
				t.Fatalf("title %q", got)
			}
		})
	}
}

// Only the owner's chat is titled, and another user's message never titles it.
func TestAutoTitleIsTheOwnersChatOnly(t *testing.T) {
	f := &fakeTitler{reply: "Alice Topic"}
	a := newApp(t, appOpts{titler: f})
	alice, bob := a.user("alice"), a.user("bobby")
	id := untitledChat(t, alice)
	if w := bob.do("POST", "/api/chats/"+id+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "bob writes"}); w.Code != 404 {
		t.Fatalf("bob sends into alice's chat: %d", w.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if got := titleOf(t, alice, id, func(s string) bool { return true }); got != "" || f.count() != 0 {
		t.Fatalf("title %q after %d calls", got, f.count())
	}
}

func TestTidyTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Title: **Budget laptop**":  "Budget laptop",
		"\"Resep Rendang Empuk.\"":  "Resep Rendang Empuk",
		"Judul: Resep Rendang":      "Resep Rendang",
		"Greeting!\nSecond line":    "Greeting",
		"   ":                       "",
		"«Red apple on table»?":     "Red apple on table",
		strings.Repeat("word ", 30): "word word word word word word word word word word word word",
		"a\u0007b":                  "ab",
		"# Heading `code`":          "Heading `code",
	} {
		if got := tidyTitle(in); got != want {
			t.Errorf("tidyTitle(%q) = %q, want %q", in, got, want)
		}
	}
	if got := fallbackTitle("[link](x) *bold* text that keeps going on and on"); got != "link x bold text that keeps" {
		t.Errorf("fallback %q", got)
	}
}

// The title request: the configured model and key, the instruction and the message only (capped), nothing else.
func TestTitleModelRequest(t *testing.T) {
	var got struct {
		Model    string
		Messages []struct{ Role, Content string }
	}
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Budget laptop"}}]}`))
	}))
	defer srv.Close()
	m := &TitleModel{URL: srv.URL + "/v1/", Key: "k-fabricated", Model: "small-model", Client: srv.Client()}
	title, err := m.Title(context.Background(), strings.Repeat("x", titleInputRunes+500))
	if err != nil || title != "Budget laptop" {
		t.Fatalf("title %q err %v", title, err)
	}
	if path != "/v1/chat/completions" || auth != "Bearer k-fabricated" || got.Model != "small-model" || len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Fatalf("request path %s auth %q body %+v", path, auth, got)
	}
	if n := strings.Count(got.Messages[1].Content, "x"); n != titleInputRunes {
		t.Fatalf("sent %d characters of the message", n)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusTooManyRequests) }))
	defer bad.Close()
	if _, err := (&TitleModel{URL: bad.URL, Key: "k", Model: "m", Client: bad.Client()}).Title(context.Background(), "hi"); err == nil || strings.Contains(err.Error(), "k") && strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("error %v", err)
	}
}

// A slow title model: a call returns only when released, with the title of the message it was given.
type slowTitler struct {
	mu      sync.Mutex
	calls   []string
	release chan struct{}
}

func (f *slowTitler) Title(ctx context.Context, first string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, first)
	f.mu.Unlock()
	select {
	case <-f.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return "Title of " + strings.Fields(first)[0], nil
}

// The first message reserves the chat's title: a second message sent while the model is still writing that title
// starts no second job, so the chat is titled from the message that started it.
func TestTitleComesFromTheFirstMessageEvenWhenTheModelIsSlow(t *testing.T) {
	f := &slowTitler{release: make(chan struct{})}
	a := newApp(t, appOpts{titler: f})
	alice := a.user("alice")
	id := untitledChat(t, alice)
	first := alice.send(id, "laptops for students")
	_, fr := a.hx.runWithInput(t, "laptops for students") // finish the first run so the chat takes another message
	st := alice.open(first, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", `{"output":"ok"}`)
	st.waitFor(t, "run.completed")
	alice.send(id, "recipes for dinner")
	time.Sleep(100 * time.Millisecond)
	close(f.release)
	if got := titleOf(t, alice, id, func(s string) bool { return s != "" }); got != "Title of laptops" {
		t.Fatalf("title %q", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || f.calls[0] != "laptops for students" {
		t.Fatalf("title model calls %q", f.calls)
	}
}
