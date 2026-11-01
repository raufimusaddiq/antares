package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// One profile's memory, read in the container from that profile only, comes back as two lists.
func TestMemoryReadsOneProfile(t *testing.T) {
	r := &skillsRunner{stdout: `{"user":["Prefers short answers"],"memory":["Project uses Go"]}`}
	rec := do(t, newServer(r, testToken), "/memory", "Bearer "+testToken, `{"slug":"u_existing1"}`)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"memory":["Project uses Go"],"user":["Prefers short answers"]}`+"\n" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if len(r.args) != 9 || r.args[2] != memoryCmd || r.args[4] != hermesPython || r.args[5] != memoryScript || r.args[6] != profilesDir || r.args[7] != "u_existing1" {
		t.Fatalf("command %q", r.args[:min(len(r.args), 5)])
	}
}

func TestMemoryRefusesWhatIsNotAProfilesMemory(t *testing.T) {
	for name, c := range map[string]struct {
		body, out string
		err       error
		want      int
	}{
		"no token":        {`{"slug":"u_existing1"}`, "", nil, http.StatusUnauthorized},
		"bad slug":        {`{"slug":"../etc"}`, "", nil, http.StatusBadRequest},
		"extra field":     {`{"slug":"u_existing1","path":"/etc"}`, "", nil, http.StatusBadRequest},
		"missing profile": {`{"slug":"u_existing1"}`, "", exitErr(3), http.StatusNotFound},
		"refused file":    {`{"slug":"u_existing1"}`, "", exitErr(4), http.StatusBadGateway},
		"not json":        {`{"slug":"u_existing1"}`, `Traceback`, nil, http.StatusBadGateway},
	} {
		auth := "Bearer " + testToken
		if name == "no token" {
			auth = ""
		}
		rec := do(t, newServer(&skillsRunner{stdout: c.out, err: c.err}, testToken), "/memory", auth, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: got %d %s, want %d", name, rec.Code, rec.Body, c.want)
		}
	}
}

// The script itself, run by python3 against temporary profiles (with a stand-in for Hermes' parser): the profile's own
// files are read; a symlinked profile, memories directory or file is refused, so another profile's memory never leaks.
func TestMemoryScriptReadsOnlyTheProfilesOwnFiles(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	code := t.TempDir()
	must(os.MkdirAll(filepath.Join(code, "tools"), 0o755))
	must(os.WriteFile(filepath.Join(code, "tools", "__init__.py"), nil, 0o644))
	must(os.WriteFile(filepath.Join(code, "tools", "memory_tool_store.py"), []byte("class MemoryStore:\n    @staticmethod\n    def _parse_entries(raw):\n        return [e for e in (x.strip() for x in raw.split('\\n§\\n')) if e]\n"), 0o644))
	base := t.TempDir()
	write := func(slug, name, text string) {
		must(os.MkdirAll(filepath.Join(base, slug, "memories"), 0o755))
		must(os.WriteFile(filepath.Join(base, slug, "memories", name), []byte(text), 0o600))
	}
	write("u_victim", "USER.md", "VICTIM-SECRET")
	write("u_own", "USER.md", "Likes tea\n§\nLives in Jakarta\n§\nLikes tea")
	write("u_own", "MEMORY.md", "Uses Go")
	must(os.MkdirAll(filepath.Join(base, "u_empty"), 0o755))
	must(os.Symlink(filepath.Join(base, "u_victim"), filepath.Join(base, "u_linkprofile")))
	must(os.MkdirAll(filepath.Join(base, "u_linkdir"), 0o755))
	must(os.Symlink(filepath.Join(base, "u_victim", "memories"), filepath.Join(base, "u_linkdir", "memories")))
	must(os.MkdirAll(filepath.Join(base, "u_linkfile", "memories"), 0o755))
	must(os.Symlink(filepath.Join(base, "u_victim", "memories", "USER.md"), filepath.Join(base, "u_linkfile", "memories", "USER.md")))

	run := func(slug string) (map[string][]string, int, string) {
		cmd := exec.Command("python3", "-c", memoryScript, base, slug, "65536")
		cmd.Dir = code
		cmd.Env = append(os.Environ(), "PYTHONPATH="+code)
		out, err := cmd.Output()
		exit := 0
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		}
		var d map[string][]string
		_ = json.Unmarshal(out, &d)
		return d, exit, string(out)
	}
	if d, exit, _ := run("u_own"); exit != 0 || strings.Join(d["user"], "|") != "Likes tea|Lives in Jakarta" || strings.Join(d["memory"], "|") != "Uses Go" {
		t.Fatalf("own profile: exit %d, %v", exit, d)
	}
	if d, exit, _ := run("u_empty"); exit != 0 || len(d["user"]) != 0 || len(d["memory"]) != 0 {
		t.Fatalf("profile without memory: exit %d, %v", exit, d)
	}
	if _, err := os.Stat(filepath.Join(base, "u_empty", "memories")); !os.IsNotExist(err) {
		t.Fatal("reading created the memories directory")
	}
	if _, exit, _ := run("u_missing"); exit != 3 {
		t.Fatalf("missing profile: exit %d, want 3", exit)
	}
	for _, slug := range []string{"u_linkprofile", "u_linkdir", "u_linkfile"} {
		if _, exit, out := run(slug); exit == 0 || strings.Contains(out, "VICTIM-SECRET") {
			t.Errorf("%s: exit %d, out %q: a symlink was followed", slug, exit, out)
		}
	}
}
