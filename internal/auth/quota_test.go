package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/raufimusaddiq/antares/internal/config"
	"github.com/raufimusaddiq/antares/internal/kenari"
)

// The shared plan quota is everyone's limit, so every active account sees how much of each window is used and when it
// resets, and nothing else: no Rupiah amount, no balance, no per-model spend, no key. Not an anonymous or pending user.
func TestSharedQuotaShowsFractionsToActiveUsersOnly(t *testing.T) {
	h := newHarness(t)
	quota, err := os.ReadFile("../kenari/testdata/account/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/v1/account/quota":
			w.Write(quota)
		case "/v1/account/balance":
			w.Write([]byte(`{"balance_micro_idr":25000000000,"reserved_micro_idr":0,"available_micro_idr":25000000000,"currency":"IDR"}`))
		default:
			w.Write([]byte(`{"object":"usage.summary","start":1,"end":2,"data":[],"total":{"requests":7,"prompt_tokens":1,"completion_tokens":2,"cached_prompt_tokens":0,"cache_write_prompt_tokens":0,"cost_micro_idr":3,"catalog_micro_idr":3}}`))
		}
	}))
	defer srv.Close()
	h.a.usage = kenari.NewAccount(srv.URL, config.Secret(usageTestKey), nil)
	user := h.userIn("usr", "APPROVED", "READY", "USER", true)
	pending := h.userIn("pnd", "PENDING", "UNPROVISIONED", "USER", false)

	for name, b := range map[string]*browser{"anonymous": h.browser(), "pending": pending} {
		if w := b.get("/api/quota"); w.Code == 200 {
			t.Errorf("%s GET = 200 %s", name, w.Body.String())
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("refused callers made Kenari receive %d requests", n)
	}
	w := user.get("/api/quota")
	body := w.Body.String()
	if w.Code != 200 || !has(body, `"state":"OK"`) || !has(body, `"plan":"Indie"`) || !has(body, `"key":"week"`) || !has(body, `"usedFrac":0.16`) || !has(body, `"resetsAt":"2026-10-13T07:12:00Z"`) {
		t.Fatalf("user GET = %d %s", w.Code, body)
	}
	for _, leak := range []string{"75000", "12000", "63000", "Rp", "budget", "remaining", "25000000000", "balance", usageTestKey} {
		if strings.Contains(body, leak) {
			t.Errorf("the shared quota carries %q: %s", leak, body)
		}
	}
	// Opening the app many times reads the cached snapshot: Kenari is asked once.
	before := hits.Load()
	for i := 0; i < 20; i++ {
		user.get("/api/quota")
	}
	if hits.Load() != before {
		t.Errorf("repeated reads reached Kenari %d more times", hits.Load()-before)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestSharedQuotaWithoutAReaderSaysNotConfigured(t *testing.T) {
	h := newHarness(t)
	user := h.userIn("usr", "APPROVED", "READY", "USER", true)
	if w := user.get("/api/quota"); w.Code != 200 || !has(w.Body.String(), `"state":"NOT_CONFIGURED"`) || has(w.Body.String(), "windows") {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
}
