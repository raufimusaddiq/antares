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
		"wrong version":    `{"version":2,"notes":[],"blocks":[]}`,
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
