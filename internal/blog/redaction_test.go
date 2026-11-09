package blog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// Secrets and log hygiene (T071): whatever the assistant's side says, the author's credential, the run and session ids and
// the provider's own words stay on the server. Hermes is made to fail, to talk, and to answer in every way a check can end,
// with a secret-looking key and provider text in every place it can put them; everything an author can then fetch, and the
// server's log, is scanned for them.
func TestNothingFromTheAssistantsSideReachesAnAuthorOrTheLog(t *testing.T) {
	const (
		secret   = "sk-LEAK-4f9c2e71"
		provider = "upstream-provider-said-quota-exceeded"
	)
	leak := errors.New(secret + " " + provider)
	says := secret + " " + provider

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ids := regexp.MustCompile(`\b(run|sess)[a-z]0{8}\b`) // what the fake Hermes calls its runs and sessions

	scenarios := map[string]func(f *fakeHermes, ran *bool){
		"the run fails and says why": func(f *fakeHermes, ran *bool) {
			f.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
				*ran = true
				return emit("run.failed", map[string]any{"error": says, "message": says, "run_id": "runb00000000"})
			}
		},
		"the stream breaks with the provider's error": func(f *fakeHermes, ran *bool) {
			f.script = func(ctx context.Context, emit func(string, map[string]any) error) error { *ran = true; return leak }
			f.getRunErr = leak
		},
		"the assistant asks to run a command": func(f *fakeHermes, ran *bool) {
			f.script = func(ctx context.Context, emit func(string, map[string]any) error) error {
				*ran = true
				return emit("approval.request", map[string]any{"request_id": "r1", "command": "curl " + says})
			}
		},
		"the reply is not usable but quotes the secret": func(f *fakeHermes, ran *bool) { *ran = true; f.output = says },
		"the run cannot start":                          func(f *fakeHermes, ran *bool) { f.createRunErr = leak },
		"there is no session":                           func(f *fakeHermes, ran *bool) { f.createSessionErr = leak },
		"the check succeeds and the run reports usage": func(f *fakeHermes, ran *bool) {
			f.output = goodReply
			f.usage = map[string]any{"input_tokens": 10, "output_tokens": 5, "provider": provider, "api_key": secret}
		},
	}

	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			logs.Reset()
			fh := newFake()
			ran := false
			setup(fh, &ran)
			a := newAppWithHermes(t, fh)
			u := a.active("alice")
			post := u.draftToCheck("Probe")
			start := u.do("POST", "/api/blog/posts/"+post.ID+"/check", nil)
			if start.Code != 202 {
				t.Fatalf("start: %d %s", start.Code, start.Body)
			}
			u.waitCheck(post.ID, "ready", "failed", "timed_out", "cancelled")

			// Everything an author can read about this post and their account's blog.
			seen := []string{start.Body.String()}
			for _, path := range []string{"/api/blog/posts/" + post.ID + "/check", "/api/blog/posts/" + post.ID, "/api/blog/posts", "/api/blog/capabilities", "/api/blog/limits"} {
				seen = append(seen, u.do("GET", path, nil).Body.String())
			}
			all := strings.Join(seen, "\n")

			// The scan must be able to fail: the credential and profile really did reach Hermes, and the scenario really ran.
			_, _, bindings, _ := fh.snapshot()
			if len(bindings) == 0 || bindings[0].Key == "" || bindings[0].Profile == "" {
				t.Fatalf("nothing was sent to Hermes, so the scan proves nothing: %+v", bindings)
			}
			if fh.script != nil && !ran {
				t.Fatal("the scripted failure never ran, so the scan proves nothing")
			}

			bad := []string{secret, provider, bindings[0].Key, bindings[0].Profile}
			for _, b := range bad {
				if strings.Contains(all, b) {
					t.Errorf("%q is in what the author can read:\n%s", b, all)
				}
				if strings.Contains(logs.String(), b) {
					t.Errorf("%q is in the log:\n%s", b, logs.String())
				}
			}
			if m := ids.FindString(all); m != "" {
				t.Errorf("a run or session id (%q) is in what the author can read:\n%s", m, all)
			}
			if m := ids.FindString(logs.String()); m != "" {
				t.Errorf("a run or session id (%q) is in the log:\n%s", m, logs.String())
			}
		})
	}
}

// The binding's own formatting never gives the key up, in any verb: this is what keeps it out of a log line written by mistake.
func TestABindingNeverPrintsItsKey(t *testing.T) {
	b := hermes.Binding{Profile: "u_someprofile", Key: "sk-LEAK-4f9c2e71"}
	for _, s := range []string{
		strings.TrimSpace(strings.Join([]string{fmt.Sprintf("%v", b), fmt.Sprintf("%+v", b), fmt.Sprintf("%#v", b), fmt.Sprintf("%s", b), fmt.Sprintf("%q", b)}, " ")),
	} {
		if strings.Contains(s, "sk-LEAK") || strings.Contains(s, "u_someprofile") {
			t.Fatalf("a binding printed itself: %s", s)
		}
	}
}
