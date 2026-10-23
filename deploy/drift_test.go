// Package deploy holds no code; its test keeps the runbook, the systemd unit, the installer and the
// helper's /health contract from drifting apart (#50).
package deploy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func unitValue(t *testing.T, unit, key string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + key + `=(.+)$`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatalf("unit has no %s=", key)
	}
	return strings.TrimSpace(m[1])
}

// TestDeployDocsAndUnitDoNotDrift fails when the documented install sequence, the unit's
// requirements and the health revision field stop agreeing.
func TestDeployDocsAndUnitDoNotDrift(t *testing.T) {
	unit := read(t, "deploy/hermes-provisioner.service")
	readme := read(t, "deploy/README.md")
	runbook := read(t, "docs/runbooks/deployment.md")
	installer := read(t, "deploy/install-provisioner.sh")
	verify := read(t, "deploy/verify-helper-revision.sh")
	helper := read(t, "tools/hermes-provisioner/main.go") + read(t, "tools/hermes-provisioner/buildinfo.go")

	bin := unitValue(t, unit, "ExecStart")
	svcUser := unitValue(t, unit, "User")
	envFile := unitValue(t, unit, "EnvironmentFile")
	optDir := filepath.Dir(filepath.Dir(bin)) // /opt/antares/bin/x -> /opt/antares
	if filepath.Dir(envFile) != optDir {
		t.Errorf("EnvironmentFile %s is not under %s", envFile, optDir)
	}

	// Binary path, service user and mode appear in every document that tells the operator what to do.
	for name, doc := range map[string]string{"deploy/README.md": readme, "runbook": runbook, "installer": installer} {
		if name != "installer" && !strings.Contains(doc, bin) {
			t.Errorf("%s does not mention ExecStart path %s", name, bin)
		}
		if name == "installer" && !strings.Contains(doc, `"$opt/bin/hermes-provisioner"`) {
			t.Errorf("installer does not install to $opt/bin/hermes-provisioner")
		}
	}
	if !strings.Contains(readme, svcUser) {
		t.Errorf("README does not mention service user %s", svcUser)
	}
	if !regexp.MustCompile("`" + regexp.QuoteMeta(optDir) + "`, `" + regexp.QuoteMeta(optDir) + "/bin` \\| `755` \\| `root:root`").MatchString(readme) {
		t.Errorf("README layout table must say %s and %s/bin are 755 root:root", optDir, optDir)
	}
	if !strings.Contains(installer, `opt=${ANTARES_OPT_DIR:-`+optDir+`}`) ||
		!strings.Contains(installer, "install -d -m 0755 -o root -g root") ||
		!strings.Contains(installer, "755 $owner") || !strings.Contains(installer, "600 $owner") {
		t.Errorf("installer does not enforce 755 dirs / 600 env files under %s", optDir)
	}

	// The runbook sequence is build -> stop -> install -> start -> verify, in that order.
	steps := []string{
		"go build -o /tmp/hermes-provisioner .",
		"systemctl stop hermes-provisioner",
		"install -m 0755 -o root -g root /tmp/hermes-provisioner " + bin,
		"systemctl start hermes-provisioner",
		"verify-helper-revision.sh",
	}
	for name, doc := range map[string]string{"deploy/README.md": readme, "runbook": runbook} {
		at := strings.Index(doc, "### Deploying the helper")
		if name == "deploy/README.md" {
			at = strings.Index(doc, "## 3. Provisioner")
		}
		if at < 0 {
			t.Fatalf("%s: helper deploy section not found", name)
		}
		for _, step := range steps {
			i := strings.Index(doc[at:], step)
			if i < 0 {
				t.Errorf("%s lacks step %q after its helper section", name, step)
				continue
			}
			at += i + len(step)
		}
	}

	// The installer must hand its health URL to the verifier, and both default to the unit's address.
	unitAddr := unitValue(t, unit, "Environment=PROVISIONER_ADDR")
	def := "http://" + unitAddr + "/health"
	if !strings.Contains(installer, `"$sha" "$health_url"`) ||
		!strings.Contains(installer, "PROVISIONER_HEALTH_URL:-"+def) ||
		!strings.Contains(verify, "url=${2:-"+def+"}") {
		t.Errorf("installer/verifier health URL is not passed through or no longer matches the unit's %s", unitAddr)
	}
	if !strings.Contains(readme, "PROVISIONER_HEALTH_URL") {
		t.Errorf("README does not document PROVISIONER_HEALTH_URL")
	}

	// /health field name is shared by the helper, the verifier and the docs.
	for _, f := range []string{`"revision"`, `"modified"`} {
		if !strings.Contains(helper, f) {
			t.Errorf("helper does not emit %s", f)
		}
	}
	if !strings.Contains(verify, `"revision"`) || !strings.Contains(verify, `"modified"`) || !strings.Contains(helper, "vcs.revision") {
		t.Errorf("verifier and helper disagree on the revision fields")
	}
	if !strings.Contains(readme, `"revision":"<vcs.revision>"`) {
		t.Errorf("README does not document the /health revision field")
	}
}

func runVerify(t *testing.T, body string, code int, want string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		fmt.Fprintln(w, body)
	}))
	defer srv.Close()
	cmd := exec.Command("sh", "verify-helper-revision.sh", want, srv.URL)
	cmd.Env = append(os.Environ(), "VERIFY_ATTEMPTS=1", "VERIFY_SLEEP=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestVerifyHelperRevision(t *testing.T) {
	const sha = "af2ad227cc64851d961fd2aa90641a3be7a792de"
	const stale = "4bd083529c0e3cabe66c3921f973e45f2c34cbd0"
	cases := []struct {
		name string
		body string
		code int
		ok   bool
	}{
		{"match", `{"status":"ok","revision":"` + sha + `","modified":false}`, 200, true},
		{"stale 200", `{"status":"ok","revision":"` + stale + `","modified":false}`, 200, false},
		{"pre-#50 helper", `{"status":"ok"}`, 200, false},
		{"unavailable", `{"status":"ok","revision":"unavailable","modified":false}`, 200, false},
		{"dirty", `{"status":"ok","revision":"` + sha + `","modified":true}`, 200, false},
		{"right revision, hermes down", `{"status":"hermes unreachable","revision":"` + sha + `","modified":false}`, 503, false},
		{"stale and down", `{"status":"hermes unreachable","revision":"` + stale + `","modified":false}`, 503, false},
	}
	for _, c := range cases {
		out, err := runVerify(t, c.body, c.code, sha)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v want %v\n%s", c.name, err == nil, c.ok, out)
		}
	}
}

func TestInstallerEnforcesDirectoryContract(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Fatal(err)
	}
	check := func(opt string) error {
		cmd := exec.Command("sh", "install-provisioner.sh", "--check-dirs")
		cmd.Env = append(os.Environ(), "ANTARES_OPT_DIR="+opt, "ANTARES_OPT_OWNER="+u.Username+":"+g.Name)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("%s", out)
		}
		return err
	}
	opt := filepath.Join(t.TempDir(), "antares")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(opt, "bin"), 0o755))
	must(os.Chmod(opt, 0o755))
	must(os.Chmod(filepath.Join(opt, "bin"), 0o755))
	must(os.WriteFile(filepath.Join(opt, "provisioner.env"), nil, 0o600))
	if err := check(opt); err != nil {
		t.Fatalf("contract should hold: %v", err)
	}
	must(os.Chmod(opt, 0o700)) // the live-host fault
	if check(opt) == nil {
		t.Fatal("700 /opt/antares must fail the check")
	}
	must(os.Chmod(opt, 0o755))
	must(os.Chmod(filepath.Join(opt, "provisioner.env"), 0o644))
	if check(opt) == nil {
		t.Fatal("a loosened env file must fail the check")
	}
}
