// Command antares-smoke is the deploy gate (issue #53, docs/runbooks/smoke-test.md).
// It signs in with real sessions, makes the tenant prove itself, chats, and checks
// the negative cases, then reports which step failed.
//
//	ANTARES_SMOKE_COOKIE_ADMIN=… ANTARES_SMOKE_COOKIE_A=… ANTARES_SMOKE_COOKIE_B=… \
//	ANTARES_SMOKE_COOKIE_PENDING=… antares-smoke --url https://antares.example.com --json result.json
//
// Exit status: 0 every step passed; 1 a step failed; 2 nothing failed but a step
// did not run (not a pass); 3 usage or configuration error.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/raufimusaddiq/antares/internal/smoke"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

const usageExit = 3

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("antares-smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	target := fs.String("url", getenv("ANTARES_SMOKE_URL"), "base URL of the deployed Antares (env ANTARES_SMOKE_URL)")
	origin := fs.String("origin", getenv("ANTARES_SMOKE_ORIGIN"), "Origin header for mutations; must equal ANTARES_PUBLIC_ORIGIN (default: --url)")
	jsonOut := fs.String("json", "", "write the machine-readable result here; '-' for stdout (human output then goes to stderr)")
	runTO := fs.Duration("run-timeout", 2*time.Minute, "bound on a chat: message sent → terminal phase")
	tenantTO := fs.Duration("tenant-timeout", 5*time.Minute, "bound on a tenant reaching READY, and on a re-verification")
	total := fs.Duration("timeout", 20*time.Minute, "bound on the whole run")
	prompt := fs.String("prompt", "", "message to send (default: a short fixed sentence)")
	allowHTTP := fs.Bool("allow-http", false, "permit a non-loopback http:// target (session cookies then cross the network in clear)")
	if err := fs.Parse(args); err != nil {
		return usageExit
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "antares-smoke: "+format+"\n", a...)
		return usageExit
	}
	if *target == "" {
		return fail("--url (or ANTARES_SMOKE_URL) is required")
	}
	u, err := url.Parse(*target)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" {
		return fail("--url must be a bare http(s) origin, e.g. https://antares.example.com")
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) && !*allowHTTP {
		return fail("refusing http:// to %s: session cookies would cross the network in clear (use https, or --allow-http)", u.Host)
	}

	red := &smoke.Redactor{}
	actors := map[string]*smoke.Actor{}
	supplied := 0
	for _, slot := range []struct{ key, env, name string }{
		{"admin", "ANTARES_SMOKE_COOKIE_ADMIN", "admin"}, {"a", "ANTARES_SMOKE_COOKIE_A", "A"},
		{"b", "ANTARES_SMOKE_COOKIE_B", "B"}, {"pending", "ANTARES_SMOKE_COOKIE_PENDING", "pending"},
	} {
		v := strings.TrimSpace(getenv(slot.env))
		// Accept the value pasted either bare or as "name=value" from a browser's cookie view.
		v = strings.TrimPrefix(v, smoke.SessionCookie+"=")
		if v == "" {
			actors[slot.key] = nil
			continue
		}
		red.Add(v)
		supplied++
		actors[slot.key] = &smoke.Actor{Name: slot.name, Session: &smoke.Session{Name: slot.name, Cookie: v}}
	}
	if supplied == 0 {
		return fail("no sessions supplied: set ANTARES_SMOKE_COOKIE_ADMIN, _A, _B and _PENDING (docs/runbooks/smoke-test.md)")
	}

	runID := newRunID()
	cfg := &smoke.Config{
		Client: smoke.NewClient(*target, *origin, red), RunID: runID, Prompt: *prompt,
		Admin: actors["admin"], A: actors["a"], B: actors["b"], Pending: actors["pending"],
		RunTimeout: *runTO, TenantTimeout: *tenantTO,
	}
	human := stdout
	if *jsonOut == "-" {
		human = stderr
	}
	fmt.Fprintf(human, "antares smoke test  target=%s  run=%s  sessions supplied=%d/4\n", red.Clean(*target), runID, supplied)

	ctx, cancel := context.WithTimeout(ctx, *total)
	defer cancel()
	r := &smoke.Runner{Red: red, Log: human}
	started := time.Now()
	smoke.Run(ctx, cfg, r)
	rep := r.Report(*target, runID, started)
	fmt.Fprintln(human, rep.Summary())

	if *jsonOut != "" {
		raw, err := rep.JSON()
		if err == nil {
			raw = append(raw, '\n')
			if *jsonOut == "-" {
				_, err = stdout.Write(raw)
			} else {
				err = os.WriteFile(*jsonOut, raw, 0o644)
			}
		}
		if err != nil {
			fmt.Fprintf(stderr, "antares-smoke: could not write the result: %v\n", errors.Unwrap(err))
			return 1
		}
	}
	return rep.ExitCode()
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newRunID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
