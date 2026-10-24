package chat

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Property and fuzz tests for the two things Phase 3 names: ownership over HTTP
// and SSE recovery. Both run as ordinary tests on a fixed corpus, so `go test`
// always exercises them, and as native fuzz targets under a bounded -fuzztime
// (`make fuzz`). The store-level ownership property is in
// internal/store/ownership_fuzz_test.go.

// ---- ownership over HTTP ------------------------------------------------------------

// foreignIDPool is every id an attacker might try in an id slot: the victim's
// real ids (and the wrong kind of them), a bystander's, unknown well-formed
// ids, and malformed or hostile strings.
func foreignIDPool(v *victim, bob, raw string) []string {
	pool := []string{v.chatID, v.runID, v.session, v.hermesRun, v.alice.id, bob, store.NewID(), raw,
		strings.ToUpper(v.chatID), strings.ToUpper(v.runID), v.chatID[:31], v.runID + "0", v.chatID + v.runID,
		" " + v.chatID, v.chatID + " ", v.chatID + "\x00", v.chatID + "%00", "%2e%2e", "..", ".", "", "null",
		"'" + v.chatID + "' OR 1=1", v.chatID + "/messages", "*", v.chatID[:16] + strings.Repeat("0", 16)}
	return pool
}

func aliceFingerprint(t testing.TB, v *victim) string {
	t.Helper()
	var runs, chats string
	if err := v.a.db.QueryRow(`SELECT COALESCE(group_concat(id||status||updated_at||COALESCE(error_code,'')||COALESCE(hermes_run_id,'')), '') FROM
		(SELECT * FROM runs WHERE user_id = ? ORDER BY id)`, v.alice.id).Scan(&runs); err != nil {
		t.Fatalf("alice's runs fingerprint: %v", err)
	}
	if err := v.a.db.QueryRow(`SELECT COALESCE(group_concat(id||status||updated_at||title||hermes_session_id), '') FROM
		(SELECT * FROM chats WHERE user_id = ? ORDER BY id)`, v.alice.id).Scan(&chats); err != nil {
		t.Fatalf("alice's chats fingerprint: %v", err)
	}
	return runs + "|" + chats
}

type ownFuzz struct {
	v               *victim
	bob             *person
	bobChat, bobRun string
	routes          []probe
	baseline        map[string]string // route key -> bob's answer for an unknown id
	fingerprint     string
}

func newOwnFuzz(t testing.TB) *ownFuzz {
	v := newVictim(t)
	// Keep repeated ownership probes reaching handlers; quota enforcement has its own tests.
	limits := auth.DefaultLimits()
	limits.RunsPerMinute, limits.MessagesPerMinute, limits.ArtifactsPerMinute = math.MaxInt, math.MaxInt, math.MaxInt
	limits.HistoryPerMinute, limits.DraftWritesPerMinute = math.MaxInt, math.MaxInt
	v.a.limits = &limits
	v.a.h = rebuild(t, v.a, v.a.svc)
	bob := v.a.user("bobby")
	o := &ownFuzz{v: v, bob: bob, routes: builtProbes, baseline: map[string]string{}}
	o.bobChat = bob.newChat()
	o.bobRun = bob.send(o.bobChat, "bob-input-1")
	o.fingerprint = aliceFingerprint(t, v)
	v.snapshot(t)
	return o
}

// try is one attack: bob sends route `ri` with id `id` in whatever slot the
// route has. Properties: a foreign or unknown id is the same 404 as a bare
// unknown id, leaks nothing, and leaves alice's data and Hermes untouched; bob's
// own id is the only thing that works, and then only under his own profile.
func (o *ownFuzz) try(t testing.TB, ri uint8, id string) {
	t.Helper()
	v := o.v
	p := o.routes[int(ri)%len(o.routes)]
	own := id == o.bobChat && strings.Contains(p.key, "{chatId}") || id == o.bobRun && strings.Contains(p.key, "{runId}")
	if own && strings.HasSuffix(p.key, "/events") {
		return // bob's own stream legitimately stays open
	}
	seg := url.PathEscape(id)
	mtd, path := fill(p.key, seg, seg)
	before := len(v.a.hx.requests())
	w := o.bob.do(mtd, path, map[string]string{"Idempotency-Key": nextIdem()}, requestBody(mtd))
	if m := v.leaks(w); m != "" {
		t.Fatalf("SURFACE BROKEN [%s]: id %q leaks %q: %d %s", p.surface, id, m, w.Code, w.Body.String())
	}
	if !own {
		if w.Code != 404 {
			t.Fatalf("SURFACE BROKEN [%s]: bob with id %q got %d %s, want 404", p.surface, id, w.Code, w.Body.String())
		}
		for _, q := range v.a.hx.requests()[before:] { // bob's own run is also talking to Hermes; only a call that carries the attacker's id counts
			if len(id) >= 16 && (strings.Contains(q.Path, id) || strings.Contains(q.Body, id) || strings.Contains(q.SessionKey, id)) {
				t.Fatalf("SURFACE BROKEN [%s]: a 404 for id %q still called Hermes: %s %s", p.surface, id, q.Method, q.Path)
			}
		}
		// Same answer as a well-formed unknown id (no oracle) when the id is well formed.
		if idRE.MatchString(id) {
			_, ref := fill(p.key, store.NewID(), store.NewID())
			want := o.bob.do(mtd, ref, map[string]string{"Idempotency-Key": nextIdem()}, requestBody(mtd))
			if !sameAnswer(w, want) {
				t.Fatalf("SURFACE BROKEN [%s]: id %q answers %d %q, unknown id answers %d %q", p.surface, id, w.Code, w.Body.String(), want.Code, want.Body.String())
			}
		}
	}
	for _, r := range v.a.hx.requests()[before:] {
		if !strings.HasPrefix(r.Path, "/p/"+o.bob.profile+"/") || r.Auth != "Bearer "+o.bob.key {
			t.Fatalf("SURFACE BROKEN [%s]: bob's request for id %q reached Hermes as %s with %q", p.surface, id, r.Path, r.Auth)
		}
	}
	if got := aliceFingerprint(t, v); got != o.fingerprint {
		t.Fatalf("SURFACE BROKEN [%s]: alice's rows changed after bob's request for id %q:\n before %s\n after  %s", p.surface, id, o.fingerprint, got)
	}
	if n := v.aliceHermesReqs(); n != v.hxAlice {
		t.Fatalf("SURFACE BROKEN [%s]: bob's request for id %q reached alice's Hermes profile", p.surface, id)
	}
}

func (o *ownFuzz) allIDs(raw string) []string {
	return append(foreignIDPool(o.v, o.bob.profile, raw), o.bobChat, o.bobRun)
}

func TestOwnershipOverHTTPProperty(t *testing.T) {
	o := newOwnFuzz(t)
	for _, id := range o.allIDs("zz") {
		for ri := range o.routes {
			o.try(t, uint8(ri), id)
		}
	}
}

func TestOwnershipOverHTTPRepeatedProbes(t *testing.T) {
	o := newOwnFuzz(t)
	for ri, p := range o.routes {
		if p.key == "POST /api/chats/{chatId}/messages" {
			for range 64 {
				o.try(t, uint8(ri), o.v.chatID)
			}
			return
		}
	}
	t.Fatal("run admission probe is missing")
}

// Draft and template writes have their own per-account bucket; a fuzzer (or an attacker) hammering a foreign chat's
// draft must keep getting the same 404, not trade it for a 429 once its own budget runs out.
func TestOwnershipOverHTTPRepeatedDraftWrites(t *testing.T) {
	o := newOwnFuzz(t)
	n := 0
	for ri, p := range o.routes {
		if strings.HasPrefix(p.key, "PUT /api/chats/{chatId}/draft") || strings.HasPrefix(p.key, "DELETE /api/chats/{chatId}/draft") {
			for range 300 { // well past the default of 240 a minute
				o.try(t, uint8(ri), o.v.chatID)
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("draft write probes are missing")
	}
}

func FuzzOwnershipOverHTTP(f *testing.F) {
	o := newOwnFuzz(f)
	f.Add(uint8(0), uint8(0), "")
	f.Add(uint8(3), uint8(1), "0123456789abcdef0123456789abcdef")
	f.Add(uint8(5), uint8(24), "%2e%2e/%2e%2e")
	f.Fuzz(func(t *testing.T, ri, sel uint8, raw string) {
		ids := o.allIDs(raw)
		o.try(t, ri, ids[int(sel)%len(ids)])
	})
}

// ---- SSE recovery ----------------------------------------------------------------------

// sseClient is a browser: it reconnects with Last-Event-ID and keeps what it
// rendered. It applies run.resumed{replace:true} as the real client does.
type sseClient struct {
	p      *person
	runID  string
	lastID string
	text   string
	seen   []sseEv
	terms  map[string]bool
	skewed bool // sent an id it did not receive, so gaps are expected
	open   *stream
	used   int
}

func (c *sseClient) connect(t testing.TB, lastID string) {
	c.disconnect()
	if lastID != c.lastID {
		c.skewed = true
	}
	c.open, c.used = c.p.open(c.runID, lastID), 0
	// Let the request get past authentication before anything can cancel it: a
	// request cancelled in the middle of its session query is a different test
	// (see the PR notes), and would make this one flaky.
	s := c.open
	eventually(t, func() bool {
		s.mu.Lock()
		n := s.buf.Len()
		s.mu.Unlock()
		select {
		case <-s.done:
			return true
		default:
		}
		return n > 0
	}, "the event stream to start")
}

// disconnect closes the connection, then applies everything it delivered: a
// browser has rendered whatever arrived before the socket closed.
func (c *sseClient) disconnect() {
	if c.open != nil {
		c.open.close()
		c.pull()
		c.open = nil
	}
}

// resume reconnects the way EventSource does: with the id of the last event
// actually received on the connection it is replacing.
func (c *sseClient) resume(t testing.TB) {
	c.disconnect()
	c.connect(t, c.lastID)
}

// pull applies the events that arrived since the last pull.
func (c *sseClient) pull() {
	if c.open == nil {
		return
	}
	evs := c.open.events()
	for _, e := range evs[c.used:] {
		c.seen = append(c.seen, e)
		if e.ID != "" {
			c.lastID = e.ID
		}
		switch e.Name {
		case "assistant.delta":
			var d struct{ Text string }
			_ = jsonUnmarshal(e.Data, &d)
			c.text += d.Text
		case "run.started", "run.resumed": // a journal replayed from its start opens with one of these
			c.text = ""
		case "run.completed", "run.failed", "run.cancelled":
			c.terms[e.Name] = true
		}
	}
	c.used = len(evs)
}

type sseRun struct {
	p       *person
	chat    string
	runID   string
	fr      *fakeRun
	hid     string
	input   string
	cl      *sseClient
	pushed  []string     // delta tokens in the order pushed
	subs    atomic.Int64 // upstream subscriptions opened
	ends    atomic.Int64 // upstream subscriptions that ended
	cancel  bool         // the client cancelled before any terminal frame
	quit    chan struct{}
	drained sync.WaitGroup
}

func startSSERun(t testing.TB, a *app, p *person, input string) *sseRun {
	r := &sseRun{p: p, input: input, quit: make(chan struct{})}
	r.chat = p.newChat()
	r.runID = p.send(r.chat, input)
	r.hid, r.fr = a.hx.runWithInput(t, input)
	// The fake blocks when its token channels fill; drain them for the whole scenario.
	r.drained.Add(1)
	go func() {
		defer r.drained.Done()
		for {
			select {
			case <-r.fr.opens:
				r.subs.Add(1)
			case <-r.fr.ended:
				r.ends.Add(1)
			case <-r.quit:
				return
			}
		}
	}()
	r.cl = &sseClient{p: p, runID: r.runID, terms: map[string]bool{}}
	r.cl.connect(t, "")
	eventually(t, func() bool { return r.subs.Load() >= 1 }, "first upstream subscription")
	return r
}

var tokSeq atomic.Int64

// step performs one scripted action. Terminal frames are never pushed here:
// those come last, so an earlier cancel is always the first terminal decision.
func (r *sseRun) step(t testing.TB, a *app, op, arg byte) {
	switch op % 7 {
	case 0:
		tok := fmt.Sprintf("<%s·%d>", r.p.sub, tokSeq.Add(1))
		r.pushed = append(r.pushed, tok)
		a.hx.push(r.fr, "assistant.delta", fmt.Sprintf(`{"delta":%q}`, tok))
	case 1:
		id := fmt.Sprintf("%032x", tokSeq.Add(1))
		a.hx.push(r.fr, "tool.started", fmt.Sprintf(`{"tool_call_id":%q,"label":"lookup"}`, id))
		a.hx.push(r.fr, "tool.completed", fmt.Sprintf(`{"tool_call_id":%q,"summary":"ok"}`, id))
	case 2: // frames that mean nothing, or only look like an ending
		junk := []struct{ name, data string }{
			{"run.complete", `{}`}, {"totally.unknown", `{"status":"completed"}`}, {"assistant.delta", `not json`},
			{"message.delta", `{"delta":""}`}, {"run.finished", `{"output":"x"}`}, {"", `{"type":"run.complet"}`}, {"status", `{"state":"completed"}`},
		}[int(arg)%7]
		a.hx.push(r.fr, junk.name, junk.data)
	case 3: // the upstream stream breaks; Hermes still says running
		if r.cancel {
			return
		}
		// A drop only breaks a stream that is open; one sent while the service is
		// between subscriptions would be absorbed by the next one and prove nothing.
		eventually(t, func() bool { return r.subs.Load() > r.ends.Load() || r.p.runStatus(r.runID) != store.RunRunning }, "an open upstream stream to drop")
		n := r.subs.Load()
		a.hx.dropStream(r.fr)
		eventually(t, func() bool { return r.ends.Load() >= n }, "the dropped upstream stream to end")
		eventually(t, func() bool {
			st := r.p.runStatus(r.runID)
			return r.subs.Load() > n || st != store.RunRunning
		}, "the service to resubscribe or stand down after a drop")
	case 4: // the browser reconnects properly
		r.cl.resume(t)
	case 5: // the browser reconnects with an id it never received, or garbage
		r.cl.disconnect()
		bad := []string{"", "x", "x-1", "-5", "0-0", "99999999999999999999", strings.Repeat("9", 400), "../..", "a-b-c"}
		id := bad[int(arg)%len(bad)]
		if arg%3 == 0 {
			if e, _, ok := strings.Cut(r.cl.lastID, "-"); ok {
				id = e + "-999999" // right epoch, a sequence number far ahead
			}
		}
		r.cl.connect(t, id)
	case 6: // the user cancels, once
		if r.cancel {
			return
		}
		w := r.p.do("POST", "/api/runs/"+r.runID+"/cancel", nil, nil)
		if w.Code != 200 {
			t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
		}
		r.cancel = true
	}
}

// truth is what Hermes finally does, chosen by the script.
type truth int

const (
	endCompleted truth = iota
	endFailed
	endInterrupted
	endCancelled
	endSilentCompleted // no terminal frame: the stream just drops and Hermes says completed
	endNever           // no terminal frame and Hermes keeps saying running
)

func (r *sseRun) finish(t testing.TB, a *app, end truth) (want string) {
	out := strings.Join(r.pushed, "")
	switch end {
	case endCompleted:
		a.hx.setStatus(r.fr, "completed", out)
		a.hx.push(r.fr, "run.completed", fmt.Sprintf(`{"output":%q}`, out))
	case endFailed:
		a.hx.setStatus(r.fr, "failed", "")
		a.hx.push(r.fr, "run.failed", `{}`)
	case endInterrupted:
		a.hx.setStatus(r.fr, "failed", "")
		a.hx.push(r.fr, "run.interrupted", `{}`)
	case endCancelled:
		a.hx.setStatus(r.fr, "cancelled", "")
		a.hx.push(r.fr, "run.cancelled", `{}`)
	case endSilentCompleted:
		a.hx.setStatus(r.fr, "completed", out)
		a.hx.dropStream(r.fr)
	case endNever:
		a.hx.dropStream(r.fr)
	}
	switch {
	case r.cancel:
		return store.RunCancelled // decided before Hermes said anything; a late "completed" must not undo it
	case end == endCompleted || end == endSilentCompleted:
		return store.RunCompleted
	case end == endFailed || end == endInterrupted:
		return store.RunFailed
	case end == endCancelled:
		return store.RunCancelled
	}
	return "" // not terminal
}

// settle reconnects like a browser until the run reaches its end or, for a run
// that has none, the service has had time to prove it does not invent one.
func (r *sseRun) settle(t testing.TB, a *app, want string) {
	start := time.Now()
	for {
		r.cl.resume(t)
		select {
		case <-r.cl.open.done:
		case <-time.After(250 * time.Millisecond):
		}
		r.cl.pull()
		st := r.p.runStatus(r.runID)
		switch {
		case want != "" && st == want && len(r.cl.terms) > 0:
			r.cl.disconnect()
			return
		case want == "" && time.Since(start) > 1500*time.Millisecond:
			r.cl.disconnect()
			return
		case time.Since(start) > 8*time.Second:
			t.Fatalf("run %s of %s never settled at %q (stored %s); events %v\nlog: %s\nhermes: %+v", r.runID, r.p.sub, want, st, names(r.cl.seen)[:min(len(r.cl.seen), 30)], logBuf.String(), a.hx.requests()[max(0, len(a.hx.requests())-6):])
		}
	}
}

func names(evs []sseEv) []string {
	var n []string
	for _, e := range evs {
		n = append(n, e.Name)
	}
	return n
}

// check asserts the SSE-recovery properties for one run.
func (r *sseRun) check(t testing.TB, a *app, want string, others ...*sseRun) {
	t.Helper()
	who := r.p.sub
	// 1. One logical run: one app run row, one Hermes run for this input.
	var rows int
	a.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE chat_id = ?`, r.chat).Scan(&rows)
	posts := 0
	for _, q := range a.hx.requests() {
		if q.Method == "POST" && q.Path == "/p/"+r.p.profile+"/v1/runs" && strings.Contains(q.Body, r.input) {
			posts++
		}
	}
	if rows != 1 || posts != 1 {
		t.Fatalf("%s: duplicated run: %d app rows, %d Hermes runs", who, rows, posts)
	}
	// 2. The stored and shown end agree with what Hermes did, and never claim completion otherwise.
	st := r.p.runStatus(r.runID)
	if want != "" && st != want {
		t.Fatalf("%s: stored status %s, want %s; events %v", who, st, want, names(r.cl.seen))
	}
	if want == "" && store.RunTerminal(st) {
		t.Fatalf("%s: run with no end on Hermes is stored as %s; events %v", who, st, names(r.cl.seen))
	}
	allowed := map[string]string{store.RunCompleted: "run.completed", store.RunFailed: "run.failed", store.RunCancelled: "run.cancelled"}[want]
	for term := range r.cl.terms {
		if term != allowed {
			t.Fatalf("%s: shown %q but the run is %q (want %s); events %v", who, term, st, want, names(r.cl.seen))
		}
	}
	if want != "" && !r.cl.terms[allowed] {
		t.Fatalf("%s: the end %q was never shown; events %v", who, allowed, names(r.cl.seen))
	}
	// 3. No token twice, none out of order, none from another user, in the rendered text.
	seq := r.cl.text
	pos := 0
	for _, tok := range r.pushed {
		switch c := strings.Count(seq, tok); {
		case c > 1:
			t.Fatalf("%s: token %s rendered %d times: %q", who, tok, c, seq)
		case c == 1:
			i := strings.Index(seq, tok)
			if i < pos {
				t.Fatalf("%s: token %s out of order in %q", who, tok, seq)
			}
			pos = i
		}
	}
	for _, o := range others {
		for _, tok := range o.pushed {
			for _, e := range r.cl.seen {
				if strings.Contains(e.Data, tok) || strings.Contains(e.ID, tok) {
					t.Fatalf("%s: received %s's token %s in %s", who, o.p.sub, tok, e.Name)
				}
			}
		}
	}
	// 4. Within one connection ids only go up, and a proper resume never repeats an event.
	if !r.cl.skewed {
		last := map[string]int{}
		for _, e := range r.cl.seen {
			if e.ID == "" {
				continue
			}
			ep, sq, _ := strings.Cut(e.ID, "-")
			var n int
			fmt.Sscanf(sq, "%d", &n)
			if n <= last[ep] {
				t.Fatalf("%s: event id %s repeated or went backwards after %d", who, e.ID, last[ep])
			}
			last[ep] = n
		}
	}
}

func (r *sseRun) stop(a *app) {
	// Leave nothing running for the next iteration.
	if st := r.p.runStatus(r.runID); !store.RunTerminal(st) {
		r.p.do("POST", "/api/runs/"+r.runID+"/cancel", nil, nil)
	}
	r.cl.disconnect()
	close(r.quit)
	r.drained.Wait()
}

var scenarioSeq atomic.Int64

// runSSEScenario drives two users' runs at once from one script and checks
// every property. The script is bytes: op, arg pairs; the first two bytes pick
// how each run ends.
func runSSEScenario(t testing.TB, a *app, script []byte) {
	if len(script) < 2 {
		return
	}
	// Inside a fuzz target only the iteration's t may be used (f.Helper panics).
	prev := a.t
	a.t = t
	defer func() { a.t = prev }()
	n := scenarioSeq.Add(1)
	pa := a.user(fmt.Sprintf("sa%x", n))
	pb := a.user(fmt.Sprintf("sb%x", n))
	ra := startSSERun(t, a, pa, fmt.Sprintf("input-a-%d", n))
	rb := startSSERun(t, a, pb, fmt.Sprintf("input-b-%d", n))
	defer ra.stop(a)
	defer rb.stop(a)
	endA, endB := truth(script[0]%6), truth(script[1]%6)
	body := script[2:]
	if len(body) > 40 {
		body = body[:40]
	}
	for i := 0; i+1 < len(body); i += 2 {
		r := ra
		if body[i]&0x80 != 0 {
			r = rb
		}
		r.step(t, a, body[i], body[i+1])
	}
	wantA, wantB := ra.finish(t, a, endA), rb.finish(t, a, endB)
	ra.settle(t, a, wantA)
	rb.settle(t, a, wantB)
	ra.check(t, a, wantA, rb)
	rb.check(t, a, wantB, ra)
}

// A fixed corpus of scripts covering each ending, repeated drops, hostile
// Last-Event-IDs, and a cancel followed by a late "completed".
var sseCorpus = [][]byte{
	{0, 0, 0, 0, 1, 0, 0, 0, 4, 0, 0, 0},                                    // completed, plain resume
	{1, 3, 0, 0, 3, 0, 0, 0, 3, 0, 4, 0, 0, 0, 3, 0, 4, 0},                  // failed / interrupted via repeated drops
	{2, 4, 0, 0, 6, 0, 0, 0, 0, 0},                                          // interrupted; silent-completed after a cancel on A
	{0, 0, 6, 0, 0, 0, 2, 3, 4, 0},                                          // cancel first, then a late completed frame
	{4, 5, 0, 0, 3, 0, 5, 1, 0, 0, 5, 3, 4, 0, 5, 0},                        // silent completion with hostile reconnects
	{5, 5, 0, 0, 3, 0, 3, 0, 3, 0, 3, 0, 0, 0, 4, 0, 5, 2},                  // never ends: must not be shown completed
	{3, 0, 0x80, 0, 0, 0, 0x80 | 3, 0, 1, 0, 0x80 | 4, 0, 2, 1, 2, 2, 2, 5}, // interleaved users, junk frames
}

func TestSSERecoveryProperty(t *testing.T) {
	a := newApp(t, appOpts{backoff: fastBackoff})
	for i, s := range sseCorpus {
		t.Run(fmt.Sprintf("script-%d", i), func(t *testing.T) { runSSEScenario(t, a, s) })
	}
}

func FuzzSSERecovery(f *testing.F) {
	a := newApp(f, appOpts{backoff: fastBackoff})
	for _, s := range sseCorpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, script []byte) { runSSEScenario(t, a, script) })
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
