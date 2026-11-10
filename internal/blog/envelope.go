package blog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The envelope is what the author's assistant returns from a check (specs/001-blog-cms-openui-publish/contracts/
// check-envelope.md). It is untrusted: the whole reply is refused if it is not the envelope at all, and each note and
// block that fails is dropped on its own with a one-sentence notice for the author. Nothing is repaired by guessing, and
// nothing in it can change the author's document, address, title or account.

const (
	envelopeVersion = 2 // 1 is accepted too: it has no edits
	maxEnvelope     = 256 << 10
	maxNotes        = 20
	maxBlocks       = 10
	maxSourcesPer   = 10
	maxNoteText     = 600
	maxFallback     = 2000
)

var ErrEnvelope = errors.New("the assistant's reply is not usable")

var noteKinds = setOfNames("clarity", "fact", "missing-context", "unsourced", "style")

// blockKinds is the catalogue of what a block may be called; the kind is a label for the author, not a component.
var blockKinds = setOfNames("table", "chart", "metric", "accordion", "sources", "list", "callout", "tabs", "steps", "summary", "timeline", "comparison")

type rawEnvelope struct {
	Version int `json:"version"`
	Notes   []struct {
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		AfterNode *int   `json:"afterNode"`
	} `json:"notes"`
	Edits []struct {
		Kind     string `json:"kind"`
		Part     *int   `json:"part"`
		Original string `json:"original"`
		Proposed string `json:"proposed"`
		Reason   string `json:"reason"`
	} `json:"edits"`
	Blocks []struct {
		Kind      string   `json:"kind"`
		AfterNode *int     `json:"afterNode"`
		Markup    string   `json:"markup"`
		Fallback  string   `json:"fallback"`
		Sources   []Source `json:"sources"`
	} `json:"blocks"`
}

// Note is one remark for the author, anchored after a top-level node of the checked version. Plain text.
type Note struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	AfterNode int    `json:"afterNode"`
}

// Block is one proposed interactive block that passed every rule.
type Block struct {
	Kind      string
	AfterNode int
	Anchor    string // the anchor of that part in the version that was read (anchors.go); "" when the draft's parts are not known
	Markup    string
	Fallback  string
	Sources   []Source
	// NeedsAck: acceptance must be explicit. Always true for a block that shows data, because no source can be verified
	// (research R4); a block with no data and no sources needs none.
	NeedsAck bool
}

// Edit is one suggested change to the author's own text that passed every rule (specs/006-ai-editor-blog). Plain text throughout.
// replace: Original is an exact span inside one line of the part; insert: Proposed holds the paragraphs to place after the part.
type Edit struct {
	Kind     string
	Part     int
	Anchor   string // the part's anchor in the version that was read (anchors.go)
	Original string
	Proposed string
	Reason   string
}

// DraftInput is what the envelope is checked against: the title, the plain text of each top-level part as the assistant was shown it
// (checkInput), and each part's anchor.
type DraftInput struct {
	Title   string
	Parts   []string
	Anchors []string
}

// Result is the validated envelope.
type Result struct {
	Notes  []Note
	Edits  []Edit
	Blocks []Block
	// Notices has one plain sentence per note or block that was dropped. The assistant's own words are never in it.
	Notices []string
}

// ParseEnvelope validates the assistant's final output against the version it checked. topNodes is the number of
// top-level nodes of that version's document (an anchor must be one of them).
func ParseEnvelope(raw []byte, topNodes int, now time.Time) (Result, error) {
	return parseEnvelope(raw, topNodes, nil, now)
}

// ParseEnvelopeParts is ParseEnvelope for a draft whose parts are known, which is what allows the suggested edits to be checked against it.
func ParseEnvelopeParts(raw []byte, d DraftInput, now time.Time) (Result, error) {
	return parseEnvelope(raw, len(d.Parts), &d, now)
}

func parseEnvelope(raw []byte, topNodes int, draft *DraftInput, now time.Time) (Result, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > maxEnvelope {
		return Result{}, ErrEnvelope
	}
	raw = unfence(raw)
	var env rawEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&env); err != nil || dec.More() || (env.Version != 1 && env.Version != envelopeVersion) {
		return Result{}, ErrEnvelope
	}
	if len(env.Notes) > maxNotes || len(env.Blocks) > maxBlocks || len(env.Edits) > maxEnvelope/16 {
		return Result{}, ErrEnvelope
	}
	var res Result
	drop := func(format string, a ...any) { res.Notices = append(res.Notices, fmt.Sprintf(format, a...)) }
	anchored := func(n *int) bool { return n != nil && *n >= 0 && *n < topNodes }

	for i, n := range env.Notes {
		text := collapseSpace(n.Text)
		switch {
		case !noteKinds[n.Kind]:
			drop("Note %d was removed: it was not one of the kinds a note can be.", i+1)
		case !anchored(n.AfterNode):
			drop("Note %d was removed: it pointed at a part of the post that does not exist.", i+1)
		case text == "" || len([]rune(text)) > maxNoteText || !isPlainText(text):
			drop("Note %d was removed: its text was empty, too long or not plain text.", i+1)
		default:
			res.Notes = append(res.Notes, Note{Kind: n.Kind, Text: text, AfterNode: *n.AfterNode})
		}
	}
	if draft != nil {
		limit := 4*len(draft.Parts) + 8
		figs := draftFigures(*draft)
		all := draftText(*draft)
		for i, e := range env.Edits {
			if len(res.Edits) >= limit {
				drop("%d more suggestions were left out: that is more than this post can use.", len(env.Edits)-i)
				break
			}
			if why := checkEdit(e.Kind, e.Part, e.Original, e.Proposed, e.Reason, *draft, figs, all); why != "" {
				drop("Suggestion %d was removed: %s.", i+1, why)
				continue
			}
			res.Edits = append(res.Edits, Edit{Kind: e.Kind, Part: *e.Part, Anchor: draft.Anchors[*e.Part], Original: e.Original, Proposed: strings.TrimSpace(e.Proposed), Reason: collapseSpace(e.Reason)})
		}
	}
	for i, b := range env.Blocks {
		label := fmt.Sprintf("Block %d", i+1)
		if !blockKinds[b.Kind] {
			drop("%s was removed: it was not one of the kinds a block can be.", label)
			continue
		}
		if !anchored(b.AfterNode) {
			drop("%s was removed: it pointed at a part of the post that does not exist.", label)
			continue
		}
		fallback := strings.TrimSpace(b.Fallback)
		if fallback == "" || len([]rune(fallback)) > maxFallback || !isPlainText(fallback) {
			drop("%s was removed: it had no usable plain-text version.", label)
			continue
		}
		if len(b.Sources) > maxSourcesPer {
			drop("%s was removed: it listed too many sources.", label)
			continue
		}
		var kept []Source
		urls := make([]string, 0, len(b.Sources))
		badSource := false
		for _, s := range b.Sources {
			c, ok := cleanSource(s, now)
			if !ok {
				badSource = true
				break
			}
			kept, urls = append(kept, c), append(urls, c.URL)
		}
		if badSource {
			drop("%s was removed: one of its sources was not a public https address with a title and a date.", label)
			continue
		}
		prog, err := ValidateMarkup(b.Markup, urls)
		if err != nil {
			drop("%s was removed: it used something a published post cannot show.", label)
			continue
		}
		if prog.Data && len(kept) == 0 {
			drop("%s was removed: it shows data but cites no source.", label)
			// The author is told where the gap is, on the part of the post the block was meant for.
			if len(res.Notes) < maxNotes {
				res.Notes = append(res.Notes, Note{Kind: "unsourced", Text: "A block with data was proposed here, but it cited no source, so it was left out.", AfterNode: *b.AfterNode})
			}
			continue
		}
		res.Blocks = append(res.Blocks, Block{Kind: b.Kind, AfterNode: *b.AfterNode, Anchor: anchorFor(draft, *b.AfterNode), Markup: prog.Markup, Fallback: fallback, Sources: kept, NeedsAck: len(kept) > 0})
	}
	return res, nil
}

// unfence removes a Markdown code fence around the envelope, which assistants add unasked; nothing else is stripped.
func unfence(b []byte) []byte {
	s := string(b)
	if !strings.HasPrefix(s, "```") {
		return b
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 || !strings.HasSuffix(strings.TrimSpace(s), "```") {
		return b
	}
	body := strings.TrimSpace(s[nl+1:])
	return []byte(strings.TrimSpace(strings.TrimSuffix(body, "```")))
}

// ---- edits ----

const (
	maxOriginal   = 1000
	maxProposed   = 4000
	maxInsert     = 8000
	maxParagraphs = 12
	maxEditReason = 300
	maxQuoteWords = 6
)

var (
	figureRe  = regexp.MustCompile(`\d[\d.,]*\d|\d`)
	htmlTagRe = regexp.MustCompile(`<[A-Za-z/!][^>]*>`)
	addressRe = regexp.MustCompile(`(?i)(https?://|www\.|\]\(|\S+@\S+\.\S+|\b[a-z0-9-]+\.(com|org|net|io|id|dev|app|co|ai|gov|edu)\b)`)
	quoteRe   = regexp.MustCompile("[\u201c\"]([^\u201d\"]{1,600})[\u201d\"]")
)

func draftText(d DraftInput) string { return d.Title + "\n" + strings.Join(d.Parts, "\n") }

// draftFigures is every number in the draft (the title included), so a suggestion may reuse one and may not bring a new one.
func draftFigures(d DraftInput) map[string]bool {
	out := map[string]bool{}
	for _, f := range figureRe.FindAllString(draftText(d), -1) {
		out[strings.TrimRight(f, ".,")] = true
	}
	return out
}

// partLines are the lines of a part as the assistant was shown them, without the heading and list markers.
func partLines(part string) []string {
	lines := strings.Split(part, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimPrefix(l, "## "), "- ")
	}
	return lines
}

// checkEdit says in a few words why a suggestion cannot be kept, or "" when it can.
func checkEdit(kind string, part *int, original, proposed, reason string, d DraftInput, figs map[string]bool, all string) string {
	if kind != "replace" && kind != "insert" {
		return "it was not a kind of suggestion that exists"
	}
	if part == nil || *part < 0 || *part >= len(d.Parts) {
		return "it pointed at a part of the post that does not exist"
	}
	r, p := collapseSpace(reason), strings.TrimSpace(proposed)
	if r == "" || len([]rune(r)) > maxEditReason {
		return "it had no usable reason"
	}
	if p == "" {
		return "it proposed no text"
	}
	if !isPlainText(p) || !isPlainText(original) || !isPlainText(r) || htmlTagRe.MatchString(p+original+r) || addressRe.MatchString(p+" "+r) {
		return "its text was not plain text, or held a link or an address"
	}
	if kind == "replace" {
		if original == "" || len([]rune(original)) > maxOriginal || strings.ContainsAny(original, "\n\r") {
			return "the passage it replaces was empty, too long or spanned lines"
		}
		found := false
		for _, l := range partLines(d.Parts[*part]) {
			if strings.Contains(l, original) {
				found = true
				break
			}
		}
		if !found {
			return "the passage it replaces is not in your post"
		}
		if len([]rune(p)) > maxProposed {
			return "its proposed text was too long"
		}
		if p == original {
			return "it proposed the same text"
		}
	} else {
		if original != "" {
			return "an insertion cannot replace a passage"
		}
		if len([]rune(p)) > maxInsert || strings.Count(p, "\n\n")+1 > maxParagraphs {
			return "its proposed text was too long"
		}
	}
	for _, f := range figureRe.FindAllString(p, -1) {
		if !figs[strings.TrimRight(f, ".,")] {
			return "it contained a figure that is not in your draft"
		}
	}
	for _, q := range quoteRe.FindAllStringSubmatch(p, -1) {
		if len(strings.Fields(q[1])) > maxQuoteWords && !strings.Contains(all, q[1]) {
			return "it contained a quotation that is not in your draft"
		}
	}
	return ""
}

func anchorFor(d *DraftInput, part int) string {
	if d == nil || part < 0 || part >= len(d.Anchors) {
		return ""
	}
	return d.Anchors[part]
}
