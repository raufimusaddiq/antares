package blog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var envNow = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

const okMarkup = `root = Card([t, tbl])\nt = TextContent(\"Cited: [report](https://example.org/r).\")\ntbl = Table([Col(\"A\", [1, 2])])`

func env(blocks, notes string) string {
	return `{"version":1,"notes":[` + notes + `],"blocks":[` + blocks + `]}`
}

const okBlock = `{"kind":"table","afterNode":1,"markup":"` + okMarkup + `","fallback":"A: 1, 2","sources":[{"url":"https://example.org/r","title":"The report","retrievedAt":"2026-11-08"}]}`

func TestAValidEnvelopeIsKept(t *testing.T) {
	res, err := ParseEnvelope([]byte(env(okBlock, `{"kind":"fact","text":"  Check the   2019 figure. ","afterNode":0}`)), 3, envNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) != 1 || res.Notes[0].Text != "Check the 2019 figure." || res.Notes[0].AfterNode != 0 {
		t.Fatalf("notes: %+v", res.Notes)
	}
	if len(res.Blocks) != 1 || len(res.Notices) != 0 {
		t.Fatalf("blocks %d notices %v", len(res.Blocks), res.Notices)
	}
	b := res.Blocks[0]
	if !b.NeedsAck || b.Sources[0].Verified || b.AfterNode != 1 {
		t.Fatalf("a data block with a source needs the author's acknowledgement and is never verified: %+v", b)
	}
}

func TestAFencedEnvelopeIsAccepted(t *testing.T) {
	if _, err := ParseEnvelope([]byte("```json\n"+env("", "")+"\n```"), 1, envNow); err != nil {
		t.Fatal(err)
	}
}

func TestAReplyThatIsNotTheEnvelopeIsRefusedWhole(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":            "",
		"prose":            "Here is my feedback: it reads well.",
		"not json":         "{not json",
		"wrong version":    `{"version":3,"notes":[],"blocks":[]}`,
		"no version":       `{"notes":[],"blocks":[]}`,
		"trailing text":    env("", "") + ` and some more`,
		"two documents":    env("", "") + env("", ""),
		"too many notes":   env("", strings.TrimSuffix(strings.Repeat(`{"kind":"fact","text":"x","afterNode":0},`, maxNotes+1), ",")),
		"too many blocks":  env(strings.TrimSuffix(strings.Repeat(okBlock+",", maxBlocks+1), ","), ""),
		"oversize":         `{"version":1,"notes":[],"blocks":[],"pad":"` + strings.Repeat("x", maxEnvelope) + `"}`,
		"array at the top": `[]`,
	} {
		if _, err := ParseEnvelope([]byte(raw), 3, envNow); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestEachBadPartIsDroppedOnItsOwnWithAPlainNotice(t *testing.T) {
	cases := map[string]string{
		"unknown kind":       `{"kind":"banner","afterNode":0,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"anchor past end":    `{"kind":"table","afterNode":9,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"negative anchor":    `{"kind":"table","afterNode":-1,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"no anchor":          `{"kind":"table","markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"no fallback":        `{"kind":"table","afterNode":0,"markup":"` + okMarkup + `","fallback":" ","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"private source":     `{"kind":"table","afterNode":0,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://10.0.0.1/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"http source":        `{"kind":"table","afterNode":0,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"http://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"credentialed":       `{"kind":"table","afterNode":0,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://u:p@example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"bad date":           `{"kind":"table","afterNode":0,"markup":"` + okMarkup + `","fallback":"x","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"soon"}]}`,
		"link not in source": `{"kind":"table","afterNode":0,"markup":"root = Card([t])\nt = TextContent(\"[x](https://evil.example/)\")","fallback":"x","sources":[{"url":"https://example.org/r","title":"t","retrievedAt":"2026-11-08"}]}`,
		"button":             `{"kind":"list","afterNode":0,"markup":"root = Card([b])\nb = Button(\"go\")","fallback":"x","sources":[]}`,
		"raw html":           `{"kind":"callout","afterNode":0,"markup":"root = Card([t])\nt = TextContent(\"<script>x</script>\")","fallback":"x","sources":[]}`,
	}
	for name, block := range cases {
		res, err := ParseEnvelope([]byte(env(block+","+okBlock, "")), 3, envNow)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(res.Blocks) != 1 || len(res.Notices) != 1 {
			t.Errorf("%s: kept %d blocks, %d notices (%v): the good block must survive and the bad one be dropped", name, len(res.Blocks), len(res.Notices), res.Notices)
			continue
		}
		if !strings.HasPrefix(res.Notices[0], "Block 1 was removed") {
			t.Errorf("%s: notice %q", name, res.Notices[0])
		}
	}
}

func TestDataWithoutASourceIsDroppedAndNoted(t *testing.T) {
	block := `{"kind":"table","afterNode":2,"markup":"root = Card([tbl])\ntbl = Table([Col(\"A\", [1])])","fallback":"A: 1","sources":[]}`
	res, err := ParseEnvelope([]byte(env(block, "")), 3, envNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 0 || len(res.Notices) != 1 || len(res.Notes) != 1 || res.Notes[0].Kind != "unsourced" || res.Notes[0].AfterNode != 2 {
		t.Fatalf("%+v", res)
	}
	// Words and no data need no source.
	text := `{"kind":"summary","afterNode":0,"markup":"root = Card([t])\nt = TextContent(\"A summary.\")","fallback":"A summary.","sources":[]}`
	res, _ = ParseEnvelope([]byte(env(text, "")), 3, envNow)
	if len(res.Blocks) != 1 || res.Blocks[0].NeedsAck {
		t.Fatalf("%+v", res)
	}
}

func TestBadNotesAreDropped(t *testing.T) {
	notes := strings.Join([]string{
		`{"kind":"opinion","text":"x","afterNode":0}`,
		`{"kind":"fact","text":"x","afterNode":7}`,
		`{"kind":"fact","text":"  ","afterNode":0}`,
		`{"kind":"fact","text":"` + strings.Repeat("x", maxNoteText+1) + `","afterNode":0}`,
		`{"kind":"clarity","text":"Fine.","afterNode":1}`,
	}, ",")
	res, err := ParseEnvelope([]byte(env("", notes)), 3, envNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) != 1 || res.Notes[0].Text != "Fine." || len(res.Notices) != 4 {
		t.Fatalf("%+v", res)
	}
}

func TestNoticesNeverCarryTheAssistantsWords(t *testing.T) {
	secret := "SECRET-MODEL-TEXT"
	block := `{"kind":"banner","afterNode":0,"markup":"` + secret + `","fallback":"` + secret + `","sources":[{"url":"https://` + secret + `.example/","title":"` + secret + `","retrievedAt":"2026-11-08"}]}`
	res, err := ParseEnvelope([]byte(env(block, `{"kind":"x","text":"`+secret+`","afterNode":0}`)), 3, envNow)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Notices)
	if strings.Contains(string(b), secret) {
		t.Fatalf("a notice repeats the assistant's text: %s", b)
	}
}

func TestAStyleNoteIsAccepted(t *testing.T) {
	res, err := ParseEnvelope([]byte(env("", `{"kind":"style","text":"Hype words in the opening.","afterNode":0}`)), 3, envNow)
	if err != nil || len(res.Notes) != 1 || res.Notes[0].Kind != "style" {
		t.Fatalf("%v %+v", err, res)
	}
	res, _ = ParseEnvelope([]byte(env("", `{"kind":"tone","text":"x","afterNode":0}`)), 3, envNow)
	if len(res.Notes) != 0 {
		t.Fatalf("an unknown note kind was kept: %+v", res.Notes)
	}
}

// ---- edits (specs/006-ai-editor-blog, contracts/check-envelope-v2.md) ----

var editDraft = DraftInput{
	Title: "Self-hosting an assistant",
	Parts: []string{
		"Running your own assistant sounds cheap until the bill arrives. It costs 24 dollars a month.",
		"## Hardware",
		"- a small server is enough\n- power and cooling",
		"Plain closing paragraph.",
	},
}

func init() {
	for _, p := range editDraft.Parts {
		editDraft.Anchors = append(editDraft.Anchors, strings.TrimPrefix(strings.TrimPrefix(strings.Split(p, "\n")[0], "## "), "- "))
	}
}

func envEdits(edits ...string) []byte {
	return []byte(`{"version":2,"notes":[],"blocks":[],"edits":[` + strings.Join(edits, ",") + `]}`)
}

func TestVersionOneRepliesAreStillAcceptedAndHaveNoEdits(t *testing.T) {
	res, err := ParseEnvelopeParts([]byte(env("", "")), editDraft, envNow)
	if err != nil || len(res.Edits) != 0 {
		t.Fatalf("%v %+v", err, res.Edits)
	}
}

func TestGoodEditsAreKeptInTheOrderGiven(t *testing.T) {
	res, err := ParseEnvelopeParts(envEdits(
		`{"kind":"replace","part":0,"original":"sounds cheap until the bill arrives","proposed":"looks cheap until the first bill","reason":"Shorter."}`,
		`{"kind":"insert","part":1,"proposed":"A small server is enough for one person.\n\nIt still needs power and cooling.","reason":"Builds on your points."}`,
		`{"kind":"replace","part":2,"original":"a small server is enough","proposed":"one small server is enough","reason":"Plainer."}`,
	), editDraft, envNow)
	if err != nil || len(res.Edits) != 3 || len(res.Notices) != 0 {
		t.Fatalf("%v %+v %v", err, res.Edits, res.Notices)
	}
	if res.Edits[0].Kind != "replace" || res.Edits[0].Anchor != "Running your own assistant sounds cheap until the bill arrives. It costs 24 dollars a month." || res.Edits[1].Kind != "insert" || res.Edits[1].Anchor != "Hardware" || res.Edits[2].Part != 2 {
		t.Fatalf("%+v", res.Edits)
	}
}

func TestABadEditIsDroppedOnItsOwnWithOneSentenceAndTheRestStay(t *testing.T) {
	good := `{"kind":"replace","part":3,"original":"Plain closing","proposed":"Closing","reason":"Shorter."}`
	long := strings.Repeat("x", 1001)
	bad := map[string]string{
		"unknown kind":              `{"kind":"delete","part":0,"original":"sounds cheap","proposed":"x","reason":"r"}`,
		"part past the end":         `{"kind":"replace","part":9,"original":"sounds cheap","proposed":"x","reason":"r"}`,
		"no part":                   `{"kind":"replace","original":"sounds cheap","proposed":"x","reason":"r"}`,
		"empty original":            `{"kind":"replace","part":0,"original":"","proposed":"x","reason":"r"}`,
		"original over 1000":        `{"kind":"replace","part":0,"original":"` + long + `","proposed":"x","reason":"r"}`,
		"original with a newline":   `{"kind":"replace","part":2,"original":"a small server is enough\n- power","proposed":"x","reason":"r"}`,
		"original not in the part":  `{"kind":"replace","part":0,"original":"never written","proposed":"x","reason":"r"}`,
		"original in another part":  `{"kind":"replace","part":3,"original":"sounds cheap","proposed":"x","reason":"r"}`,
		"original with the marker":  `{"kind":"replace","part":1,"original":"## Hardware","proposed":"x","reason":"r"}`,
		"proposed empty":            `{"kind":"replace","part":0,"original":"sounds cheap","proposed":"","reason":"r"}`,
		"proposed over 4000":        `{"kind":"replace","part":0,"original":"sounds cheap","proposed":"` + strings.Repeat("y", 4001) + `","reason":"r"}`,
		"proposed equals original":  `{"kind":"replace","part":0,"original":"sounds cheap","proposed":"sounds cheap","reason":"r"}`,
		"insert over 8000":          `{"kind":"insert","part":0,"proposed":"` + strings.Repeat("y", 8001) + `","reason":"r"}`,
		"insert with 13 paragraphs": `{"kind":"insert","part":0,"proposed":"` + strings.TrimSuffix(strings.Repeat("Para.\\n\\n", 13), "\\n\\n") + `","reason":"r"}`,
		"insert with an original":   `{"kind":"insert","part":0,"original":"x","proposed":"New text.","reason":"r"}`,
		"reason empty":              `{"kind":"insert","part":0,"proposed":"New text.","reason":""}`,
		"reason over 300":           `{"kind":"insert","part":0,"proposed":"New text.","reason":"` + strings.Repeat("z", 301) + `"}`,
		"control character":         `{"kind":"insert","part":0,"proposed":"New\u0007text.","reason":"r"}`,
		"html tag":                  `{"kind":"insert","part":0,"proposed":"New <b>text</b>.","reason":"r"}`,
		"markdown link":             `{"kind":"insert","part":0,"proposed":"See [the post](x).","reason":"r"}`,
		"url":                       `{"kind":"insert","part":0,"proposed":"See https://example.org/page for more.","reason":"r"}`,
		"bare address":              `{"kind":"insert","part":0,"proposed":"See example.com for more.","reason":"r"}`,
		"e-mail address":            `{"kind":"insert","part":0,"proposed":"Write to me@example.org today.","reason":"r"}`,
		"a figure not in the draft": `{"kind":"insert","part":0,"proposed":"It costs 99 dollars a month.","reason":"r"}`,
		"a quote not in the draft":  `{"kind":"insert","part":0,"proposed":"As the report put it, \"the entire industry is changing faster than anyone expected\" this year.","reason":"r"}`,
	}
	for name, e := range bad {
		res, err := ParseEnvelopeParts(envEdits(e, good), editDraft, envNow)
		if err != nil {
			t.Errorf("%s: the reply was refused whole: %v", name, err)
			continue
		}
		if len(res.Edits) != 1 || res.Edits[0].Part != 3 || len(res.Notices) != 1 || !strings.HasPrefix(res.Notices[0], "Suggestion 1 was removed") {
			t.Errorf("%s: edits %+v notices %v", name, res.Edits, res.Notices)
		}
	}
}

func TestAFigureThatIsInTheDraftIsAllowed(t *testing.T) {
	res, err := ParseEnvelopeParts(envEdits(`{"kind":"replace","part":0,"original":"It costs 24 dollars a month.","proposed":"It costs 24 dollars each month.","reason":"Plainer."}`), editDraft, envNow)
	if err != nil || len(res.Edits) != 1 {
		t.Fatalf("%v %+v %v", err, res.Edits, res.Notices)
	}
	res, _ = ParseEnvelopeParts(envEdits(`{"kind":"insert","part":0,"proposed":"A short quote: \"cheap until the bill\".","reason":"r"}`), editDraft, envNow)
	if len(res.Edits) != 1 {
		t.Fatalf("a short quote is allowed: %v", res.Notices)
	}
}

func TestMoreEditsThanTheDraftSupportsAreCutWithOneNotice(t *testing.T) {
	one := `{"kind":"insert","part":0,"proposed":"Text.","reason":"r"}`
	max := 4*len(editDraft.Parts) + 8
	res, err := ParseEnvelopeParts(envEdits(strings.TrimSuffix(strings.Repeat(one+",", max+3), ",")), editDraft, envNow)
	if err != nil || len(res.Edits) != max {
		t.Fatalf("%v %d", err, len(res.Edits))
	}
	if len(res.Notices) != 1 {
		t.Fatalf("notices %v", res.Notices)
	}
}
