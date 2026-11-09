package blog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The check's blocks are OpenUI Lang written by the author's assistant. Readers see them, so each is validated here before
// it is stored, against a read-only allowlist and default deny: whatever is not known and harmless is refused. The browser
// runs the same program through the OpenUI parser with a read-only library (specs T061); this is the part that does not
// trust the browser. Keep readerComponents in step with that library.

const (
	maxMarkupBytes = 20000
	maxMarkupNodes = 600
	maxNesting     = 24 // calls, arrays and objects inside one another
	maxStatements  = 300
	maxArray       = 200
	maxString      = 20000
)

// readerComponents is every component a published block may use: content, tables, charts, lists, sections and tags.
// Absent on purpose: images (a reader's browser would fetch them), forms and inputs, buttons, follow-ups and anything
// that sends text to an assistant or navigates (actions are refused outright, see below).
var readerComponents = setOfNames(
	"Card", "CardHeader", "TextContent", "MarkDownRenderer", "Callout", "TextCallout", "CodeBlock", "Separator", "InlineHeader",
	"Table", "Col",
	"BarChart", "LineChart", "AreaChart", "RadarChart", "HorizontalBarChart", "Series",
	"PieChart", "RadialChart", "SingleStackedBarChart", "Slice",
	"ScatterChart", "ScatterSeries", "Point",
	"ListBlock", "ListItem", "SectionBlock", "SectionItem", "Tabs", "TabItem", "Accordion", "AccordionItem", "Steps", "StepsItem",
	"TagBlock", "Tag", "EntityList", "Icon",
)

// dataComponents show data: a block that uses one needs a source (FR-016). Figures written into prose (TextContent) cannot
// be told from words by code, so they are not caught here: the instruction to the assistant says to put figures in a table,
// chart or list, and the author, who must accept every block, is the check on the rest.
var dataComponents = setOfNames("Table", "Col", "BarChart", "LineChart", "AreaChart", "RadarChart", "HorizontalBarChart",
	"PieChart", "RadialChart", "SingleStackedBarChart", "ScatterChart", "EntityList")

func setOfNames(n ...string) map[string]bool {
	m := make(map[string]bool, len(n))
	for _, s := range n {
		m[s] = true
	}
	return m
}

var errMarkup = errors.New("markup is not allowed")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errMarkup}, a...)...)
}

// ---- tokens ----

type tokKind int

const (
	tEOF tokKind = iota
	tNL
	tIdent
	tString
	tNumber
	tPunct // = ( ) [ ] { } , : .
)

type token struct {
	kind tokKind
	s    string
}

func lex(src string) ([]token, error) {
	var out []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '\n':
			out = append(out, token{tNL, "\n"})
			i++
		case c == '\r' || c == ' ' || c == '\t':
			i++
		case c == '"':
			var b strings.Builder
			i++
			for {
				if i >= len(rs) || rs[i] == '\n' {
					return nil, bad("a string is not closed")
				}
				if rs[i] == '\\' {
					if i+1 >= len(rs) {
						return nil, bad("a string is not closed")
					}
					switch rs[i+1] {
					case 'n':
						b.WriteRune('\n')
					case 't':
						b.WriteRune('\t')
					case '"', '\\', '/':
						b.WriteRune(rs[i+1])
					default:
						return nil, bad("an unknown escape in a string")
					}
					i += 2
					continue
				}
				if rs[i] == '"' {
					i++
					break
				}
				b.WriteRune(rs[i])
				i++
			}
			out = append(out, token{tString, b.String()})
		case c == '-' || unicode.IsDigit(c):
			j := i + 1
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.' || rs[j] == 'e' || rs[j] == 'E' || rs[j] == '+' || rs[j] == '-') {
				j++
			}
			if _, err := strconv.ParseFloat(string(rs[i:j]), 64); err != nil {
				return nil, bad("a malformed number")
			}
			out = append(out, token{tNumber, string(rs[i:j])})
			i = j
		case c == '_' || unicode.IsLetter(c):
			j := i + 1
			for j < len(rs) && (rs[j] == '_' || unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j])) {
				j++
			}
			out = append(out, token{tIdent, string(rs[i:j])})
			i = j
		case strings.ContainsRune("=()[]{},:", c):
			out = append(out, token{tPunct, string(c)})
			i++
		default:
			// '@' (an action), '$' (state or a binding), '.', operators, '?', '<', backticks: none is part of what a block may use.
			return nil, bad("the character %q is not allowed", c)
		}
	}
	out = append(out, token{tEOF, ""})
	return out, nil
}

// ---- syntax tree ----

type expr struct {
	kind  string // "call", "array", "object", "string", "number", "bool", "null", "ref"
	name  string // component, reference, or the string's text
	args  []*expr
	keys  []string // object keys, parallel to args
	depth int
}

type parser struct {
	toks  []token
	pos   int
	nodes int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) is(s string) bool {
	t := p.peek()
	return t.kind == tPunct && t.s == s
}
func (p *parser) skipNL() {
	for p.peek().kind == tNL {
		p.pos++
	}
}

func (p *parser) expect(s string) error {
	if !p.is(s) {
		return bad("expected %q", s)
	}
	p.pos++
	return nil
}

func (p *parser) parseExpr(depth int) (*expr, error) {
	if depth > maxNesting {
		return nil, bad("nested more than %d deep", maxNesting)
	}
	if p.nodes++; p.nodes > maxMarkupNodes {
		return nil, bad("more than %d elements", maxMarkupNodes)
	}
	t := p.next()
	switch t.kind {
	case tString:
		if len(t.s) > maxString {
			return nil, bad("a string longer than %d", maxString)
		}
		return &expr{kind: "string", name: t.s}, nil
	case tNumber:
		return &expr{kind: "number", name: t.s}, nil
	case tIdent:
		switch t.s {
		case "true", "false":
			return &expr{kind: "bool", name: t.s}, nil
		case "null":
			return &expr{kind: "null"}, nil
		}
		if p.is("(") {
			p.pos++
			var args []*expr
			p.skipNL()
			for !p.is(")") {
				a, err := p.parseExpr(depth + 1)
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if len(args) > maxArray {
					return nil, bad("more than %d arguments", maxArray)
				}
				p.skipNL()
				if p.is(",") {
					p.pos++
					p.skipNL()
				} else if !p.is(")") {
					return nil, bad("expected , or )")
				}
			}
			p.pos++
			return &expr{kind: "call", name: t.s, args: args, depth: depth}, nil
		}
		return &expr{kind: "ref", name: t.s}, nil
	case tPunct:
		switch t.s {
		case "[":
			var items []*expr
			p.skipNL()
			for !p.is("]") {
				a, err := p.parseExpr(depth + 1)
				if err != nil {
					return nil, err
				}
				items = append(items, a)
				if len(items) > maxArray {
					return nil, bad("an array of more than %d", maxArray)
				}
				p.skipNL()
				if p.is(",") {
					p.pos++
					p.skipNL()
				} else if !p.is("]") {
					return nil, bad("expected , or ]")
				}
			}
			p.pos++
			return &expr{kind: "array", args: items}, nil
		case "{":
			e := &expr{kind: "object"}
			p.skipNL()
			for !p.is("}") {
				k := p.next()
				if k.kind != tIdent && k.kind != tString {
					return nil, bad("an object key is not a name")
				}
				p.skipNL()
				if err := p.expect(":"); err != nil {
					return nil, err
				}
				p.skipNL()
				v, err := p.parseExpr(depth + 1)
				if err != nil {
					return nil, err
				}
				e.keys, e.args = append(e.keys, k.s), append(e.args, v)
				if len(e.args) > maxArray {
					return nil, bad("an object of more than %d fields", maxArray)
				}
				p.skipNL()
				if p.is(",") {
					p.pos++
					p.skipNL()
				} else if !p.is("}") {
					return nil, bad("expected , or }")
				}
			}
			p.pos++
			return e, nil
		}
	}
	return nil, bad("unexpected %q", t.s)
}

// Program is a validated block's markup, kept as written, with what the validator learned about it.
type Program struct {
	Markup string
	// Data is true if it uses a component that shows data, so it must cite a source.
	Data bool
}

var (
	reMDImage  = regexp.MustCompile(`!\[`)
	reMDLink   = regexp.MustCompile(`\]\(\s*<?([^)\s>]*)`)
	reAutoLink = regexp.MustCompile(`<((?:[a-zA-Z][a-zA-Z0-9+.-]*):[^>\s]*)>`)
	reBareURL  = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s)>\]"]+`)
	reHTMLTag  = regexp.MustCompile(`(?i)<\s*/?\s*[a-z!?]`)
	reScheme   = regexp.MustCompile(`(?i)\b(?:javascript|data|vbscript|file|blob)\s*:`)
	reURLKey   = regexp.MustCompile(`^(?:url|href|link)$|Url$|URL$`)
	reImageKey = regexp.MustCompile(`^(?:src|image|img|icon[Ss]rc)$|Src$|Image$`)
)

// ValidateMarkup checks one block's OpenUI Lang. sources are the addresses the block cites: every address a reader could
// follow in it (a card's url, a Markdown link, a bare address in text) must be one of them, so the assistant cannot send
// readers somewhere it did not cite.
func ValidateMarkup(markup string, sources []string) (Program, error) {
	if len(markup) == 0 || len(markup) > maxMarkupBytes || !isPlainText(markup) {
		return Program{}, bad("empty, too long or not text")
	}
	toks, err := lex(markup)
	if err != nil {
		return Program{}, err
	}
	p := &parser{toks: toks}
	defs := map[string]*expr{}
	var order []string
	for {
		p.skipNL()
		if p.peek().kind == tEOF {
			break
		}
		name := p.next()
		if name.kind != tIdent || !p.is("=") {
			return Program{}, bad("each statement must be name = value")
		}
		p.pos++
		if _, dup := defs[name.s]; dup {
			return Program{}, bad("%s is defined twice", name.s)
		}
		if len(defs) >= maxStatements {
			return Program{}, bad("more than %d statements", maxStatements)
		}
		e, err := p.parseExpr(0)
		if err != nil {
			return Program{}, err
		}
		if t := p.peek(); t.kind != tNL && t.kind != tEOF {
			return Program{}, bad("something follows the end of a statement")
		}
		defs[name.s] = e
		order = append(order, name.s)
	}
	root := defs["root"]
	if root == nil || root.kind != "call" || root.name != "Card" {
		return Program{}, bad("root must be a Card")
	}
	allowed := map[string]bool{}
	for _, s := range sources {
		allowed[s] = true
	}
	prog := Program{Markup: markup}
	for _, name := range order {
		if err := checkExpr(defs[name], defs, allowed, &prog); err != nil {
			return Program{}, err
		}
	}
	if err := noCycles(defs); err != nil {
		return Program{}, err
	}
	return prog, nil
}

func isPlainText(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return false // control characters, line separators and bidirectional overrides
		}
	}
	return true
}

func checkExpr(e *expr, defs map[string]*expr, sources map[string]bool, prog *Program) error {
	switch e.kind {
	case "call":
		if !readerComponents[e.name] {
			return bad("%s is not a component a published block may use", e.name)
		}
		if dataComponents[e.name] {
			prog.Data = true
		}
		for _, a := range e.args {
			if err := checkExpr(a, defs, sources, prog); err != nil {
				return err
			}
		}
	case "array":
		for _, a := range e.args {
			if err := checkExpr(a, defs, sources, prog); err != nil {
				return err
			}
		}
	case "object":
		for i, a := range e.args {
			k := e.keys[i]
			switch {
			case reImageKey.MatchString(k):
				return bad("an image field (%s)", k)
			case reURLKey.MatchString(k):
				if a.kind != "string" || !sources[a.name] || !PublicHTTPS(a.name) {
					return bad("the address in %s is not one of the block's sources", k)
				}
			}
			if err := checkExpr(a, defs, sources, prog); err != nil {
				return err
			}
		}
	case "string":
		return checkText(e.name, sources)
	case "ref":
		switch e.name {
		case "Query", "Mutation", "Action", "State":
			return bad("%s is not allowed", e.name)
		}
		if defs[e.name] == nil {
			return bad("%s is used but never defined", e.name)
		}
	}
	return nil
}

// checkText refuses raw HTML, images, script-like schemes and any address that is not one of the block's sources.
func checkText(s string, sources map[string]bool) error {
	if reHTMLTag.MatchString(s) {
		return bad("raw HTML in text")
	}
	if reMDImage.MatchString(s) {
		return bad("an image in text")
	}
	if reScheme.MatchString(s) {
		return bad("a script-like address in text")
	}
	var urls []string
	for _, m := range reMDLink.FindAllStringSubmatch(s, -1) {
		urls = append(urls, m[1])
	}
	for _, m := range reAutoLink.FindAllStringSubmatch(s, -1) {
		urls = append(urls, m[1])
	}
	urls = append(urls, reBareURL.FindAllString(s, -1)...)
	for _, u := range urls {
		u = strings.TrimRight(u, ".,;:!?")
		if !sources[u] || !PublicHTTPS(u) {
			return bad("a link in the text is not one of the block's sources")
		}
	}
	return nil
}

func noCycles(defs map[string]*expr) error {
	const (
		white = iota
		grey
		black
	)
	state := map[string]int{}
	var visit func(name string) error
	var walk func(e *expr) error
	walk = func(e *expr) error {
		if e.kind == "ref" {
			return visit(e.name)
		}
		for _, a := range e.args {
			if err := walk(a); err != nil {
				return err
			}
		}
		return nil
	}
	visit = func(name string) error {
		switch state[name] {
		case grey:
			return bad("%s refers to itself", name)
		case black:
			return nil
		}
		state[name] = grey
		if err := walk(defs[name]); err != nil {
			return err
		}
		state[name] = black
		return nil
	}
	for name := range defs {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
