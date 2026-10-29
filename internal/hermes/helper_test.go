package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const helperToken = "fabricated-helper-token-0123456789abcdef"

type helperCall struct{ path, auth, body string }

func fakeHelper(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, c helperCall)) (*Helper, *[]helperCall) {
	var calls []helperCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		c := helperCall{r.URL.Path, r.Header.Get("Authorization"), string(b[:n])}
		calls = append(calls, c)
		handle(w, r, c)
	}))
	t.Cleanup(srv.Close)
	h, err := NewHelper(srv.URL, helperToken, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return h, &calls
}

func ok(slug, key string) func(http.ResponseWriter, *http.Request, helperCall) {
	return func(w http.ResponseWriter, r *http.Request, c helperCall) {
		json.NewEncoder(w).Encode(map[string]string{"profile": slug, "key": key, "created_at": "x"})
	}
}

// The admin's tenant policy travels with the request; an invalid one never leaves Antares.
func TestHelperSendsTheTenantToolPolicy(t *testing.T) {
	h, calls := fakeHelper(t, ok(slA, keyA))
	if _, err := h.Rotate(context.Background(), slA, []string{"web", "tts"}); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].body; got != `{"slug":"`+slA+`","toolsets":["web","tts"]}` {
		t.Fatalf("body %s", got)
	}
	for _, bad := range [][]string{{}, {"terminal"}, {"computer_use"}, {"web", "web"}} {
		if _, err := h.Provision(context.Background(), slA, bad); !errors.Is(err, ErrPermanent) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("an invalid policy reached the helper: %d calls", len(*calls))
	}
}

func TestProvisionSendsTokenAndSlugOnlyAndReturnsTheKey(t *testing.T) {
	h, calls := fakeHelper(t, ok(slA, keyA))
	cred, err := h.Provision(context.Background(), slA, nil)
	if err != nil || cred.Key != keyA || cred.Profile != slA {
		t.Fatalf("cred=%v err=%v", cred, err)
	}
	c := (*calls)[0]
	if c.path != "/provision" || c.auth != "Bearer "+helperToken || c.body != `{"slug":"`+slA+`"}` {
		t.Fatalf("call = %+v", c)
	}
}

func TestCredentialNeverPrintsItsKey(t *testing.T) {
	c := Credential{Profile: slA, Key: keyA}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%v %+v %#v %s %q %x", c, c, c, c, c, c)} {
		if strings.Contains(s, keyA) {
			t.Fatalf("credential printed its key: %s", s)
		}
	}
	b, _ := json.Marshal(struct{ C Credential }{c})
	_ = b // Key is exported for sealing; the redaction above covers formatting verbs
}

func TestHelperFailureClasses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		class  error
		key    bool
	}{
		{"unauthorised token is permanent", 401, `{"error":"unauthorized: ` + keyA + `"}`, ErrPermanent, false},
		{"bad request is permanent", 400, `{"error":"slug"}`, ErrPermanent, false},
		{"upstream 502 is transient", 502, `{"error":"docker exec failed ` + keyA + `"}`, ErrRetryable, false},
		{"500 is transient", 500, ``, ErrRetryable, false},
		{"redirect is transient and not followed", 302, ``, ErrRetryable, false},
		{"garbage 200 is transient", 200, `not json`, ErrRetryable, false},
		{"answer for another profile is permanent", 200, `{"profile":"u_cccccccccccc","key":"` + keyA + `"}`, ErrPermanent, false},
		{"empty key is permanent", 200, `{"profile":"` + slA + `","key":""}`, ErrPermanent, false},
		{"key with whitespace is permanent", 200, `{"profile":"` + slA + `","key":"abc def abc def abc def"}`, ErrPermanent, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := fakeHelper(t, func(w http.ResponseWriter, r *http.Request, _ helperCall) {
				if c.status == 302 {
					http.Redirect(w, r, "http://127.0.0.1:1/x", c.status)
					return
				}
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			})
			_, err := h.Provision(context.Background(), slA, nil)
			if !errors.Is(err, c.class) {
				t.Fatalf("err = %v, want class %v", err, c.class)
			}
			if strings.Contains(err.Error(), keyA) || strings.Contains(err.Error(), helperToken) {
				t.Fatalf("error leaks a secret: %v", err)
			}
		})
	}
}

func TestProvision409MeansKeyExistsAndRotateRecovers(t *testing.T) {
	h, calls := fakeHelper(t, func(w http.ResponseWriter, r *http.Request, c helperCall) {
		if c.path == "/provision" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		ok(slA, keyB)(w, r, c)
	})
	if _, err := h.Provision(context.Background(), slA, nil); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("err = %v", err)
	}
	cred, err := h.Rotate(context.Background(), slA, nil)
	if err != nil || cred.Key != keyB || (*calls)[1].path != "/rotate" {
		t.Fatalf("rotate: %v %v", cred, err)
	}
	// A 409 from /rotate is not "key exists".
	h2, _ := fakeHelper(t, func(w http.ResponseWriter, r *http.Request, c helperCall) { w.WriteHeader(409) })
	if _, err := h2.Rotate(context.Background(), slA, nil); !errors.Is(err, ErrPermanent) {
		t.Fatalf("rotate 409 = %v", err)
	}
}

func TestHelperUnreachableIsTransient(t *testing.T) {
	h, _ := fakeHelper(t, ok(slA, keyA))
	srv := httptest.NewServer(nil)
	h2, _ := NewHelper(srv.URL, helperToken, nil)
	srv.Close()
	_ = h
	_, err := h2.Provision(context.Background(), slA, nil)
	if !errors.Is(err, ErrRetryable) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("err = %v", err)
	}
}

func TestHelperRefusesBadSlugBeforeAnyRequest(t *testing.T) {
	h, calls := fakeHelper(t, ok(slA, keyA))
	for _, s := range []string{"default", "", "u_short", "U_ABCDEFGHIJKL", "u_aaaaaaaaaaaa/../x", "../default", "u_" + strings.Repeat("a", 40)} {
		if _, err := h.Provision(context.Background(), s, nil); !errors.Is(err, ErrPermanent) {
			t.Errorf("slug %q: %v", s, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a bad slug reached the helper: %v", *calls)
	}
}

func TestNewHelperValidation(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "http://u:p@x:1", "http://x:1/provision", "http://x:1?a=b", "x:1"} {
		if _, err := NewHelper(u, helperToken, nil); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
	if _, err := NewHelper("http://127.0.0.1:8750", "short", nil); err == nil {
		t.Error("accepted a short token")
	}
}

func TestOnlyRotateMissingProfileOrKeyAllowsProvisionFallback(t *testing.T) {
	for _, path := range []string{"/rotate", "/provision"} {
		for _, status := range []int{400, 401, 403, 404, 409, 502} {
			t.Run(fmt.Sprintf("%s/%d", path, status), func(t *testing.T) {
				h, _ := fakeHelper(t, func(w http.ResponseWriter, _ *http.Request, _ helperCall) {
					w.WriteHeader(status)
					w.Write([]byte(keyA))
				})
				_, err := h.issue(context.Background(), path, slA, nil)
				want := path == "/rotate" && (status == 404 || status == 409)
				if errors.Is(err, ErrNotProvisioned) != want {
					t.Fatalf("fallback classification: %v", err)
				}
				if want && (!errors.Is(err, ErrPermanent) || errors.Is(err, ErrKeyExists)) {
					t.Fatalf("unexpected failure classes: %v", err)
				}
				if err == nil || strings.Contains(err.Error(), keyA) {
					t.Fatal("helper error missing or leaked response content")
				}
			})
		}
	}
}

// The skills a profile can load, read through the helper because Hermes' own route fails (helper.go, Skills).
func TestHelperSkillsReadsOneProfilesList(t *testing.T) {
	h, calls := fakeHelper(t, func(w http.ResponseWriter, _ *http.Request, c helperCall) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"name":"deep-research","description":"Research","category":"research"},{"name":"bad\u0007name"}]}`))
	})
	list, err := h.Skills(context.Background(), slA)
	if err != nil || len(list) != 1 || list[0].Name != "deep-research" {
		t.Fatalf("list %+v err %v", list, err)
	}
	if c := (*calls)[0]; c.path != "/skills" || c.body != `{"slug":"`+slA+`"}` || c.auth != "Bearer "+helperToken {
		t.Fatalf("call %+v", c)
	}
	missing, _ := fakeHelper(t, func(w http.ResponseWriter, _ *http.Request, _ helperCall) { w.WriteHeader(http.StatusNotFound) })
	if list, err := missing.Skills(context.Background(), slA); err != nil || len(list) != 0 {
		t.Fatalf("a missing profile: %v %v", list, err)
	}
	broken, _ := fakeHelper(t, func(w http.ResponseWriter, _ *http.Request, _ helperCall) { w.WriteHeader(http.StatusBadGateway) })
	if _, err := broken.Skills(context.Background(), slA); err == nil {
		t.Fatal("a failing helper must be an error, not an empty list")
	}
	if _, err := h.Skills(context.Background(), "../x"); err == nil {
		t.Fatal("an invalid slug reached the helper")
	}
}
