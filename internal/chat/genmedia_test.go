package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// fakeMedia stands in for the provisioning helper: it serves one file of one profile, and records who asked.
type fakeMedia struct {
	mu     sync.Mutex
	asked  []string
	owner  string
	path   string
	broken bool
}

func (f *fakeMedia) Media(_ context.Context, slug, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, slug+" "+path)
	if f.broken {
		return nil, "", errors.New("helper down")
	}
	if slug != f.owner || path != f.path {
		return nil, "", hermes.ErrMediaNotFound
	}
	return []byte("\x89PNG-0123456789"), "image/png", nil
}

var sealedTag = regexp.MustCompile(`MEDIA:(image-[A-Za-z0-9_-]+)`)

func TestMediaTagsAreSealedAndNeverCarryThePath(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/a.png"
	out := a.svc.sealMediaTags(alice.id, "Here.\nMEDIA:"+path+". And MEDIA:/opt/data/profiles/x/notes.txt done")
	if strings.Contains(out, "/opt/data") || strings.Contains(out, alice.profile) || strings.Contains(out, "notes.txt") {
		t.Fatalf("a path survived: %q", out)
	}
	if !sealedTag.MatchString(out) || !strings.HasPrefix(out, "Here.\nMEDIA:image-") || !strings.Contains(out, ". And  done") {
		t.Fatalf("sealed text: %q", out)
	}
}

func TestGeneratedMediaIsServedOnlyToTheUserItWasIssuedTo(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bob")
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/a.png"
	m := &fakeMedia{owner: alice.profile, path: path}
	a.svc.media = m
	id := sealedTag.FindStringSubmatch(a.svc.sealMediaTags(alice.id, "MEDIA:"+path))[1]
	q := "/api/media?id=" + url.QueryEscape(id)

	r := alice.get(q)
	if r.Code != http.StatusOK || r.Body.String() != "\x89PNG-0123456789" || r.Header().Get("Content-Type") != "image/png" ||
		r.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header().Get("Content-Security-Policy"), "sandbox") ||
		r.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("owner: %d %q %v", r.Code, r.Body, r.Header())
	}
	if r := alice.do("GET", q, map[string]string{"Range": "bytes=0-3"}, nil); r.Code != http.StatusPartialContent || r.Body.String() != "\x89PNG" {
		t.Fatalf("range: %d %q", r.Code, r.Body)
	}
	// Bob holding Alice's id: it does not open for him, and the helper is never asked.
	before := len(m.asked)
	if r := bob.get(q); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "PNG") {
		t.Fatalf("other user: %d", r.Code)
	}
	for _, bad := range []string{"", "image-", "image-AAAA", "image-" + strings.Repeat("A", 80)} {
		if r := alice.get("/api/media?id=" + url.QueryEscape(bad)); r.Code != http.StatusNotFound {
			t.Errorf("%q: %d", bad, r.Code)
		}
	}
	if len(m.asked) != before {
		t.Error("an id that does not open for the caller reached the helper")
	}
	// A helper that cannot answer is a visible failure, not "no media".
	m.broken = true
	if r := alice.get(q); r.Code != http.StatusBadGateway {
		t.Errorf("broken helper: %d", r.Code)
	}
	a.svc.media = nil
	if r := alice.get(q); r.Code != http.StatusNotFound {
		t.Errorf("no helper configured: %d", r.Code)
	}
}

// A generated file's location is never searchable and never comes back in a snippet (#168 review).
func TestSearchNeverReturnsAGeneratedFilesPath(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	chat := alice.newChat()
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/apple.png"
	a.setTranscript(t, alice, chat, [2]string{"user", "draw an apple"}, [2]string{"assistant", "Your apple: MEDIA:" + path + " enjoy the apple"})
	r := alice.get("/api/search/chats?q=apple")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "enjoy") {
		t.Fatalf("search: %d %s", r.Code, r.Body.String())
	}
	if body := r.Body.String(); strings.Contains(body, "/opt/data") || strings.Contains(body, alice.profile) || strings.Contains(body, "MEDIA:") {
		t.Fatalf("a path reached the search results: %s", body)
	}
	if got := stripMediaTags("a MEDIA:/x/y.png b MEDIA:\"/p q.mp3\" c"); got != "a  b  c" {
		t.Errorf("stripMediaTags: %q", got)
	}
}

func TestStreamMediaStripsTagsSplitAcrossDeltas(t *testing.T) {
	deltas := []string{"Here is ", "your image: ME", "DIA:/opt/data/profiles/u_x/cache/gen", "erated/a.png", "\nEnjoy. A MEDIAtor too."}
	var out strings.Builder
	hold := ""
	for _, d := range deltas {
		var text string
		text, hold = streamMedia(hold, d)
		out.WriteString(text)
	}
	out.WriteString(stripMediaTags(hold))
	if got := out.String(); got != "Here is your image: \nEnjoy. A MEDIAtor too." {
		t.Fatalf("streamed text: %q", got)
	}
	// A quoted tag with a space, closed only in a later delta.
	text1, hold := streamMedia("", `MEDIA:"/a b`)
	text2, hold := streamMedia(hold, `/c.png" ok`)
	if text1+text2+hold != " ok" {
		t.Fatalf("quoted: %q %q %q", text1, text2, hold)
	}
}

// The live stream never carries a generated file's location: not in a delta (even split across deltas), not in the
// terminal output (#168 review).
func TestRunStreamNeverCarriesAGeneratedFilesPath(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	run := alice.send(alice.newChat(), "draw it")
	_, fr := a.hx.runWithInput(t, "draw it")
	st := alice.open(run, "")
	recv(t, fr.opens, "upstream stream")
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/a.png"
	for _, piece := range []string{"Done: ME", "DIA:" + path[:20], path[20:], " enjoy"} {
		frame, _ := json.Marshal(map[string]string{"delta": piece})
		a.hx.push(fr, "assistant.delta", string(frame))
	}
	out, _ := json.Marshal(map[string]string{"output": "Done: MEDIA:" + path + " enjoy"})
	a.hx.push(fr, "run.completed", string(out))
	st.waitFor(t, "run.completed")
	for _, e := range st.events() {
		if strings.Contains(e.Data, "/opt/data") || strings.Contains(e.Data, alice.profile) || strings.Contains(e.Data, "MEDIA:") {
			t.Fatalf("%s carried a path: %s", e.Name, e.Data)
		}
	}
	if got := st.text(); got != "Done:  enjoy" {
		t.Errorf("streamed text: %q", got)
	}
}

func TestStreamMediaDropsAnOversizedTagWithoutLeakingItsRest(t *testing.T) {
	var out strings.Builder
	hold := ""
	feed := func(d string) {
		var text string
		text, hold = streamMedia(hold, d)
		out.WriteString(text)
		if len(hold) > 9<<10 {
			t.Fatalf("hold grew to %d bytes", len(hold))
		}
	}
	feed("Look: MEDIA:/opt/data/profiles/u_x/cache/")
	for i := 0; i < 5; i++ {
		feed(strings.Repeat("a", 4<<10)) // the tag keeps going past 8 KiB
	}
	feed("SECRETTAIL.png")
	feed(" after")
	out.WriteString(stripMediaTags(hold))
	if got := out.String(); got != "Look:  after" {
		t.Fatalf("streamed text: %.80q", got)
	}
	// A quoted one, too.
	out.Reset()
	hold = ""
	feed(`MEDIA:"/a b ` + strings.Repeat("c", 9<<10))
	feed(`SECRET d.png" ok`)
	out.WriteString(stripMediaTags(hold))
	if got := out.String(); got != " ok" {
		t.Fatalf("quoted: %.80q", got)
	}
}
