//go:build capacity

package main

// The driver: real HTTP against the scratch Antares, sampling its /proc entry.
// Nothing here touches the store directly; sign-in, approval, tenant
// provisioning, chats and runs all go through the public API.

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/raufimusaddiq/antares/internal/smoke"
)

type state struct {
	Admin   string   `json:"admin"`
	Users   []string `json:"users"` // session cookies
	UserIDs []string `json:"userIds"`
}

type sample struct {
	t       time.Time
	rssKB   int
	cpuTick int64 // utime+stime
	fds     int
	threads int
}

func readProc(pid int) (sample, error) {
	s := sample{t: time.Now()}
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return s, err
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), " kB"))
		switch k {
		case "VmRSS":
			s.rssKB, _ = strconv.Atoi(v)
		case "Threads":
			s.threads, _ = strconv.Atoi(v)
		}
	}
	f.Close()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return s, err
	}
	fields := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+2:])) // after "(comm) "
	ut, _ := strconv.ParseInt(fields[11], 10, 64)
	st, _ := strconv.ParseInt(fields[12], 10, 64)
	s.cpuTick = ut + st
	if ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		s.fds = len(ents)
	}
	return s, nil
}

const clkTck = 100.0 // getconf CLK_TCK on Linux x86-64

type summary struct {
	Samples                          int     `json:"samples"`
	Seconds                          float64 `json:"seconds"`
	RSSMeanMiB, RSSP95MiB, RSSMaxMiB float64
	CPUPercent                       float64 `json:"cpuPercentOfOneCore"`
	FDsMax, ThreadsMax               int
	SeriesMiBEvery30s                []float64
}

// watch samples pid once a second until done is closed.
func watch(pid int, done <-chan struct{}) <-chan summary {
	out := make(chan summary, 1)
	go func() {
		var ss []sample
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		take := func() {
			if s, err := readProc(pid); err == nil {
				ss = append(ss, s)
			}
		}
		take()
		for {
			select {
			case <-done:
				take()
				out <- summarize(ss)
				return
			case <-tk.C:
				take()
			}
		}
	}()
	return out
}

func summarize(ss []sample) summary {
	var r summary
	if len(ss) < 2 {
		return r
	}
	var rss []float64
	var sum float64
	for _, s := range ss {
		m := float64(s.rssKB) / 1024
		rss = append(rss, m)
		sum += m
		if len(rss)%30 == 1 {
			r.SeriesMiBEvery30s = append(r.SeriesMiBEvery30s, m)
		}
		r.FDsMax, r.ThreadsMax = max(r.FDsMax, s.fds), max(r.ThreadsMax, s.threads)
	}
	sort.Float64s(rss)
	r.Samples = len(ss)
	r.RSSMeanMiB, r.RSSP95MiB, r.RSSMaxMiB = sum/float64(len(rss)), rss[int(float64(len(rss)-1)*0.95)], rss[len(rss)-1]
	r.Seconds = ss[len(ss)-1].t.Sub(ss[0].t).Seconds()
	r.CPUPercent = float64(ss[len(ss)-1].cpuTick-ss[0].cpuTick) / clkTck / r.Seconds * 100
	return r
}

// settledRSS is the median of five one-second RSS reads.
func settledRSS(pid int) float64 {
	var v []float64
	for i := 0; i < 5; i++ {
		if s, err := readProc(pid); err == nil {
			v = append(v, float64(s.rssKB)/1024)
		}
		time.Sleep(time.Second)
	}
	sort.Float64s(v)
	if len(v) == 0 {
		return 0
	}
	return v[len(v)/2]
}

type env struct {
	c      *smoke.Client
	idp    string
	st     state
	statef string
	pid    int
	db     string
	prefix string
}

func (e *env) sess(i int) *smoke.Session { return &smoke.Session{Cookie: e.st.Users[i]} }

func (e *env) login(ctx context.Context, sub string) (string, error) {
	resp, err := e.c.HTTP.Get(e.c.Base + "/api/auth/google/start")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != 302 {
		return "", fmt.Errorf("start: %d", resp.StatusCode)
	}
	var flow string
	for _, ck := range resp.Cookies() {
		if ck.Name == "__Host-antares_oidc" {
			flow = ck.Value
		}
	}
	q := loc.Query()
	code := "code-" + sub
	g := url.Values{"code": {code}, "sub": {sub}, "nonce": {q.Get("nonce")}, "challenge": {q.Get("code_challenge")}, "aud": {q.Get("client_id")}}
	r, err := e.c.HTTP.Get(e.idp + "/_grant?" + g.Encode())
	if err != nil {
		return "", err
	}
	r.Body.Close()
	cb, _ := http.NewRequestWithContext(ctx, "GET", e.c.Base+"/api/auth/google/callback?code="+code+"&state="+url.QueryEscape(q.Get("state")), nil)
	cb.AddCookie(&http.Cookie{Name: "__Host-antares_oidc", Value: flow})
	resp, err = e.c.HTTP.Do(cb)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == smoke.SessionCookie && ck.Value != "" {
			return ck.Value, nil
		}
	}
	return "", fmt.Errorf("no session for %s (HTTP %d)", sub, resp.StatusCode)
}

type me struct {
	ID      string `json:"id"`
	CanChat bool   `json:"canChat"`
}

func (e *env) me(ctx context.Context, s *smoke.Session) (me, error) {
	r, err := e.c.Do(ctx, s, "GET", "/api/me", nil, nil)
	var m me
	if err != nil || r.Status != 200 {
		return m, fmt.Errorf("me: %v", err)
	}
	return m, r.JSON(&m)
}

func parallel(n, width int, f func(i int) error) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, width)
	var first atomic.Value
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := f(i); err != nil {
				first.CompareAndSwap(nil, err)
			}
		}(i)
	}
	wg.Wait()
	if v := first.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (e *env) setup(ctx context.Context, n int) error {
	adm, err := e.login(ctx, "sub-admin")
	if err != nil {
		return err
	}
	e.st = state{Admin: adm, Users: make([]string, n), UserIDs: make([]string, n)}
	if err := parallel(n, 10, func(i int) error {
		ck, err := e.login(ctx, fmt.Sprintf("sub-%s%04d", e.prefix, i))
		e.st.Users[i] = ck
		return err
	}); err != nil {
		return err
	}
	a := &smoke.Session{Cookie: adm}
	for t := time.Now(); ; time.Sleep(100 * time.Millisecond) { // the admin API needs the admin's own tenant READY
		if m, err := e.me(ctx, a); err == nil && m.CanChat {
			break
		} else if time.Since(t) > time.Minute {
			r, _ := e.c.Do(ctx, a, "GET", "/api/me", nil, nil)
			return fmt.Errorf("admin tenant never READY: %v", r)
		}
	}
	if err := parallel(n, 10, func(i int) error {
		m, err := e.me(ctx, e.sess(i))
		if err != nil {
			return err
		}
		e.st.UserIDs[i] = m.ID
		r, err := e.c.Do(ctx, a, "POST", "/api/admin/users/"+m.ID+"/approve", nil, map[string]string{"reason": "capacity"})
		if err != nil || r.Status != 200 {
			return fmt.Errorf("approve %d: %v %v", i, err, r)
		}
		return nil
	}); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Minute)
	for i := 0; i < n; i++ {
		for {
			m, err := e.me(ctx, e.sess(i))
			if err == nil && m.CanChat {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("user %d never became READY", i)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	b, _ := json.Marshal(e.st)
	return os.WriteFile(e.statef, b, 0o600)
}

var terminal = map[string]bool{"run.completed": true, "run.failed": true, "run.cancelled": true}

func (e *env) newChat(ctx context.Context, i int) (string, error) {
	r, err := e.c.Do(ctx, e.sess(i), "POST", "/api/chats", nil, map[string]string{"title": "capacity"})
	var c struct{ ID string }
	if err != nil || r.Status != 201 || r.JSON(&c) != nil {
		return "", fmt.Errorf("create chat: %v %v", err, r)
	}
	return c.ID, nil
}

var seq atomic.Int64

const prompt = "Please summarise the main trade-offs between SQLite and Postgres for a small multi-user web application, in a few short paragraphs, and say which you would pick for about fifty users."

func (e *env) send(ctx context.Context, i int, chat string) (string, error) {
	k := fmt.Sprintf("cap-%d-%d", time.Now().UnixNano(), seq.Add(1))
	r, err := e.c.Do(ctx, e.sess(i), "POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": k}, map[string]string{"input": prompt})
	var run struct {
		RunID string `json:"runId"`
	}
	if err != nil || r.Status != 202 || r.JSON(&run) != nil {
		return "", fmt.Errorf("send: %v %v", err, r)
	}
	return run.RunID, nil
}

// follow reads the run's SSE stream to its terminal event.
func (e *env) follow(ctx context.Context, i int, runID string) (string, error) {
	term := ""
	_, err := e.c.Stream(ctx, e.sess(i), "/api/runs/"+runID+"/events", func(ev smoke.SSEEvent) bool {
		if terminal[ev.Name] {
			term = ev.Name
			return true
		}
		return false
	})
	return term, err
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
	return d[int(float64(len(d)-1)*p)]
}

func emit(name string, v any) {
	b, _ := json.MarshalIndent(map[string]any{name: v}, "", "  ")
	fmt.Println(string(b))
}

func runLoad(args []string) error {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	base := fs.String("base", "http://127.0.0.1:18080", "scratch Antares")
	idp := fs.String("idp", "http://127.0.0.1:18081/idp", "fake provider")
	statef := fs.String("state", "state.json", "session state file")
	pid := fs.Int("pid", 0, "pid of the scratch Antares")
	dbp := fs.String("db", "", "path of the SQLite file")
	n := fs.Int("n", 10, "users / concurrent runs / streams")
	secs := fs.Int("secs", 60, "duration")
	chats := fs.Int("chats", 20, "fill: chats per user")
	msgs := fs.Int("msgs", 10, "fill: messages per chat")
	rpu := fs.Int("rpu", 1, "streams: runs per user")
	extra := fs.Int("extra", 0, "streams: extra streams per run")
	prefix := fs.String("prefix", "u", "setup: account name prefix (a fresh prefix gives fresh accounts)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("one phase: setup|idle|load|fill|streams|db")
	}
	c := smoke.NewClient(*base, "https://antares.scratch.test", &smoke.Redactor{})
	c.HTTP.Transport = &http.Transport{MaxIdleConnsPerHost: 512, MaxConnsPerHost: 0, IdleConnTimeout: 90 * time.Second}
	e := &env{c: c, idp: *idp, statef: *statef, pid: *pid, db: *dbp, prefix: *prefix}
	ctx := context.Background()
	if p := fs.Arg(0); p != "setup" && p != "idle" && p != "db" {
		b, err := os.ReadFile(e.statef)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &e.st); err != nil {
			return err
		}
	}
	switch fs.Arg(0) {
	case "setup":
		t := time.Now()
		if err := e.setup(ctx, *n); err != nil {
			return err
		}
		emit("setup", map[string]any{"users": *n, "seconds": time.Since(t).Seconds()})
	case "idle":
		done := make(chan struct{})
		res := watch(e.pid, done)
		time.Sleep(time.Duration(*secs) * time.Second)
		close(done)
		emit("idle", <-res)
	case "load", "fill":
		return e.loadOrFill(ctx, fs.Arg(0), *n, *secs, *chats, *msgs)
	case "search": // one all-chats search per user: builds that user's derived index
		done := make(chan struct{})
		res := watch(e.pid, done)
		t := time.Now()
		var bad atomic.Int64
		_ = parallel(*n, 10, func(i int) error {
			r, err := e.c.Do(ctx, e.sess(i), "GET", "/api/search/chats?q=zzzqqq", nil, nil)
			if err != nil || r.Status != 200 {
				bad.Add(1)
				fmt.Fprintln(os.Stderr, "search:", err, r)
			}
			return nil
		})
		close(done)
		emit("search", map[string]any{"users": *n, "failed": bad.Load(), "seconds": time.Since(t).Seconds(), "server": <-res})
	case "streams":
		return e.streams(ctx, *n, *rpu, *extra)
	case "db":
		for _, suf := range []string{"", "-wal", "-shm"} {
			if st, err := os.Stat(*dbp + suf); err == nil {
				fmt.Printf("%s%s %d bytes\n", filepath.Base(*dbp), suf, st.Size())
			}
		}
	}
	return nil
}

func (e *env) loadOrFill(ctx context.Context, phase string, n, secs, chats, msgs int) error {
	done := make(chan struct{})
	res := watch(e.pid, done)
	var ok, bad atomic.Int64
	var mu sync.Mutex
	var lat []time.Duration
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	t0 := time.Now()
	err := parallel(n, n, func(i int) error {
		do := func(chat string) bool {
			t := time.Now()
			id, err := e.send(ctx, i, chat)
			if err == nil {
				var term string
				if term, err = e.follow(ctx, i, id); err == nil && term != "run.completed" {
					err = fmt.Errorf("terminal %q", term)
				}
			}
			if err != nil {
				bad.Add(1)
				fmt.Fprintln(os.Stderr, "run error:", strings.SplitN(err.Error(), "map[", 2)[0])
				time.Sleep(3 * time.Second) // back off: a refused attempt still counts against the per-minute limit
				return false
			}
			ok.Add(1)
			mu.Lock()
			lat = append(lat, time.Since(t))
			mu.Unlock()
			return true
		}
		if phase == "load" { // n workers, each keeps exactly one run in flight until the deadline
			chat, err := e.newChat(ctx, i)
			if err != nil {
				return err
			}
			for time.Now().Before(deadline) {
				do(chat)
			}
			return nil
		}
		for ci := 0; ci < chats; ci++ {
			chat, err := e.newChat(ctx, i)
			if err != nil {
				return err
			}
			for mi := 0; mi < msgs; mi++ {
				do(chat)
			}
		}
		return nil
	})
	close(done)
	r := <-res
	emit(phase, map[string]any{"n": n, "wallSeconds": time.Since(t0).Seconds(), "runsOK": ok.Load(), "runsFailed": bad.Load(),
		"latencyP50": pct(lat, .5).String(), "latencyP95": pct(lat, .95).String(), "server": r})
	return err
}

// streams holds runs open (the fake Hermes is told to hold them long) and reads the
// server's RSS before any run, with one client stream per run, with the extra
// streams, and after everything is closed.
func (e *env) streams(ctx context.Context, n, rpu, extra int) error {
	base := settledRSS(e.pid)
	b0, _ := readProc(e.pid)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type target struct {
		user int
		run  string
	}
	var mu sync.Mutex
	var ts []target
	if err := parallel(n, 20, func(i int) error {
		for r := 0; r < rpu; r++ {
			chat, err := e.newChat(ctx, i)
			if err != nil {
				return err
			}
			id, err := e.send(ctx, i, chat)
			if err != nil {
				return err
			}
			mu.Lock()
			ts = append(ts, target{i, id})
			mu.Unlock()
		}
		return nil
	}); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	runsOnly := settledRSS(e.pid)
	var open atomic.Int64
	var wg sync.WaitGroup
	openStream := func(t target) {
		wg.Add(1)
		open.Add(1)
		go func() {
			defer wg.Done()
			defer open.Add(-1)
			_, _ = e.c.Stream(sctx, e.sess(t.user), "/api/runs/"+t.run+"/events", func(smoke.SSEEvent) bool { return false })
		}()
	}
	for _, t := range ts {
		openStream(t)
	}
	time.Sleep(3 * time.Second)
	one := settledRSS(e.pid)
	s1, _ := readProc(e.pid)
	var more float64
	var s2 sample
	if extra > 0 {
		for k := 0; k < extra; k++ {
			for _, t := range ts {
				openStream(t)
			}
		}
		time.Sleep(3 * time.Second)
		more = settledRSS(e.pid)
		s2, _ = readProc(e.pid)
	}
	cancel()
	wg.Wait()
	time.Sleep(2 * time.Second)
	// the held runs were abandoned by the client only; they finish on their own
	emit("streams", map[string]any{
		"users": n, "runsPerUser": rpu, "runs": len(ts), "extraPerRun": extra,
		"rssBeforeRunsMiB": base, "rssRunsNoClientStreamMiB": runsOnly, "rssOneStreamPerRunMiB": one, "rssWithExtraMiB": more,
		"fdsBefore": b0.fds, "fdsOneStream": s1.fds, "fdsWithExtra": s2.fds,
		"threadsBefore": b0.threads, "threadsOneStream": s1.threads, "threadsWithExtra": s2.threads,
	})
	return nil
}
