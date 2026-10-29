package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The policy script itself, run by sh with a fake hermes on PATH that records its arguments: the toolset policy is
// always written; with the shared plugin installed it is also copied into the profile and selected.
func runPolicy(t *testing.T, withPlugin bool) (calls []string, profiles string) {
	t.Helper()
	dir := t.TempDir()
	bin, shared := filepath.Join(dir, "bin"), filepath.Join(dir, "antares-plugins")
	profiles = filepath.Join(dir, "profiles")
	log := filepath.Join(dir, "calls")
	for _, d := range []string{bin, profiles} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "hermes"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// python3 is the reload: record the arguments after the script (the root and the profile home), and fail like a
	// gateway that does not answer, which must not fail the policy step.
	pyFake := "#!/bin/sh\nshift 2\nprintf 'reload %s\\n' \"$*\" >> " + log + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(pyFake), 0o755); err != nil {
		t.Fatal(err)
	}
	if withPlugin {
		if err := os.MkdirAll(filepath.Join(shared, "kenari-image"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shared, "kenari-image", "plugin.yaml"), []byte("name: kenari-image\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", policyScript, "sh", "u_abcdefgh", `["terminal"]`, shared, profiles, "/root-of-hermes", reloadScript, "web memory clarify")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("policy script: %v %s", err, out)
	}
	raw, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(raw)), "\n"), profiles
}

func TestPolicyScriptInstallsAndSelectsTheImagePlugin(t *testing.T) {
	calls, profiles := runPolicy(t, true)
	want := []string{
		`-p u_abcdefgh config set agent.disabled_toolsets ["terminal"]`,
		`-p u_abcdefgh tools enable web memory clarify --platform api_server`,
		`-p u_abcdefgh config set plugins.enabled ["kenari-image"]`,
		`-p u_abcdefgh config set image_gen.provider kenari-image`,
		"reload /root-of-hermes " + profiles + "/u_abcdefgh",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("hermes calls:\n%s", strings.Join(calls, "\n"))
	}
	if _, err := os.Stat(filepath.Join(profiles, "u_abcdefgh", "plugins", "kenari-image", "plugin.yaml")); err != nil {
		t.Fatalf("plugin not copied into the profile: %v", err)
	}
}

// Without the plugin only the toolsets are written: the disabled list, and the allowed ones enabled for api_server
// (Hermes' default API toolset leaves clarify, tts, stt, video, video_gen and x_search out).
func TestPolicyScriptWithoutThePluginWritesOnlyTheToolsets(t *testing.T) {
	calls, profiles := runPolicy(t, false)
	if len(calls) != 2 || !strings.Contains(calls[0], "agent.disabled_toolsets") || calls[1] != "-p u_abcdefgh tools enable web memory clarify --platform api_server" {
		t.Fatalf("hermes calls: %v", calls)
	}
	if _, err := os.Stat(filepath.Join(profiles, "u_abcdefgh", "plugins")); !os.IsNotExist(err) {
		t.Fatalf("a plugins dir was made without a plugin: %v", err)
	}
}
