package blog

import (
	"encoding/json"
	"strings"
)

// Anchors (specs/006-ai-editor-blog, research R6). A suggestion or a block is placed by the top-level part of the draft it was made for. The part's index
// moves when the author edits above it, so each one also keeps the anchor of its part: the text of the part's first text block, whitespace collapsed,
// at most anchorRunes characters. To use it again the current draft is searched for a part with that anchor, nearest to the old index first. The same rule is
// written in web/src/blog/anchors.ts; internal/blog/testdata/anchors.json is read by both tests so they cannot drift apart.

const anchorRunes = 120

// textBlock reports whether a node holds inline content (what a text block is in the editor).
func textBlock(t string) bool { return t == "paragraph" || t == "heading" || t == "codeBlock" }

func inlineText(n node) string {
	if n.Type == "text" {
		return n.Text
	}
	var b strings.Builder
	for _, c := range n.Content {
		b.WriteString(inlineText(c))
	}
	return b.String()
}

// firstTextBlock is the text of the first text block inside n (n itself when it is one), and whether there is one.
func firstTextBlock(n node) (string, bool) {
	if textBlock(n.Type) {
		return inlineText(n), true
	}
	for _, c := range n.Content {
		if t, ok := firstTextBlock(c); ok {
			return t, true
		}
	}
	return "", false
}

// anchorOf is the anchor of one top-level part: "" when it has no text (an image alone).
func anchorOf(n node) string {
	t, _ := firstTextBlock(n)
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > anchorRunes {
		t = strings.TrimSpace(string(r[:anchorRunes]))
	}
	return t
}

// partAnchors is the anchor of every top-level part of a document.
func partAnchors(doc []byte) ([]string, error) {
	var root node
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, ErrInvalid
	}
	out := make([]string, len(root.Content))
	for i, n := range root.Content {
		out[i] = anchorOf(n)
	}
	return out, nil
}

// relocate finds the part with this anchor, the one nearest to hint (the earlier on a tie), or -1. An empty anchor (a part with no text) is
// kept where it was when a part is still there.
func relocate(anchors []string, anchor string, hint int) int {
	if anchor == "" {
		if hint >= 0 && hint < len(anchors) && anchors[hint] == "" {
			return hint
		}
		return -1
	}
	best, bestDist := -1, 0
	for i, a := range anchors {
		if a != anchor {
			continue
		}
		d := i - hint
		if d < 0 {
			d = -d
		}
		if best < 0 || d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}
