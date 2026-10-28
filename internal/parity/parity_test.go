// Package parity checks docs/FEATURE_PARITY_AIXELL.md against docs/PRD.md (issue #112): every requirement row of PRD
// §§21, 26.1, 27, 28 and 29 appears exactly once, with an allowed status and evidence that exists.
package parity

import (
	"encoding/json"
	"fmt"
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

// Any requirement id family: a definition row in a covered section counts, whatever its prefix.
var idRE = regexp.MustCompile("`([A-Z]+(?:-[A-Z]+)*-[0-9]{3})`")

// required lists the row ids the PRD defines in the covered sections.
// required lists the requirement rows the PRD defines in the covered sections, with the priority the PRD gives each
// ("" where its definition states none).
func required(t *testing.T) (ids []string, pri map[string]string) {
	prd := read(t, "docs/PRD.md")
	pri = map[string]string{}
	priRE := regexp.MustCompile(`\bP[0-3]\b`)
	add := func(id, line string) {
		if _, ok := pri[id]; !ok {
			ids = append(ids, id)
			pri[id] = priRE.FindString(line)
		}
	}
	for _, n := range []string{"21", "25", "27", "28", "29"} {
		s := section(prd, n)
		if s == "" {
			t.Fatalf("PRD §%s not found", n)
		}
		for _, line := range strings.Split(s, "\n") {
			// Definitions only: a table row or a list item that starts with the id.
			if m := regexp.MustCompile("^(?:\\| |- )`([A-Z-]+-[0-9]{3})`").FindStringSubmatch(line); m != nil && idRE.MatchString("`"+m[1]+"`") {
				add(m[1], line)
			}
		}
	}
	// Domain rows have no id: §26.1 (by domain) and §29.1 (by UI capability).
	for _, tbl := range []struct{ sec, header, prefix string }{{"26", "| Domain |", "§26/"}, {"29", "| UI capability |", "§29.1/"}} {
		in := false
		for _, line := range strings.Split(section(prd, tbl.sec), "\n") {
			if strings.HasPrefix(line, tbl.header) {
				in = true
				continue
			}
			if !in {
				continue
			}
			if !strings.HasPrefix(line, "|") {
				break
			}
			cells := strings.Split(line, "|")
			if d := strings.TrimSpace(cells[1]); d != "" && !strings.HasPrefix(d, "---") {
				p := ""
				if tbl.sec == "26" {
					p = priRE.FindString(cells[len(cells)-2]) // "Priority / integration gate"
				}
				add(tbl.prefix+d, "")
				pri[tbl.prefix+d] = p
			}
		}
	}
	return ids, pri
}

type row struct{ id, pri, status, evidence, source string }

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
			for _, h := range []string{"ID", "Pri", "Source module", "Owner", "Status", "Evidence"} {
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
		out = append(out, row{id: cells[0], pri: cells[col["Pri"]], status: cells[col["Status"]], evidence: cells[col["Evidence"]], source: cells[col["Source module"]]})
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
	want, pri := required(t)
	if len(want) < 100 {
		t.Fatalf("only %d PRD rows found: the PRD headings or tables moved", len(want))
	}
	got := map[string]int{}
	for _, r := range rows(t) {
		got[r.id]++
		// The PRD's priority where it states one, "—" where it states none: no unsourced priority.
		if p, ok := pri[r.id]; ok {
			want := p
			if want == "" {
				want = "—"
			}
			if r.pri != want {
				t.Errorf("%s has priority %q in the table, the PRD gives %q", r.id, r.pri, want)
			}
		}
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
			t.Errorf("%s is in the table but is not a requirement row of PRD §§21, 25, 26.1, 27-29, 29.1", id)
		}
	}
}

func TestEveryRowHasItsEvidence(t *testing.T) {
	gotests, webtests := existing(t)
	prd := read(t, "docs/PRD.md")
	probeRoutes := map[string]bool{}
	var probe struct{ Routes []struct{ Path string } }
	if err := json.Unmarshal([]byte(read(t, "docs/hermes/fixtures/hermes-route-probe.json")), &probe); err != nil {
		t.Fatal(err)
	}
	for _, r := range probe.Routes {
		probeRoutes[r.Path] = true
	}
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
			concrete := false
			for _, m := range regexp.MustCompile("`(/[a-z0-9_/{}.-]+)`").FindAllStringSubmatch(r.evidence, -1) {
				if probeRoutes[m[1]] {
					concrete = true
				}
			}
			if !concrete {
				t.Errorf("%s is UPSTREAM_BLOCKED but names no concrete route from the committed probe (the endpoint that was checked)", r.id)
			}
			if !strings.Contains(r.evidence, "`/") || !strings.Contains(r.evidence, "hermes-route-probe.json") {
				t.Errorf("%s is UPSTREAM_BLOCKED but does not name the probed endpoint and cite the route probe", r.id)
			}
		case "OWNER_ACCEPTED_EXCEPTION":
			// An exception is an owner decision or a divergence the PRD itself states; an upstream gap is not one.
			cites := regexp.MustCompile(`PRD §([0-9]+(?:\.[0-9]+)?)`).FindAllStringSubmatch(r.evidence, -1)
			if len(cites) == 0 && !regexp.MustCompile(`[Oo]wner decision \d{4}-\d{2}-\d{2}`).MatchString(r.evidence) {
				t.Errorf("%s is an owner exception but cites neither a PRD section nor a dated owner decision", r.id)
			}
			for _, c := range cites {
				if !regexp.MustCompile(`(?m)^#{2,3} ` + regexp.QuoteMeta(c[1]) + `[. ]`).MatchString(prd) {
					t.Errorf("%s cites PRD §%s, which docs/PRD.md does not have", r.id, c[1])
				}
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

// LLM-ACC-015 / UXP-ACC-020: what goes into the image (internal/, cmd/) has no Hermes data path or database, no local
// media pipeline, no process spawning, and no LLM provider request. The one provider host it may name is kenari.id, for
// the model catalogue and the admin-only account read (#146), never a completion route. The image is distroless and
// the container mounts only its own data volume.
func TestImageHasNoForbiddenUpstreamAccess(t *testing.T) {
	forbidden := regexp.MustCompile(`/opt/data|memory_store|state\.db|\.hermes/|ffmpeg|"os/exec"|chat/completions|/v1/responses|/v1/messages|api\.openai\.com|anthropic\.com|generativelanguage`)
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if forbidden.MatchString(line) {
					t.Errorf("%s:%d: %s", p, i+1, strings.TrimSpace(line))
				}
				if strings.Contains(line, "kenari.id") && !strings.Contains(p, "internal/kenari") && !strings.HasPrefix(strings.TrimSpace(line), "//") &&
					!strings.Contains(line, "usageSource") {
					t.Errorf("%s:%d: names the provider outside internal/kenari: %s", p, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	froms := regexp.MustCompile(`(?m)^FROM\s+(\S+)`).FindAllStringSubmatch(read(t, "Dockerfile"), -1)
	if len(froms) == 0 || !strings.HasPrefix(froms[len(froms)-1][1], "gcr.io/distroless/static") {
		t.Errorf("the final image stage is %v, not distroless static (no runtime Python or Node)", froms)
	}
	// The service's volumes: block (indented under the service), item by item.
	block := regexp.MustCompile(`(?m)^    volumes:\n((?:      - .*\n)+)`).FindStringSubmatch(read(t, "deploy/compose.yml"))
	if block == nil {
		t.Fatal("no volumes block for the antares service in deploy/compose.yml")
	}
	for _, line := range strings.Split(strings.TrimSpace(block[1]), "\n") {
		if src := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(line), "- "), ":", 2)[0]; src != "antares-data" {
			t.Errorf("the antares container mounts %q; only its own data volume is allowed", line)
		}
	}
}

// The totals are pinned so the table cannot grow, shrink or change status without this line (and the PR that changes
// it) saying so.
func TestTableTotals(t *testing.T) {
	counts := map[string]int{}
	all := rows(t)
	for _, r := range all {
		counts[r.status]++
	}
	want := map[string]int{"TESTED": wantTested, "IMPLEMENTED": wantImplemented, "UPSTREAM_BLOCKED": wantBlocked, "OWNER_ACCEPTED_EXCEPTION": wantException}
	if len(all) != wantRows || fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Errorf("table has %d rows %v; pinned %d rows %v", len(all), counts, wantRows, want)
	}
}

const (
	wantRows        = 121
	wantTested      = 96
	wantImplemented = 6
	wantBlocked     = 18
	wantException   = 1
)
