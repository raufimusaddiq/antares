// Package parity checks docs/FEATURE_PARITY_AIXELL.md against docs/PRD.md (issue #112): every requirement row of PRD
// §§21, 26.1, 27, 28 and 29 appears exactly once, with an allowed status and evidence that exists.
package parity

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const root = "../.."

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the PRD text from the heading "## n." up to the next "## ".
func section(prd, n string) string {
	i := strings.Index(prd, "\n## "+n+".")
	if i < 0 {
		return ""
	}
	rest := prd[i+1:]
	if j := strings.Index(rest[3:], "\n## "); j >= 0 {
		return rest[:j+3]
	}
	return rest
}

var idRE = regexp.MustCompile("`((?:UXP|LLM-ACC|LLM|CTX|MEDIA-EXT|QUEUE|KNOW|IMPORT)-[0-9]{3})`")

// required lists the row ids the PRD defines in the covered sections.
func required(t *testing.T) []string {
	prd := read(t, "docs/PRD.md")
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, n := range []string{"21", "27", "28", "29"} {
		s := section(prd, n)
		if s == "" {
			t.Fatalf("PRD §%s not found", n)
		}
		for _, line := range strings.Split(s, "\n") {
			// Definitions only: a table row or a list item that starts with the id.
			if m := regexp.MustCompile("^(?:\\| |- )`([A-Z-]+-[0-9]{3})`").FindStringSubmatch(line); m != nil && idRE.MatchString("`"+m[1]+"`") {
				add(m[1])
			}
		}
	}
	s26 := section(prd, "26")
	inTable := false
	for _, line := range strings.Split(s26, "\n") {
		if strings.HasPrefix(line, "| Domain |") {
			inTable = true
			continue
		}
		if inTable {
			if !strings.HasPrefix(line, "|") {
				break
			}
			cells := strings.Split(line, "|")
			if d := strings.TrimSpace(cells[1]); d != "" && !strings.HasPrefix(d, "---") {
				add("§26/" + d)
			}
		}
	}
	return ids
}

type row struct{ id, status, evidence, source string }

// rows reads the table's rows by header name, so adding or moving a column cannot silently drop them.
func rows(t *testing.T) []row {
	var out []row
	var col map[string]int
	for _, line := range strings.Split(read(t, "docs/FEATURE_PARITY_AIXELL.md"), "\n") {
		if !strings.HasPrefix(line, "|") {
			col = nil
			continue
		}
		cells := strings.Split(line, "|")
		cells = cells[1 : len(cells)-1]
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) > 0 && cells[0] == "ID" {
			col = map[string]int{}
			for i, h := range cells {
				col[h] = i
			}
			for _, h := range []string{"ID", "Source module", "Owner", "Status", "Evidence"} {
				if _, ok := col[h]; !ok {
					t.Fatalf("a requirements table has no %q column", h)
				}
			}
			continue
		}
		if col == nil || strings.HasPrefix(cells[0], "---") {
			continue
		}
		if len(cells) != len(col) {
			t.Fatalf("row %q has %d cells, its table has %d columns", cells[0], len(cells), len(col))
		}
		if cells[col["Owner"]] == "" {
			t.Errorf("%s has no owner", cells[0])
		}
		out = append(out, row{id: cells[0], status: cells[col["Status"]], evidence: cells[col["Evidence"]], source: cells[col["Source module"]]})
	}
	return out
}

// existing collects the Go test names and web test files in the repository.
func existing(t *testing.T) (gotests map[string]bool, webtests map[string]bool) {
	gotests, webtests = map[string]bool{}, map[string]bool{}
	testRE := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case strings.HasSuffix(p, "_test.go"):
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, m := range testRE.FindAllStringSubmatch(string(b), -1) {
				gotests[m[1]] = true
			}
		case strings.Contains(p, ".test.ts"):
			webtests[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return gotests, webtests
}

var (
	goRef  = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
	webRef = regexp.MustCompile("`(web/src/[A-Za-z0-9_/.]+\\.test\\.tsx?)`")
)

func TestTableIsComplete(t *testing.T) {
	want := required(t)
	if len(want) < 80 {
		t.Fatalf("only %d PRD rows found: the PRD headings or tables moved", len(want))
	}
	got := map[string]int{}
	for _, r := range rows(t) {
		got[r.id]++
	}
	for _, id := range want {
		if got[id] != 1 {
			t.Errorf("%s appears %d times in the table, want exactly once", id, got[id])
		}
	}
	wanted := map[string]bool{}
	for _, id := range want {
		wanted[id] = true
	}
	for id := range got {
		if !wanted[id] {
			t.Errorf("%s is in the table but is not a requirement row of PRD §§21, 26.1, 27-29", id)
		}
	}
}

func TestEveryRowHasItsEvidence(t *testing.T) {
	gotests, webtests := existing(t)
	for _, r := range rows(t) {
		var named int
		for _, m := range goRef.FindAllStringSubmatch(r.evidence, -1) {
			if !gotests[m[1]] {
				t.Errorf("%s names %s, which is not a test in this repository", r.id, m[1])
			}
			named++
		}
		for _, m := range webRef.FindAllStringSubmatch(r.evidence, -1) {
			if !webtests[m[1]] {
				t.Errorf("%s names %s, which does not exist", r.id, m[1])
			}
			named++
		}
		for _, m := range regexp.MustCompile("`([a-z][A-Za-z0-9_./*-]+)`").FindAllStringSubmatch(r.source, -1) {
			if hits, _ := filepath.Glob(filepath.Join(root, m[1])); len(hits) == 0 {
				t.Errorf("%s: source module %s does not exist", r.id, m[1])
			}
		}
		switch r.status {
		case "TESTED", "IMPLEMENTED":
			if named == 0 {
				t.Errorf("%s is %s but names no test", r.id, r.status)
			}
		case "UPSTREAM_BLOCKED":
			if !strings.Contains(r.evidence, "`/") || !strings.Contains(r.evidence, "hermes-route-probe.json") {
				t.Errorf("%s is UPSTREAM_BLOCKED but does not name the probed endpoint and cite the route probe", r.id)
			}
		case "OWNER_ACCEPTED_EXCEPTION":
			if len(strings.TrimSpace(r.evidence)) < 20 {
				t.Errorf("%s is an exception with no recorded reason", r.id)
			}
		default:
			t.Errorf("%s has status %q, not one of TESTED, IMPLEMENTED, UPSTREAM_BLOCKED, OWNER_ACCEPTED_EXCEPTION", r.id, r.status)
		}
	}
}

// The table is stamped with the Hermes version it was verified against; the committed route probe must be of the same
// version, and must contain none of the route families the blocked rows say are absent. After a Hermes upgrade, re-run
// scripts/hermes-route-probe.sh: a new memory, speech or quota route then fails here until the rows are revisited.
func TestTableMatchesTheRouteProbe(t *testing.T) {
	var probe struct {
		Version string `json:"hermes_version"`
		Routes  []struct{ Method, Path string }
	}
	if err := json.Unmarshal([]byte(read(t, "docs/hermes/fixtures/hermes-route-probe.json")), &probe); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Verified against Hermes ([0-9.]+)\.`).FindStringSubmatch(read(t, "docs/FEATURE_PARITY_AIXELL.md"))
	if m == nil || m[1] != probe.Version {
		t.Fatalf("table stamp %v, route probe version %q: re-verify the table against the deployed Hermes", m, probe.Version)
	}
	if len(probe.Routes) < 20 {
		t.Fatalf("the route probe lists %d routes: it did not read the gateway", len(probe.Routes))
	}
	absent := regexp.MustCompile(`(?i)memory|/notes|/facts|transcri|speech|/stt|/tts|/audio|quota|balance`)
	for _, r := range probe.Routes {
		if absent.MatchString(r.Path) {
			t.Errorf("Hermes now has %s %s: revisit the UPSTREAM_BLOCKED rows that say it does not", r.Method, r.Path)
		}
	}
}
