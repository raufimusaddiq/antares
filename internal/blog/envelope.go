package blog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The envelope is what the author's assistant returns from a check (specs/001-blog-cms-openui-publish/contracts/
// check-envelope.md). It is untrusted: the whole reply is refused if it is not the envelope at all, and each note and
// block that fails is dropped on its own with a one-sentence notice for the author. Nothing is repaired by guessing, and
// nothing in it can change the author's document, address, title or account.

const (
	envelopeVersion = 1
	maxEnvelope     = 256 << 10
	maxNotes        = 20
	maxBlocks       = 10
	maxSourcesPer   = 10
	maxNoteText     = 600
	maxFallback     = 2000
)

var ErrEnvelope = errors.New("the assistant's reply is not usable")

var noteKinds = setOfNames("clarity", "fact", "missing-context", "unsourced")

// blockKinds is the catalogue of what a block may be called; the kind is a label for the author, not a component.
var blockKinds = setOfNames("table", "chart", "metric", "accordion", "sources", "list", "callout", "tabs", "steps", "summary", "timeline", "comparison")

type rawEnvelope struct {
	Version int `json:"version"`
	Notes   []struct {
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		AfterNode *int   `json:"afterNode"`
	} `json:"notes"`
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
	Markup    string
	Fallback  string
	Sources   []Source
	// NeedsAck: acceptance must be explicit. Always true for a block that shows data, because no source can be verified
	// (research R4); a block with no data and no sources needs none.
	NeedsAck bool
}

// Result is the validated envelope.
type Result struct {
	Notes  []Note
	Blocks []Block
	// Notices has one plain sentence per note or block that was dropped. The assistant's own words are never in it.
	Notices []string
}

// ParseEnvelope validates the assistant's final output against the version it checked. topNodes is the number of
// top-level nodes of that version's document (an anchor must be one of them).
func ParseEnvelope(raw []byte, topNodes int, now time.Time) (Result, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > maxEnvelope {
		return Result{}, ErrEnvelope
	}
	raw = unfence(raw)
	var env rawEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&env); err != nil || dec.More() || env.Version != envelopeVersion {
		return Result{}, ErrEnvelope
	}
	if len(env.Notes) > maxNotes || len(env.Blocks) > maxBlocks {
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
		res.Blocks = append(res.Blocks, Block{Kind: b.Kind, AfterNode: *b.AfterNode, Markup: prog.Markup, Fallback: fallback, Sources: kept, NeedsAck: len(kept) > 0})
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
