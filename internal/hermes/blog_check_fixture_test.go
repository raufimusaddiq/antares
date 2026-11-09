package hermes

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The blog check is written against a recording of the deployed Hermes (testdata/blog_check_README.md). Nothing else reads
// those files, so this test is what makes a re-recording after an upgrade visible: the facts the design rests on are
// asserted here, and one that stops being true fails loudly instead of rotting.
func TestBlogCheckRecordingShowsWhatTheDesignRestsOn(t *testing.T) {
	f, err := os.Open("testdata/blog_check_events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type row struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	var rows []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("not JSONL: %v", err)
		}
		rows = append(rows, r)
	}
	started, completed, otherTool := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var sawRunStarted, sawRunCompleted bool
	for _, r := range rows {
		switch r.Event {
		case "run.started":
			sawRunStarted = true
		case "tool.started":
			// Only searches matter here: another tool in a later recording (a file read, say) is not what R4 is about.
			if r.Data["label"] != "web_search" {
				otherTool[asString(r.Data["toolCallId"])] = true
				continue
			}
			// The query is visible; that is all a search tells us up front.
			if strings.TrimSpace(asString(r.Data["detail"])) == "" {
				t.Errorf("a web_search no longer shows its query: %v", r.Data)
			}
			started[asString(r.Data["toolCallId"])] = true
		case "tool.completed":
			if otherTool[asString(r.Data["toolCallId"])] {
				continue
			}
			completed[asString(r.Data["toolCallId"])] = true
			// Research R4: what a search returned is NOT in the events. If this ever has text, sources can be cross-checked
			// and the design (every source unverified, every data block acknowledged) can be relaxed. Until then, it must not.
			if asString(r.Data["summary"]) != "" {
				t.Errorf("tool.completed now carries a summary (%q): research R4 is out of date, re-read it before changing the check", r.Data["summary"])
			}
		case "run.completed":
			sawRunCompleted = true
		}
	}
	if !sawRunStarted || !sawRunCompleted || len(started) < 1 {
		t.Fatalf("the recording lost its run or its searches: started=%v completed=%v searches=%d", sawRunStarted, sawRunCompleted, len(started))
	}
	for id := range started {
		if !completed[id] {
			t.Errorf("search %s has no tool.completed", id)
		}
	}
	out, err := os.ReadFile("testdata/blog_check_output.txt")
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		t.Fatalf("the recorded output is missing: %v", err)
	}
}

func asString(v any) string { s, _ := v.(string); return s }
