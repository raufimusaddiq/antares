package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path"
	"strings"
	"time"
)

// Runner executes a command inside the hermes container. args are argv
// elements, never a shell string. stdin may carry a secret so it stays out of
// the process table.
type Runner interface {
	Run(ctx context.Context, stdin string, args ...string) (stdout, stderr string, err error)
}

type dockerRunner struct{ container string }

func (d dockerRunner) Run(ctx context.Context, stdin string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	full := append([]string{"exec", "-i", "-u", "hermes", d.container}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

const profilesDir = "/opt/data/profiles"

// callTimeout bounds one docker exec. A request runs at most maxCalls of them
// in sequence (/deprovision: delete, purge-identity, test, rmdir), so
// opBudget, the whole-request deadline, must exceed maxCalls*callTimeout and
// the HTTP WriteTimeout must exceed opBudget.
const (
	callTimeout = 60 * time.Second
	maxCalls    = 5
	opBudget    = 330 * time.Second
	writeSlack  = 30 * time.Second
)

// Bootstrap credentials travel only on stdin; an existing profile's inference settings are never rewritten.
const envScript = `
import json, os, stat, sys, tempfile
from pathlib import Path
from agent.secret_scope import load_env_file
mode, path = sys.argv[1], sys.argv[2]
try:
    payload = json.load(sys.stdin)
    key, bootstrap = payload["key"], payload.get("bootstrap")
    parent = os.path.dirname(path)
    if os.path.islink(parent) or (os.path.lexists(path) and not stat.S_ISREG(os.lstat(path).st_mode)):
        sys.exit(5)
    lines = open(path).read().splitlines() if os.path.exists(path) else []
    entries = load_env_file(Path(path))
    has = "API_SERVER_KEY" in entries
    if mode == "init" and has:
        sys.exit(3)
    if mode == "rotate" and not has:
        sys.exit(4)
    if mode == "init":
        from hermes_cli.config import read_user_config_raw
        config = read_user_config_raw(Path(parent) / "config.yaml")
        model = config.get("model", {})
        if not isinstance(model, dict):
            sys.exit(5)
        # Route the way the runtime does: a named provider takes its endpoint and
        # key variable from its providers.<name> / custom_providers entry, never
        # from the model block; only openai and an unnamed custom use model.*.
        provider = str(model.get("provider") or "").strip().lower()
        route = model
        if provider != "openai":
            from hermes_cli.runtime_provider_custom import _get_named_custom_provider
            route = _get_named_custom_provider(provider, config=config, resolve_credentials=False)
            if route is None and provider == "custom":
                route = model
            elif route is None or route.get("key_cmd"):
                sys.exit(5)
        endpoint = str(route.get("base_url") or "").rstrip("/")
        if endpoint in ("${OPENAI_BASE_URL}", "${env:OPENAI_BASE_URL}"):
            endpoint = (bootstrap["base_url"] if bootstrap is not None and not entries else entries.get("OPENAI_BASE_URL", "")).rstrip("/")
        # A profile whose parsed .env holds no variable at all (absent, or only the
        # comment header profile create seeds) has no inference binding yet: a new
        # profile, or the retry of a bootstrap that failed after create.
        if bootstrap is not None and not entries:
            if not endpoint or endpoint != bootstrap["base_url"].rstrip("/"):
                sys.exit(5)
            if route.get("key_env") not in (None, "", "OPENAI_API_KEY") or route.get("api_key_env") not in (None, "", "OPENAI_API_KEY"):
                sys.exit(5)
            configured_key = route.get("api_key") or route.get("api")
            if configured_key and configured_key not in (bootstrap["key"], "${OPENAI_API_KEY}", "${env:OPENAI_API_KEY}"):
                sys.exit(5)
            if key == bootstrap["key"]:
                sys.exit(5)
            lines += ["OPENAI_API_KEY=" + json.dumps(bootstrap["key"]), "OPENAI_BASE_URL=" + json.dumps(bootstrap["base_url"], ensure_ascii=False)]
        else:
            if not entries.get("OPENAI_API_KEY") or not endpoint or endpoint != entries.get("OPENAI_BASE_URL", "").rstrip("/"):
                sys.exit(5)
    lines = [line for line in lines if line.removeprefix("export ").split("=", 1)[0].strip() != "API_SERVER_KEY"]
    lines.append("API_SERVER_KEY=" + key)
    fd, tmp = tempfile.mkstemp(prefix=".antares-env-", dir=parent)
    try:
        with os.fdopen(fd, "w") as f:
            f.write("\n".join(lines) + "\n")
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)
except Exception:
    sys.exit(5)
`

var (
	errKeyExists = errors.New("profile already has a key; if the key was never received, call /rotate")
	errNoProfile = errors.New("profile does not exist")
	errNoKey     = errors.New("profile has no key; use /provision")
	errUpstream  = errors.New("hermes command failed")
)

type hermes struct{ r Runner }

// liveProfiles runs one `hermes profile list` and returns the profile names it
// reports. Each call loads the whole agent CLI, so callers that need several
// answers must reuse the result.
func (h hermes) liveProfiles(ctx context.Context) (map[string]bool, error) {
	out, stderr, err := h.r.Run(ctx, "", "hermes", "profile", "list")
	if err != nil {
		return nil, fmt.Errorf("%w: profile list: %s", errUpstream, brief(stderr))
	}
	live, sawDefault := map[string]bool{}, false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "◆"))
		if len(f) == 0 {
			continue
		}
		if f[0] == "default" {
			sawDefault = true
		}
		live[f[0]] = true
	}
	if !sawDefault {
		// default always exists; not seeing it means the output format changed
		// and "absent" would be a guess.
		return nil, fmt.Errorf("%w: profile list output not recognised", errUpstream)
	}
	return live, nil
}

func (h hermes) exists(ctx context.Context, slug string) (bool, error) {
	live, err := h.liveProfiles(ctx)
	return live[slug], err
}

func (h hermes) create(ctx context.Context, slug string) error {
	_, stderr, err := h.r.Run(ctx, "", "hermes", "profile", "create", slug, "--no-alias")
	if err != nil {
		return fmt.Errorf("%w: profile create: %s", errUpstream, brief(stderr))
	}
	return nil
}

// policyScript writes the tenant toolset policy into the profile's own config.yaml (never default's), and gives the
// profile the Kenari image backend: the plugin is copied from the shared copy the deploy installs (when present) and
// selected as its image_gen provider. One docker exec (three hermes CLI calls, about 6 s each measured on the deployed
// host, within callTimeout); slug, list and directories arrive as arguments ($1-$4), never in the script.
// plugins.enabled is replaced, not merged: a tenant profile is provisioner-owned and holds no other plugin.
const policyScript = `set -e
hermes -p "$1" config set ` + toolsetsConfigKey + ` "$2"
if [ -d "$3/kenari-image" ]; then
  mkdir -p "$4/$1/plugins/kenari-image"
  cp -r "$3/kenari-image/." "$4/$1/plugins/kenari-image/"
  hermes -p "$1" config set plugins.enabled '["kenari-image"]'
  hermes -p "$1" config set image_gen.provider kenari-image
fi`

// sharedPlugins is where the deploy installs the Hermes plugins Antares relies on (deploy/hermes-plugins).
const sharedPlugins = "/opt/data/antares-plugins"

// applyPolicy runs policyScript for one profile.
func (h hermes) applyPolicy(ctx context.Context, slug string, disabled []string) error {
	_, stderr, err := h.r.Run(ctx, "", "sh", "-c", policyScript, "sh", slug, toolsetsValue(disabled), sharedPlugins, profilesDir)
	if err != nil {
		return fmt.Errorf("%w: applying the profile policy: %s", errUpstream, brief(stderr))
	}
	return nil
}

func (h hermes) writeKey(ctx context.Context, mode, slug, key string, bootstrap *inferenceBootstrap) error {
	path := profilesDir + "/" + slug + "/.env"
	payload, err := json.Marshal(struct {
		Key       string              `json:"key"`
		Bootstrap *inferenceBootstrap `json:"bootstrap,omitempty"`
	}{key, bootstrap})
	if err != nil {
		return errInferenceBootstrap
	}
	_, _, err = h.r.Run(ctx, string(payload), "python3", "-c", envScript, mode, path)
	var ee interface{ ExitCode() int } // *exec.ExitError satisfies this
	if errors.As(err, &ee) {
		switch ee.ExitCode() {
		case 3:
			return errKeyExists
		case 4:
			return errNoKey
		case 5:
			return errInferenceBootstrap
		}
	}
	if err != nil {
		// stderr is deliberately dropped: it could echo the key.
		return fmt.Errorf("%w: writing profile env", errUpstream)
	}
	return nil
}

func (h hermes) delete(ctx context.Context, slug string) error {
	// delete may exit 1 while still removing the profile (pending identity
	// settlement); purge-identity is the authoritative step, so its result is
	// what counts.
	_, delErr, derr := h.r.Run(ctx, "", "hermes", "profile", "delete", slug, "--yes")
	_, pErr, perr := h.r.Run(ctx, "", "hermes", "profile", "purge-identity", slug)
	if perr != nil {
		msg := brief(pErr)
		if derr != nil {
			msg += " (delete: " + brief(delErr) + ")"
		}
		return fmt.Errorf("%w: purge-identity: %s", errUpstream, msg)
	}
	return h.removeResidue(ctx, slug)
}

// profileDir returns the profile directory, refusing anything that is not a
// strict child of profilesDir. Callers pass an already validated slug; this is
// a second, independent guard before a path is handed to a removal command.
func profileDir(slug string) (string, error) {
	if err := validateSlug(slug); err != nil {
		return "", err
	}
	dir := path.Join(profilesDir, slug)
	if path.Dir(dir) != profilesDir || dir == profilesDir {
		return "", errBadSlug
	}
	return dir, nil
}

// removeResidue deletes the directory left behind by profile delete. rmdir
// only removes an empty directory, so a directory still in use is reported,
// never deleted.
func (h hermes) removeResidue(ctx context.Context, slug string) error {
	dir, err := profileDir(slug)
	if err != nil {
		return err
	}
	if _, _, err := h.r.Run(ctx, "", "test", "-e", dir); err != nil {
		return nil // already gone
	}
	if _, _, err := h.r.Run(ctx, "", "rmdir", "--", dir); err != nil {
		return fmt.Errorf("%w: %s is not empty or cannot be removed", errUpstream, dir)
	}
	return nil
}

// sweep removes empty leftover directories of deleted u_ profiles. Hermes'
// gateway recreates the empty profile directory about 30s after a delete, when
// it unserves the profile, so removal at /deprovision time alone is not
// enough. Only validated slugs that are not live profiles are touched, and
// rmdir refuses non-empty directories. It returns the directories removed.
func (h hermes) sweep(ctx context.Context) (removed []string, err error) {
	out, stderr, err := h.r.Run(ctx, "", "ls", "-A1", profilesDir)
	if err != nil {
		return nil, fmt.Errorf("%w: listing profiles dir: %s", errUpstream, brief(stderr))
	}
	var candidates []string
	for _, name := range strings.Split(out, "\n") {
		if validateSlug(name) == nil {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	// One profile list per sweep, not one per directory: each costs seconds of
	// CPU, and N of them overran the tick.
	live, err := h.liveProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range candidates {
		if live[name] {
			continue
		}
		if err := h.removeResidue(ctx, name); err != nil {
			log.Printf("sweep: %v", err)
			continue
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func (h hermes) reachable(ctx context.Context) bool {
	_, _, err := h.r.Run(ctx, "", "hermes", "--version")
	return err == nil
}

func brief(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
