// Package blog is the writing and publishing half of the blog: posts, their versions and images, and the public read
// of what authors published. Authenticated routes are scoped to the author in the same statement as the read or write;
// the public routes serve only published posts of approved authors and no Hermes data.
package blog

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// The document is the editor's JSON tree. It is validated against an allowlist and stored rebuilt from what passed:
// a node, mark or attribute outside the list is refused (nodes, marks) or dropped (attributes), never kept.
const (
	maxDocBytes = 512 << 10
	maxDepth    = 16
	maxNodes    = 20000
	maxTextRun  = 100000
)

type node struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []node         `json:"content,omitempty"`
	Text    string         `json:"text,omitempty"`
	Marks   []mark         `json:"marks,omitempty"`
}

type mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

var (
	errDoc = errors.New("document is not valid")

	blocks    = map[string]bool{"paragraph": true, "heading": true, "bulletList": true, "orderedList": true, "blockquote": true, "codeBlock": true, "horizontalRule": true, "image": true}
	inlines   = map[string]bool{"text": true, "hardBreak": true}
	marksOnly = map[string]bool{"bold": true, "italic": true, "code": true, "link": true}
)

// children says what each node may contain.
func allowedChild(parent, child string) bool {
	switch parent {
	case "doc", "blockquote", "listItem":
		return blocks[child]
	case "bulletList", "orderedList":
		return child == "listItem"
	case "paragraph", "heading":
		return inlines[child]
	case "codeBlock":
		return child == "text"
	}
	return false
}

// CleanDoc validates raw and returns it rebuilt from the allowed parts only.
func CleanDoc(raw []byte, postID string) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxDocBytes {
		return nil, errDoc
	}
	var root node
	if err := json.Unmarshal(raw, &root); err != nil || root.Type != "doc" {
		return nil, errDoc
	}
	n := 0
	out, err := cleanNode(root, "", 0, &n, regexp.MustCompile(`^/api/blog/posts/`+regexp.QuoteMeta(postID)+`/assets/[A-Za-z0-9_-]{6,64}$`))
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(out)
	if err != nil || len(b) > maxDocBytes {
		return nil, errDoc
	}
	return b, nil
}

func cleanNode(in node, parent string, depth int, count *int, ownImage *regexp.Regexp) (node, error) {
	*count++
	if depth > maxDepth || *count > maxNodes {
		return node{}, errDoc
	}
	out := node{Type: in.Type}
	if parent != "" && !allowedChild(parent, in.Type) {
		return node{}, errDoc
	}
	switch in.Type {
	case "doc", "blockquote", "listItem", "bulletList", "orderedList", "paragraph", "heading", "codeBlock":
	case "horizontalRule", "hardBreak":
		if len(in.Content) > 0 {
			return node{}, errDoc
		}
	case "text":
		if in.Text == "" || len(in.Text) > maxTextRun || len(in.Content) > 0 {
			return node{}, errDoc
		}
		out.Text = in.Text
		for _, m := range in.Marks {
			cm, err := cleanMark(m)
			if err != nil {
				return node{}, err
			}
			out.Marks = append(out.Marks, cm)
		}
		return out, nil
	case "image":
		if len(in.Content) > 0 {
			return node{}, errDoc
		}
		src, _ := in.Attrs["src"].(string)
		if !ownImage.MatchString(src) { // the page allows images from this site only (img-src 'self'), so a remote one could never show
			return node{}, errDoc
		}
		alt, _ := in.Attrs["alt"].(string)
		if r := []rune(alt); len(r) > 500 {
			alt = string(r[:500])
		}
		out.Attrs = map[string]any{"src": src, "alt": alt}
		return out, nil
	default:
		return node{}, errDoc
	}
	switch in.Type {
	case "heading":
		lvl, _ := in.Attrs["level"].(float64)
		if lvl < 2 || lvl > 4 {
			return node{}, errDoc
		}
		out.Attrs = map[string]any{"level": int(lvl)}
	case "orderedList":
		if s, ok := in.Attrs["start"].(float64); ok && s >= 1 && s <= 100000 {
			out.Attrs = map[string]any{"start": int(s)}
		}
	case "codeBlock":
		if l, ok := in.Attrs["language"].(string); ok && len(l) <= 30 && regexp.MustCompile(`^[A-Za-z0-9+#._-]*$`).MatchString(l) && l != "" {
			out.Attrs = map[string]any{"language": l}
		}
	}
	for _, c := range in.Content {
		cc, err := cleanNode(c, in.Type, depth+1, count, ownImage)
		if err != nil {
			return node{}, err
		}
		out.Content = append(out.Content, cc)
	}
	return out, nil
}

func cleanMark(m mark) (mark, error) {
	if !marksOnly[m.Type] {
		return mark{}, errDoc
	}
	if m.Type != "link" {
		return mark{Type: m.Type}, nil
	}
	href, _ := m.Attrs["href"].(string)
	if !absoluteLink(href) {
		return mark{}, errDoc
	}
	return mark{Type: "link", Attrs: map[string]any{"href": href}}, nil
}

// absoluteLink: http, https or mailto, written out in full. A relative address, another scheme or a login in the
// address is refused.
func absoluteLink(s string) bool {
	if len(s) > 2048 || strings.TrimSpace(s) != s {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != "" || u.Path != ""
	}
	return false
}

// emptyDoc is a new post's body.
const emptyDoc = `{"type":"doc","content":[{"type":"paragraph"}]}`
