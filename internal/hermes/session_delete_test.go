package hermes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// DELETE /api/sessions/{id} (issue #148). The answers below are the ones
// gateway/platforms/api_server.py _handle_delete_session produces: 200 hermes.session.deleted with
// deleted:true|false, 404 code session_not_found, 409 code session_active_turn, 503 code
// session_db_unavailable. Ids and keys are fabricated.

func TestDeleteSessionAnswers(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"deleted", 200, `{"object":"hermes.session.deleted","id":"sess_1","deleted":true}`, func(e error) bool { return e == nil }},
		{"200 but not deleted", 200, `{"object":"hermes.session.deleted","id":"sess_1","deleted":false}`, func(e error) bool { return CodeOf(e) == CodeHermesUnavailable }},
		{"200 without a verdict", 200, `{}`, func(e error) bool { return CodeOf(e) == CodeHermesUnavailable }},
		{"200 not json", 200, `ok`, func(e error) bool { return CodeOf(e) == CodeHermesUnavailable }},
		{"session not found", 404, `{"error":{"message":"Session not found: sess_1","type":"invalid_request_error","param":null,"code":"session_not_found"}}`, func(e error) bool { return errors.Is(e, ErrSessionGone) }},
		{"unknown profile route is not 'gone'", 404, `Unknown or unconfigured profile`, func(e error) bool { return CodeOf(e) == CodeCapabilityNA && !errors.Is(e, ErrSessionGone) }},
		{"404 with another code", 404, `{"error":{"code":"route_not_found"}}`, func(e error) bool { return CodeOf(e) == CodeCapabilityNA && !errors.Is(e, ErrSessionGone) }},
		{"turn active", 409, `{"error":{"code":"session_active_turn"}}`, func(e error) bool { return CodeOf(e) == CodeRunConflict }},
		{"session db down", 503, `{"error":{"code":"session_db_unavailable"}}`, func(e error) bool { return CodeOf(e) == CodeHermesUnavailable }},
		{"key refused", 401, `{"error":"bad key ` + testKey + `"}`, func(e error) bool { return CodeOf(e) == CodeTenantNotReady }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var gotMethod, gotPath, gotAuth string
			cl, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}), ClientOptions{})
			err := cl.DeleteSession(context.Background(), bind(), "sess_1")
			if !c.check(err) {
				t.Errorf("status %d body %q -> %v", c.status, c.body, err)
			}
			if gotMethod != "DELETE" || gotPath != "/p/"+testProfile+"/api/sessions/sess_1" || gotAuth != "Bearer "+testKey {
				t.Errorf("request = %s %s %q", gotMethod, gotPath, gotAuth)
			}
			if err != nil && strings.Contains(err.Error(), testKey) {
				t.Errorf("error carries the key: %v", err)
			}
		})
	}
}

func TestDeleteSessionNeverSendsAnUnsafeID(t *testing.T) {
	called := false
	cl, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }), ClientOptions{})
	for _, id := range []string{"", "../x", "a/b", "sess 1", strings.Repeat("a", 300)} {
		if err := cl.DeleteSession(context.Background(), bind(), id); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
	if called {
		t.Error("an unsafe id reached the network")
	}
}

func TestDeleteSessionDoesNotFollowRedirects(t *testing.T) {
	cl, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusTemporaryRedirect)
	}), ClientOptions{})
	if err := cl.DeleteSession(context.Background(), bind(), "sess_1"); err == nil {
		t.Error("a redirect was treated as success")
	}
}

func TestSessionDeleteAdvertisedNeedsTheExactRoute(t *testing.T) {
	serve := func(body string) *Client {
		cl, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }), ClientOptions{})
		return cl
	}
	doc := func(ep string) string {
		return `{"object":"hermes.api_server.capabilities","features":{},"endpoints":{"session_delete":` + ep + `}}`
	}
	for body, want := range map[string]bool{
		doc(`{"method":"DELETE","path":"/api/sessions/{session_id}"}`):   true,
		doc(`{"method":"GET","path":"/api/sessions/{session_id}"}`):      false,
		doc(`{"method":"DELETE","path":"/api/sessions/{session_id}/x"}`): false,
		doc(`{"method":"DELETE","path":"/api/sessions"}`):                false,
		`{"object":"other","endpoints":{}}`:                              false,
		`not json`:                                                       false,
	} {
		got, err := serve(body).SessionDeleteAdvertised(context.Background(), bind())
		if err != nil || got != want {
			t.Errorf("%s -> %v, %v; want %v", body, got, err, want)
		}
	}
}
