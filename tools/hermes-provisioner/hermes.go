package main

import (
	"bytes"
	"context"
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
	maxCalls    = 4
	opBudget    = 270 * time.Second
	writeSlack  = 30 * time.Second
)

// envScript sets API_SERVER_KEY in a profile .env. The path is argv[2], the
// mode argv[1]; the key arrives on stdin. mode "init" refuses to overwrite an
// existing key (exit 3), "rotate" replaces it (exit 4 when there is none).
const envScript = `
import os, sys
mode, path = sys.argv[1], sys.argv[2]
key = sys.stdin.read().strip()
lines = []
if os.path.exists(path):
    lines = open(path).read().splitlines()
has = any(l.startswith("API_SERVER_KEY=") for l in lines)
if mode == "init" and has:
    sys.exit(3)
if mode == "rotate" and not has:
    sys.exit(4)
lines = [l for l in lines if not l.startswith("API_SERVER_KEY=")]
lines.append("API_SERVER_KEY=" + key)
tmp = path + ".tmp"
fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as f:
    f.write("\n".join(lines) + "\n")
os.replace(tmp, path)
`

var (
	errKeyExists = errors.New("profile already has a key; if the key was never received, call /rotate")
	errNoProfile = errors.New("profile does not exist")
	errNoKey     = errors.New("profile has no key; use /provision")
	errUpstream  = errors.New("hermes command failed")
)

type hermes struct{ r Runner }

func (h hermes) exists(ctx context.Context, slug string) (bool, error) {
	out, stderr, err := h.r.Run(ctx, "", "hermes", "profile", "list")
	if err != nil {
		return false, fmt.Errorf("%w: profile list: %s", errUpstream, brief(stderr))
	}
	found, sawDefault := false, false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "◆"))
		if len(f) > 0 && f[0] == "default" {
			sawDefault = true
		}
		if len(f) > 0 && f[0] == slug {
			found = true
		}
	}
	if !sawDefault {
		// default always exists; not seeing it means the output format changed
		// and "absent" would be a guess.
		return false, fmt.Errorf("%w: profile list output not recognised", errUpstream)
	}
	return found, nil
}

func (h hermes) create(ctx context.Context, slug string) error {
	_, stderr, err := h.r.Run(ctx, "", "hermes", "profile", "create", slug, "--no-alias")
	if err != nil {
		return fmt.Errorf("%w: profile create: %s", errUpstream, brief(stderr))
	}
	return nil
}

func (h hermes) writeKey(ctx context.Context, mode, slug, key string) error {
	path := profilesDir + "/" + slug + "/.env"
	_, _, err := h.r.Run(ctx, key, "python3", "-c", envScript, mode, path)
	var ee interface{ ExitCode() int } // *exec.ExitError satisfies this
	if errors.As(err, &ee) {
		switch ee.ExitCode() {
		case 3:
			return errKeyExists
		case 4:
			return errNoKey
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
	for _, name := range strings.Split(out, "\n") {
		if validateSlug(name) != nil {
			continue
		}
		live, err := h.exists(ctx, name)
		if err != nil {
			return removed, err
		}
		if live {
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
