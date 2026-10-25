package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/raufimusaddiq/antares/internal/config"
	"github.com/raufimusaddiq/antares/internal/kenari"
)

const usageTestKey = "kn-TESTKEY-not-a-real-credential"

func fakeProvider(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/v1/account/balance":
			w.Write([]byte(`{"object":"account.balance","balance_micro_idr":25000000000,"reserved_micro_idr":0,"available_micro_idr":25000000000,"currency":"IDR"}`))
		case "/v1/account/quota":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"code":"restricted_key_not_allowed"}}`))
		case "/v1/usage":
			w.Write([]byte(`{"object":"usage.summary","start":1,"end":2,"data":[],"total":{"requests":7,"prompt_tokens":1,"completion_tokens":2,"cached_prompt_tokens":0,"cache_write_prompt_tokens":0,"cost_micro_idr":3,"catalog_micro_idr":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// #146: the shared provider account's balance and spend are the admin's alone. Another user, an
// anonymous caller and a user's own request fields must never reach Kenari or its numbers.
func TestKenariUsageIsAdminOnlyAndNeverCarriesTheKey(t *testing.T) {
	h := newHarness(t)
	var hits atomic.Int32
	srv := fakeProvider(t, &hits)
	h.a.usage = kenari.NewAccount(srv.URL, config.Secret(usageTestKey), nil)
	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	user := h.userIn("usr", "APPROVED", "READY", "USER", true)

	for name, b := range map[string]*browser{"ordinary user": user, "anonymous": h.browser()} {
		w := b.get("/api/admin/kenari-usage")
		if w.Code != 403 && w.Code != 401 {
			t.Errorf("%s GET = %d", name, w.Code)
		}
		if strings.Contains(w.Body.String(), "25000000000") || strings.Contains(w.Body.String(), `"balance"`) {
			t.Errorf("%s received account figures: %s", name, w.Body.String())
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("refused callers made Kenari receive %d requests", n)
	}
	if w := admin.get("/api/admin/kenari-usage?profile=u_other"); w.Code != 400 {
		t.Errorf("a client-chosen profile = %d, want 400", w.Code)
	}

	w := admin.get("/api/admin/kenari-usage")
	body := w.Body.String()
	if w.Code != 200 || !has(body, `"state":"OK"`) || !has(body, `"scope":"shared-account"`) || !has(body, `"balanceMicroIdr":25000000000`) {
		t.Fatalf("admin GET = %d %s", w.Code, body)
	}
	if !has(body, `"state":"REFUSED"`) || !has(body, `"code":"restricted_key_not_allowed"`) {
		t.Errorf("a refused section must say so: %s", body)
	}
	if has(body, usageTestKey) || has(body, "Bearer") {
		t.Errorf("the provider key reached the response: %s", body)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestKenariUsageNotConfiguredShowsNothing(t *testing.T) {
	h := newHarness(t) // no reader at all
	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	w := admin.get("/api/admin/kenari-usage")
	if w.Code != 200 || !has(w.Body.String(), `"state":"NOT_CONFIGURED"`) || has(w.Body.String(), `"balance"`) {
		t.Fatalf("no reader: %d %s", w.Code, w.Body.String())
	}
	h.a.usage = kenari.NewAccount("http://127.0.0.1:1", nil, nil) // reader without a key
	w = admin.get("/api/admin/kenari-usage")
	if !has(w.Body.String(), `"state":"NOT_CONFIGURED"`) {
		t.Fatalf("keyless reader: %s", w.Body.String())
	}
}

func TestKenariUsageOutageIsUnavailableNotZero(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 502) }))
	defer srv.Close()
	h.a.usage = kenari.NewAccount(srv.URL, config.Secret(usageTestKey), nil)
	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	w := admin.get("/api/admin/kenari-usage")
	if w.Code != 200 || !has(w.Body.String(), `"state":"UNAVAILABLE"`) || has(w.Body.String(), `"balance"`) || has(w.Body.String(), "microIdr") {
		t.Fatalf("outage: %d %s", w.Code, w.Body.String())
	}
}
