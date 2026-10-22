//go:build livehermes

package hermes

// The live cross-user memory test (PRD §5.6, issue #24). It is NOT part of the
// normal suite: it needs a real Hermes with two provisioned test profiles and it
// writes to their memory, which AGENTS.md does not let a coding agent do. The
// owner (or an agent they authorise on the ticket) runs it:
//
//	ANTARES_LIVE_HERMES_URL=http://hermes:8642 \
//	ANTARES_LIVE_PROFILE_A=testa ANTARES_LIVE_KEY_A=... \
//	ANTARES_LIVE_PROFILE_B=testb ANTARES_LIVE_KEY_B=... \
//	  go test -tags livehermes -run TestLive -v -count=1 ./internal/hermes
//
// Keys come from the environment and are never printed (Binding redacts itself).
// As of this commit it has been compiled and vetted but never run: see
// docs/runbooks/memory-scoping.md for the status of the item it proves.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func liveEnv(t *testing.T) (c *Client, a, b Binding) {
	t.Helper()
	url := os.Getenv("ANTARES_LIVE_HERMES_URL")
	pa, ka := os.Getenv("ANTARES_LIVE_PROFILE_A"), os.Getenv("ANTARES_LIVE_KEY_A")
	pb, kb := os.Getenv("ANTARES_LIVE_PROFILE_B"), os.Getenv("ANTARES_LIVE_KEY_B")
	if url == "" || pa == "" || ka == "" || pb == "" || kb == "" || pa == pb {
		t.Fatal("set ANTARES_LIVE_HERMES_URL and two different profiles with their keys (ANTARES_LIVE_PROFILE_A/B, ANTARES_LIVE_KEY_A/B)")
	}
	c, err := NewClient(url, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c, Binding{Profile: pa, Key: ka}, Binding{Profile: pb, Key: kb}
}

// ask runs one prompt in a fresh session and returns Hermes' final output.
func ask(t *testing.T, c *Client, b Binding, sessionKey, prompt string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sid, err := c.CreateSession(ctx, b)
	if err != nil {
		t.Fatalf("create session on %s: %v", b.Profile, err)
	}
	rid, err := c.CreateRun(ctx, b, RunRequest{SessionID: sid, Input: prompt, SessionKey: sessionKey, IdempotencyKey: "live-" + sid})
	if err != nil {
		t.Fatalf("create run on %s: %v", b.Profile, err)
	}
	for {
		st, err := c.GetRun(ctx, b, rid)
		if err != nil {
			t.Fatalf("run status: %v", err)
		}
		if st.Terminal() {
			if st.State != StateCompleted {
				t.Fatalf("run ended %s", st.State)
			}
			return st.Output
		}
		select {
		case <-ctx.Done():
			t.Fatal("run did not finish in time")
		case <-time.After(2 * time.Second):
		}
	}
}

func TestLiveMemoryIsPerProfile(t *testing.T) {
	c, a, b := liveEnv(t)
	canary := "canary-" + strings.ToLower(time.Now().UTC().Format("150405")) + "-zq7"
	keyA, keyB := "chatui:user:live-a", "chatui:user:live-b"

	ask(t, c, a, keyA, "Please remember this exact token for later and confirm briefly: "+canary)
	recallPrompt := "What token did I ask you to remember earlier? Reply with the token only, or the single word NONE if I never told you one."
	// Control: A, in a brand new session, must recall it. If it cannot, the memory
	// provider is not persisting anything and "B did not recall it" would prove nothing.
	if got := ask(t, c, a, keyA, recallPrompt); !strings.Contains(got, canary) {
		t.Fatalf("INCONCLUSIVE: profile A did not recall its own token in a new session (got %q); memory is not persisting, so isolation cannot be judged", got)
	}
	if got := ask(t, c, b, keyB, recallPrompt); strings.Contains(got, canary) {
		t.Fatalf("LEAK: profile B recalled profile A's token")
	}
}

func TestLiveCredentialsAndSessionsDoNotCross(t *testing.T) {
	c, a, b := liveEnv(t)
	ctx := context.Background()
	sidA, err := c.CreateSession(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	// B's key on A's route, A's key on B's route, and the default profile's key (not supplied here) must all be refused.
	for name, bind := range map[string]Binding{
		"B's key on A's route": {Profile: a.Profile, Key: b.Key},
		"A's key on B's route": {Profile: b.Profile, Key: a.Key},
	} {
		if _, err := c.CreateSession(ctx, bind); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A's session id, asked for through B's own (valid) binding, must not be readable.
	if msgs, err := c.Transcript(ctx, b, sidA); err == nil && len(msgs) > 0 {
		t.Errorf("B read %d message(s) of A's session", len(msgs))
	}
	if _, err := c.CreateRun(ctx, b, RunRequest{SessionID: sidA, Input: "hello", SessionKey: "chatui:user:live-b", IdempotencyKey: "live-x-" + sidA}); err == nil {
		t.Error("B started a run in A's session")
	}
}
