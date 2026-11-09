package blog

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

const src1 = "https://example.org/report"

func TestValidMarkupIsAccepted(t *testing.T) {
	good := `root = Card([hdr, txt, tbl, chart], [{title: "Report", sourceName: "example.org", url: "https://example.org/report"}])
hdr = CardHeader("Compare", "Three plans")
txt = TextContent("**A** is cheapest, see [the report](https://example.org/report).")
tbl = Table([Col("Plan", ["A", "B"]), Col("Cost ($)", [1, 2.5])])
chart = BarChart(["A", "B"], [Series("Cost", [1, 2.5])], "grouped", null, null)
`
	p, err := ValidateMarkup(good, []string{src1})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Data {
		t.Error("a Table is data: the block must cite a source")
	}
	text, err := ValidateMarkup(`root = Card([t])`+"\n"+`t = TextContent("Plain words.")`, nil)
	if err != nil || text.Data {
		t.Fatalf("%v %v", err, text.Data)
	}
}

func TestMarkupIsRefused(t *testing.T) {
	wrap := func(body string) string { return "root = Card([x])\nx = " + body }
	for name, m := range map[string]string{
		"no root":             `x = TextContent("a")`,
		"root not a Card":     `root = TextContent("a")`,
		"unknown component":   wrap(`Marquee("a")`),
		"form":                wrap(`Form("f", b, [])`) + "\nb = Buttons([])",
		"button":              wrap(`Button("go")`),
		"follow up":           wrap(`FollowUpBlock([])`),
		"image":               wrap(`Image("alt", "https://example.org/a.png")`),
		"image field":         wrap(`ListBlock([ListItem("t", "s", {src: "https://example.org/a.png", alt: "a"})])`),
		"action":              wrap(`TextContent("a", @ToAssistant("x"))`),
		"open url":            wrap(`ListItem("t", "s", null, "go", @OpenUrl("https://example.org/report"))`),
		"binding":             wrap(`Callout("info", "t", "d", $visible)`),
		"state ref":           wrap(`TextContent(Query)`),
		"html":                wrap(`TextContent("<img src=x onerror=alert(1)>")`),
		"script tag":          wrap(`TextContent("a <script>alert(1)</script>")`),
		"markdown image":      wrap(`TextContent("![x](https://example.org/a.png)")`),
		"javascript link":     wrap(`TextContent("[x](javascript:alert(1))")`),
		"data uri":            wrap(`TextContent("see data:text/html,x")`),
		"link not cited":      wrap(`TextContent("[x](https://evil.example/)")`),
		"bare url not cited":  wrap(`TextContent("visit https://evil.example/ now")`),
		"autolink not cited":  wrap(`TextContent("<https://evil.example/>")`),
		"www not cited":       wrap(`TextContent("go to www.evil.example")`),
		"url field not cited": wrap(`Callout("info", "t", "d")`) + "\nroot2 = Card([{title: \"x\", url: \"https://evil.example/\"}])",
		"private link":        wrap(`TextContent("[x](https://10.0.0.1/)")`),
		"unterminated":        wrap(`TextContent("a`),
		"bad escape":          wrap(`TextContent("a\q")`),
		"trailing junk":       wrap(`TextContent("a") extra`),
		"two on a line":       wrap(`TextContent("a")`) + ` y = TextContent("b")`,
		"duplicate":           wrap(`TextContent("a")`) + "\nx = TextContent(\"b\")",
		"dangling ref":        "root = Card([nope])",
		"cycle":               "root = Card([a])\na = Card([b])\nb = Card([a])",
		"self reference":      "root = Card([root])",
		"operator":            wrap(`TextContent("a" + "b")`),
		"member access":       wrap(`TextContent(data.x)`),
		"control char":        wrap("TextContent(\"a\x00b\")"),
		"bidi override":       wrap("TextContent(\"a‮b\")"),
		"colon argument":      wrap(`CardHeader(title: "x")`),
		"empty":               ``,
	} {
		if _, err := ValidateMarkup(m, []string{src1}); err == nil {
			t.Errorf("%s was accepted:\n%s", name, m)
		}
	}
}

func TestMarkupLimits(t *testing.T) {
	// Statements: unreferenced definitions are legal, so only the count can refuse this one.
	var many strings.Builder
	many.WriteString("root = Card([])\n")
	for i := 0; i < maxStatements; i++ {
		many.WriteString("s" + strconv.Itoa(i) + " = TextContent(\"a\")\n")
	}
	if _, err := ValidateMarkup(many.String(), nil); err == nil {
		t.Error("more statements than the limit were accepted")
	}
	// ...and one fewer is fine, so the limit is the cause.
	fewer := strings.Replace(many.String(), "s0 = TextContent(\"a\")\n", "", 1)
	if _, err := ValidateMarkup(fewer, nil); err != nil {
		t.Errorf("a program at the limit was refused: %v", err)
	}
	// Nesting depth.
	nest := func(n int) string {
		e := `TextContent("a")`
		for i := 0; i < n; i++ {
			e = "Card([" + e + "])"
		}
		return "root = " + e
	}
	if _, err := ValidateMarkup(nest(maxNesting/2-1), nil); err != nil {
		t.Errorf("nesting within the limit was refused: %v", err)
	}
	if _, err := ValidateMarkup(nest(maxNesting), nil); err == nil {
		t.Error("a program nested past the limit was accepted")
	}
	// Arrays count too: brackets cannot be used to get around the limit.
	brackets := "root = Card([" + strings.Repeat("[", maxNesting+2) + strings.Repeat("]", maxNesting+2) + "])"
	if _, err := ValidateMarkup(brackets, nil); err == nil {
		t.Error("arrays nested past the limit were accepted")
	}
	// Strings and total size.
	if _, err := ValidateMarkup("root = Card([t])\nt = TextContent(\""+strings.Repeat("a", maxString+1)+"\")", nil); err == nil {
		t.Error("an oversize string was accepted")
	}
	if _, err := ValidateMarkup("root = Card([t])\nt = TextContent(\""+strings.Repeat("a", maxMarkupBytes)+"\")", nil); err == nil {
		t.Error("oversize markup was accepted")
	}
	// Array length.
	arr := "root = Card([t])\nt = Col(\"n\", [" + strings.TrimSuffix(strings.Repeat("1,", maxArray+1), ",") + "])"
	if _, err := ValidateMarkup(arr, nil); err == nil {
		t.Error("an oversize array was accepted")
	}
}

// The recorded reply of the deployed Hermes (testdata/blog_check_output.txt) is the chat's own OpenUI. It uses components a
// published post may not (follow-up buttons), and cites addresses with no scheme: the validator must say no, never crash.
func TestTheRecordedChatReplyIsNotAPublishedBlock(t *testing.T) {
	raw, err := os.ReadFile("../hermes/testdata/blog_check_output.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMarkup(string(raw), nil); err == nil {
		t.Error("a chat reply with follow-up buttons was accepted as a published block")
	}
}
