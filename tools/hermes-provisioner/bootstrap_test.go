package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fixtureInferenceKey = "fixture-inference-credential-132"

type profileRunner struct {
	root, endpoint string
	calls          int
}

func newProfileRunner(t *testing.T, endpoint string) *profileRunner {
	t.Helper()
	root := t.TempDir()
	module := filepath.Join(root, "modules", "hermes_cli")
	if err := os.MkdirAll(module, 0700); err != nil {
		t.Fatal(err)
	}
	// The adapter stands in for Hermes' YAML reader; fixture JSON is valid YAML.
	for name, body := range map[string]string{"__init__.py": "", "config.py": "import json\ndef read_user_config_raw(path):\n    with open(path) as f: return json.load(f)\n"} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &profileRunner{root: root, endpoint: endpoint}
}

func (f *profileRunner) Run(ctx context.Context, stdin string, args ...string) (string, string, error) {
	f.calls++
	for _, arg := range args {
		if strings.Contains(arg, fixtureInferenceKey) {
			return "", "", fmt.Errorf("inference credential reached argv")
		}
	}
	switch {
	case len(args) > 2 && args[0] == "hermes" && args[1] == "profile" && args[2] == "list":
		entries, err := os.ReadDir(f.root)
		if err != nil {
			return "", "", err
		}
		out := "default model running\n"
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "u_") {
				out += entry.Name() + " model stopped\n"
			}
		}
		return out, "", nil
	case len(args) > 3 && args[0] == "hermes" && args[1] == "profile" && args[2] == "create":
		dir := filepath.Join(f.root, args[3])
		if err := os.Mkdir(dir, 0700); err != nil {
			return "", "", err
		}
		data, _ := json.Marshal(map[string]any{"model": map[string]string{"provider": "custom", "default": "initial-model", "base_url": f.endpoint}})
		return "", "", os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600)
	case len(args) == 7 && args[0] == "hermes" && args[1] == "-p" && args[3] == "config" && args[4] == "set":
		p := filepath.Join(f.root, args[2], "config.yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			return "", "", err
		}
		var cfg map[string]any
		if err = json.Unmarshal(data, &cfg); err != nil {
			return "", "", err
		}
		if args[5] == "model.default" {
			cfg["model"].(map[string]any)["default"] = args[6]
		} else {
			var disabled []string
			if err = json.Unmarshal([]byte(args[6]), &disabled); err != nil {
				return "", "", err
			}
			cfg["agent"] = map[string]any{"disabled_toolsets": disabled}
		}
		data, _ = json.Marshal(cfg)
		return "", "", os.WriteFile(p, data, 0600)
	case len(args) >= 5 && args[0] == "python3":
		argv := append([]string(nil), args[1:]...)
		argv[len(argv)-1] = filepath.Join(f.root, filepath.Base(filepath.Dir(args[len(args)-1])), ".env")
		cmd := exec.CommandContext(ctx, "python3", argv...)
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+filepath.Join(f.root, "modules"))
		out, err := cmd.Output()
		return string(out), "fixture stderr must not escape", err
	default:
		return "", "", fmt.Errorf("unexpected fixture command")
	}
}

func readProfileEnv(t *testing.T, r *profileRunner, slug string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.root, slug, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			if strings.HasPrefix(v, "\"") {
				v, err = strconv.Unquote(v)
				if err != nil {
					t.Fatal(err)
				}
			}
			out[k] = v
		}
	}
	return out
}

func TestSharedInferenceBootstrapReachesProvider(t *testing.T) {
	models := make(chan string, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureInferenceKey {
			http.Error(w, "wrong inference credential", 401)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		models <- body.Model
		w.WriteHeader(200)
	}))
	defer provider.Close()
	t.Setenv("PROVISIONER_INFERENCE_API_KEY", fixtureInferenceKey)
	t.Setenv("PROVISIONER_INFERENCE_BASE_URL", provider.URL+"/v1")
	t.Setenv("UNRELATED_SECRET", "must-not-copy-this-secret")
	runner := newProfileRunner(t, provider.URL+"/v1")
	disabled, _ := disabledFor(defaultAllowed)
	s := newServerWithPolicy(runner, testToken, disabled)
	keys := map[string]bool{}
	for i, slug := range []string{"u_testusera132", "u_testuserb132"} {
		rec := do(t, s, "/provision", "Bearer "+testToken, `{"slug":"`+slug+`"}`)
		if rec.Code != 200 {
			t.Fatalf("provision HTTP%d", rec.Code)
		}
		var response map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		env := readProfileEnv(t, runner, slug)
		if env["OPENAI_API_KEY"] != fixtureInferenceKey || env["OPENAI_BASE_URL"] != provider.URL+"/v1" {
			t.Fatal("new profile lacks authorized inference credential/endpoint")
		}
		if env["API_SERVER_KEY"] != response["key"] || env["API_SERVER_KEY"] == fixtureInferenceKey || keys[env["API_SERVER_KEY"]] {
			t.Fatal("inbound credential is missing, shared or reused")
		}
		keys[env["API_SERVER_KEY"]] = true
		if len(env) != 3 || env["UNRELATED_SECRET"] != "" || strings.Contains(rec.Body.String(), fixtureInferenceKey) {
			t.Fatal("bootstrap copied or disclosed an unrelated/provider secret")
		}
		model := fmt.Sprintf("chosen-model-%d", i)
		if _, _, err := runner.Run(context.Background(), "", "hermes", "-p", slug, "config", "set", "model.default", model); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("POST", env["OPENAI_BASE_URL"]+"/chat/completions", strings.NewReader(`{"model":"`+model+`"}`))
		req.Header.Set("Authorization", "Bearer "+env["OPENAI_API_KEY"])
		result, err := provider.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, result.Body)
		result.Body.Close()
		if result.StatusCode != 200 {
			t.Fatal("provider did not authenticate the profile")
		}
		cfgBytes, _ := os.ReadFile(filepath.Join(runner.root, slug, "config.yaml"))
		var cfg map[string]any
		json.Unmarshal(cfgBytes, &cfg)
		if cfg["model"].(map[string]any)["default"] != model {
			t.Fatal("model choice was not independent")
		}
		stat, _ := os.Stat(filepath.Join(runner.root, slug, ".env"))
		if stat.Mode().Perm() != 0600 {
			t.Fatal("profile credential file is not0600")
		}
	}
	for i, slug := range []string{"u_testusera132", "u_testuserb132"} {
		data, err := os.ReadFile(filepath.Join(runner.root, slug, "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg["model"].(map[string]any)["default"] != fmt.Sprintf("chosen-model-%d", i) {
			t.Fatal("changing one profile synchronized another model choice")
		}
	}
	if len(models) != 2 || <-models == <-models {
		t.Fatal("per-profile models collapsed")
	}
}

func TestInferenceSourceRefusedBeforeCreate(t *testing.T) {
	for _, tc := range []struct{ name, key, url string }{
		{"missing-key", "", "https://provider.example.invalid/v1"},
		{"missing-url", fixtureInferenceKey, ""},
		{"newline-key", "bad\ncredential", "https://provider.example.invalid/v1"},
		{"url-userinfo", fixtureInferenceKey, "https://secret@example.invalid/v1"},
		{"url-query", fixtureInferenceKey, "https://provider.example.invalid/v1?token=secret"},
		{"url-fragment", fixtureInferenceKey, "https://provider.example.invalid/v1#secret"},
		{"url-scheme", fixtureInferenceKey, "file:///tmp/provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PROVISIONER_INFERENCE_API_KEY", tc.key)
			t.Setenv("PROVISIONER_INFERENCE_BASE_URL", tc.url)
			r := newProfileRunner(t, "https://provider.example.invalid/v1")
			disabled, _ := disabledFor(defaultAllowed)
			response := do(t, newServerWithPolicy(r, testToken, disabled), "/provision", "Bearer "+testToken, `{"slug":"u_badsource132"}`)
			if response.Code != 502 || r.calls != 1 {
				t.Fatal("invalid source did not fail before profile creation")
			}
			if strings.Contains(response.Body.String(), `"key"`) || strings.Contains(response.Body.String(), fixtureInferenceKey) || strings.Contains(response.Body.String(), "token=secret") {
				t.Fatal("source rejected with secret-bearing response")
			}
			if _, err := os.Stat(filepath.Join(r.root, "u_badsource132")); !os.IsNotExist(err) {
				t.Fatal("invalid source created a profile")
			}
		})
	}
}

func TestBootstrapMismatchAndRetryFailClosed(t *testing.T) {
	for _, endpoint := range []string{"", "https://different.example.invalid/v1", "https://provider.example.invalid/other", "https://provider.example.invalid:9443/v1", "http://provider.example.invalid/v1"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("PROVISIONER_INFERENCE_API_KEY", fixtureInferenceKey)
			t.Setenv("PROVISIONER_INFERENCE_BASE_URL", "https://provider.example.invalid/v1")
			r := newProfileRunner(t, endpoint)
			disabled, _ := disabledFor(defaultAllowed)
			s := newServerWithPolicy(r, testToken, disabled)
			for attempt := 0; attempt < 2; attempt++ {
				response := do(t, s, "/provision", "Bearer "+testToken, `{"slug":"u_mismatch132"}`)
				if response.Code != 502 || strings.Contains(response.Body.String(), `"key"`) || strings.Contains(response.Body.String(), fixtureInferenceKey) {
					t.Fatal("mismatched endpoint or retry issued/disclosed a credential")
				}
				if _, err := os.Stat(filepath.Join(r.root, "u_mismatch132", ".env")); !os.IsNotExist(err) {
					t.Fatal("mismatch wrote profile credentials")
				}
			}
		})
	}
}

func TestNewBootstrapPreservesExistingProfilesAndRotation(t *testing.T) {
	endpoint := "https://provider.example.invalid/v1"
	t.Setenv("PROVISIONER_INFERENCE_API_KEY", fixtureInferenceKey)
	t.Setenv("PROVISIONER_INFERENCE_BASE_URL", endpoint)
	r := newProfileRunner(t, endpoint)
	owner := filepath.Join(r.root, "u_preexist132")
	if err := os.Mkdir(owner, 0700); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{"config.yaml": `{"model":{"default":"owner-choice","provider":"custom","base_url":"https://provider.example.invalid/v1"}}`, ".env": "API_SERVER_KEY=existing-inbound\nOPENAI_API_KEY=existing-private-inference\nOPENAI_BASE_URL=" + endpoint + "\n", "sessions.db": "existing-session-state"}
	for name, value := range before {
		if err := os.WriteFile(filepath.Join(owner, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	defaultEnv := filepath.Join(r.root, ".env")
	if err := os.WriteFile(defaultEnv, []byte("UNRELATED_SECRET=not-authorized-for-copy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	disabled, _ := disabledFor(defaultAllowed)
	s := newServerWithPolicy(r, testToken, disabled)
	response := do(t, s, "/provision", "Bearer "+testToken, `{"slug":"u_newmodel132"}`)
	if response.Code != 200 {
		t.Fatalf("HTTP%d", response.Code)
	}
	for name, value := range before {
		b, _ := os.ReadFile(filepath.Join(owner, name))
		if string(b) != value {
			t.Fatalf("pre-existing file changed: %s", name)
		}
	}
	b, _ := os.ReadFile(defaultEnv)
	if string(b) != "UNRELATED_SECRET=not-authorized-for-copy\n" {
		t.Fatal("launch secrets changed")
	}
	if strings.Contains(response.Body.String(), "existing-private-inference") {
		t.Fatal("an owner's credential was copied")
	}
	envBefore := readProfileEnv(t, r, "u_newmodel132")
	if _, _, err := r.Run(context.Background(), "", "hermes", "-p", "u_newmodel132", "config", "set", "model.default", "my-independent-choice"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROVISIONER_INFERENCE_API_KEY", "different-operator-source-must-not-replace-existing")
	rotate := newServerWithPolicy(r, testToken, disabled)
	response = do(t, rotate, "/rotate", "Bearer "+testToken, `{"slug":"u_newmodel132"}`)
	if response.Code != 200 {
		t.Fatal("rotation failed")
	}
	envAfter := readProfileEnv(t, r, "u_newmodel132")
	if envAfter["OPENAI_API_KEY"] != envBefore["OPENAI_API_KEY"] || envAfter["OPENAI_BASE_URL"] != envBefore["OPENAI_BASE_URL"] || envAfter["API_SERVER_KEY"] == envBefore["API_SERVER_KEY"] {
		t.Fatal("rotation changed inference credentials or failed to rotate inbound key")
	}
	cfgBefore, _ := os.ReadFile(filepath.Join(r.root, "u_newmodel132", "config.yaml"))
	response = do(t, rotate, "/provision", "Bearer "+testToken, `{"slug":"u_newmodel132"}`)
	if response.Code != 409 {
		t.Fatal("existing inbound key was replaced")
	}
	cfgAfter, _ := os.ReadFile(filepath.Join(r.root, "u_newmodel132", "config.yaml"))
	if string(cfgAfter) != string(cfgBefore) {
		t.Fatal("existing model configuration was rewritten")
	}
	envFinal := readProfileEnv(t, r, "u_newmodel132")
	if envFinal["OPENAI_API_KEY"] != fixtureInferenceKey {
		t.Fatal("existing inference key was overwritten by provisioning")
	}
}

func TestKeylessRecoveryRequiresExistingInferenceBinding(t *testing.T) {
	endpoint := "https://provider.example.invalid/v1"
	t.Setenv("PROVISIONER_INFERENCE_API_KEY", fixtureInferenceKey)
	t.Setenv("PROVISIONER_INFERENCE_BASE_URL", endpoint)
	r := newProfileRunner(t, endpoint)
	disabled, _ := disabledFor(defaultAllowed)
	s := newServerWithPolicy(r, testToken, disabled)
	if _, _, err := r.Run(context.Background(), "", "hermes", "profile", "create", "u_keyless132", "--no-alias"); err != nil {
		t.Fatal(err)
	}
	existing := "OPENAI_API_KEY=pre-existing-authorized-key\nOPENAI_BASE_URL=" + endpoint + "\nUNRELATED_SECRET=retained-not-copied\n"
	if err := os.WriteFile(filepath.Join(r.root, "u_keyless132", ".env"), []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	response := do(t, s, "/provision", "Bearer "+testToken, `{"slug":"u_keyless132"}`)
	if response.Code != 200 {
		t.Fatal("existing keyless profile could not recover its inbound key")
	}
	env := readProfileEnv(t, r, "u_keyless132")
	if env["OPENAI_API_KEY"] != "pre-existing-authorized-key" || env["UNRELATED_SECRET"] != "retained-not-copied" {
		t.Fatal("recovery rewrote existing secrets")
	}
}

type leakingEnvRunner struct{ *profileRunner }

func (f leakingEnvRunner) Run(ctx context.Context, stdin string, args ...string) (string, string, error) {
	if args[0] == "python3" {
		return fixtureInferenceKey, fixtureInferenceKey, exitErr(1)
	}
	return f.profileRunner.Run(ctx, stdin, args...)
}

func TestBootstrapWriteFailureDoesNotDiscloseSecrets(t *testing.T) {
	t.Setenv("PROVISIONER_INFERENCE_API_KEY", fixtureInferenceKey)
	t.Setenv("PROVISIONER_INFERENCE_BASE_URL", "https://provider.example.invalid/v1")
	r := leakingEnvRunner{newProfileRunner(t, "https://provider.example.invalid/v1")}
	disabled, _ := disabledFor(defaultAllowed)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	response := do(t, newServerWithPolicy(r, testToken, disabled), "/provision", "Bearer "+testToken, `{"slug":"u_failure132"}`)
	if response.Code != 502 || strings.Contains(response.Body.String(), fixtureInferenceKey) || strings.Contains(logs.String(), fixtureInferenceKey) {
		t.Fatal("write failure disclosed provider credential or succeeded")
	}
}

func TestBootstrapEndpointReferencesAndQuotedCredential(t *testing.T) {
	endpoint := "https://provider.example.invalid/v1"
	key := `fixture-quote"hash#backslash\equals=132`
	t.Setenv("PROVISIONER_INFERENCE_API_KEY", key)
	t.Setenv("PROVISIONER_INFERENCE_BASE_URL", endpoint+"/")
	r := newProfileRunner(t, "${OPENAI_BASE_URL}")
	disabled, _ := disabledFor(defaultAllowed)
	response := do(t, newServerWithPolicy(r, testToken, disabled), "/provision", "Bearer "+testToken, `{"slug":"u_envrefs132"}`)
	if response.Code != 200 {
		t.Fatalf("HTTP%d", response.Code)
	}
	env := readProfileEnv(t, r, "u_envrefs132")
	if env["OPENAI_API_KEY"] != key || env["OPENAI_BASE_URL"] != endpoint+"/" {
		t.Fatal("quoted credential or declared endpoint was changed")
	}
	data, err := os.ReadFile(filepath.Join(r.root, "u_envrefs132", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"].(map[string]any)["base_url"] != "${OPENAI_BASE_URL}" {
		t.Fatal("bootstrap changed seeded model configuration")
	}
}

func TestBootstrapRefusesSymlinkAndConflictingSeededCredential(t *testing.T) {
	endpoint := "https://provider.example.invalid/v1"
	for _, name := range []string{"symlink", "conflicting-credential", "wrong-key-env"} {
		t.Run(name, func(t *testing.T) {
			r := newProfileRunner(t, endpoint)
			if _, _, err := r.Run(context.Background(), "", "hermes", "profile", "create", "u_guarded132", "--no-alias"); err != nil {
				t.Fatal(err)
			}
			envPath := filepath.Join(r.root, "u_guarded132", ".env")
			target := filepath.Join(r.root, "untouched.env")
			if err := os.WriteFile(target, []byte("preserved-owner-secret"), 0600); err != nil {
				t.Fatal(err)
			}
			if name == "symlink" {
				if err := os.Symlink(target, envPath); err != nil {
					t.Fatal(err)
				}
			} else {
				model := map[string]string{"default": "independent-choice", "provider": "custom", "base_url": endpoint}
				if name == "conflicting-credential" {
					model["api_key"] = "not-the-authorized-bootstrap-source"
				} else {
					model["key_env"] = "OTHER_PROVIDER_KEY"
				}
				data, _ := json.Marshal(map[string]any{"model": model})
				if err := os.WriteFile(filepath.Join(r.root, "u_guarded132", "config.yaml"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := (hermes{r}).writeKey(context.Background(), "init", "u_guarded132", "new-inbound-key", &inferenceBootstrap{Key: fixtureInferenceKey, BaseURL: endpoint})
			if err != errInferenceBootstrap {
				t.Fatal("unsafe destination/credential mapping was not refused")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "preserved-owner-secret" {
				t.Fatal("pre-existing secret was modified")
			}
			if name != "symlink" {
				if _, err := os.Stat(envPath); !os.IsNotExist(err) {
					t.Fatal("conflicting seed wrote credentials")
				}
			}
		})
	}
}
