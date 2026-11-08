package blog

import (
	"strings"
	"testing"
)

func TestCleanDocRefuses(t *testing.T) {
	p := func(inner string) string { return `{"type":"doc","content":[` + inner + `]}` }
	para := func(inner string) string { return `{"type":"paragraph","content":[` + inner + `]}` }
	text := func(s string) string { return `{"type":"text","text":"` + s + `"}` }
	for name, doc := range map[string]string{
		"not json":             `nope`,
		"wrong root":           `{"type":"paragraph"}`,
		"script node":          p(`{"type":"script","content":[` + text("alert(1)") + `]}`),
		"raw html node":        p(`{"type":"html","text":"<img src=x onerror=alert(1)>"}`),
		"unknown mark":         p(para(`{"type":"text","text":"x","marks":[{"type":"style"}]}`)),
		"javascript link":      p(para(`{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}`)),
		"data link":            p(para(`{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"data:text/html,x"}}]}`)),
		"relative link":        p(para(`{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"example.com"}}]}`)),
		"login in link":        p(para(`{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"https://u:p@example.com/"}}]}`)),
		"heading level 1":      p(`{"type":"heading","attrs":{"level":1},"content":[` + text("x") + `]}`),
		"text in doc":          p(text("x")),
		"paragraph in para":    p(para(para(""))),
		"http image":           p(`{"type":"image","attrs":{"src":"http://example.com/a.png"}}`),
		"javascript image":     p(`{"type":"image","attrs":{"src":"javascript:alert(1)"}}`),
		"foreign path image":   p(`{"type":"image","attrs":{"src":"/api/chats/abc/messages"}}`),
		"another post's image": p(`{"type":"image","attrs":{"src":"/api/blog/posts/zzzzzz99/assets/012345ab"}}`),
		"empty text":           p(para(`{"type":"text","text":""}`)),
	} {
		if _, err := CleanDoc([]byte(doc), "abcdef12"); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	deep := `{"type":"paragraph"}`
	for i := 0; i < 40; i++ {
		deep = `{"type":"blockquote","content":[` + deep + `]}`
	}
	if _, err := CleanDoc([]byte(p(deep)), "abcdef12"); err == nil {
		t.Error("a document nested 40 deep was accepted")
	}
	if _, err := CleanDoc([]byte(p(para(text(strings.Repeat("x", maxDocBytes))))), "abcdef12"); err == nil {
		t.Error("an oversize document was accepted")
	}
}

func TestCleanDocKeepsAndRebuilds(t *testing.T) {
	in := `{"type":"doc","content":[
	  {"type":"heading","attrs":{"level":2,"extra":"dropped"},"content":[{"type":"text","text":"Title"}]},
	  {"type":"paragraph","content":[{"type":"text","text":"hi","marks":[{"type":"bold"},{"type":"link","attrs":{"href":"https://example.org/a","target":"_blank","class":"x"}}]}]},
	  {"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"}]}]},
	  {"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]},
	  {"type":"image","attrs":{"src":"/api/blog/posts/abcdef12/assets/012345ab","alt":"a","onerror":"x"}},
	  {"type":"horizontalRule"}]}`
	out, err := CleanDoc([]byte(in), "abcdef12")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"dropped", "_blank", "onerror", `"class"`} {
		if strings.Contains(s, bad) {
			t.Errorf("%q survived the rebuild: %s", bad, s)
		}
	}
	for _, good := range []string{`"level":2`, `"href":"https://example.org/a"`, `"language":"go"`, `"alt":"a"`} {
		if !strings.Contains(s, good) {
			t.Errorf("%q lost: %s", good, s)
		}
	}
}
