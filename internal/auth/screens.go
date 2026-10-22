package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/http"
)

// The screens are server-rendered HTML with no script: a user who is not
// eligible gets a page that says what happened and what to do, and there is no
// bundle for a chat box to hide in. Every dynamic value goes through
// html/template's contextual escaping.

type page struct {
	state string // account state this page belongs to ("" for login/error)
	path  string
}

var (
	loginPage     = page{"", "/login"}
	pendingPage   = page{StatePending, "/pending"}
	rejectedPage  = page{StateRejected, "/rejected"}
	suspendedPage = page{StateSuspended, "/suspended"}
	setupPage     = page{StateSetup, "/setup"}
	errorPage     = page{StateError, "/error"}
)

var screens = map[string]page{
	"/login": loginPage, "/pending": pendingPage, "/rejected": rejectedPage,
	"/suspended": suspendedPage, "/setup": setupPage, "/error": errorPage,
}

// pathFor is where a user in state st belongs.
func pathFor(st string) string {
	switch st {
	case StatePending:
		return pendingPage.path
	case StateRejected:
		return rejectedPage.path
	case StateSuspended:
		return suspendedPage.path
	case StateSetup:
		return setupPage.path
	case StateActive:
		return "/"
	}
	return errorPage.path
}

const css = `body{font:16px/1.5 system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#f6f6f4;color:#1c1c1a}
main{max-width:30rem;margin:1rem;padding:2rem;background:#fff;border:1px solid #d8d8d2;border-radius:.5rem}
h1{font-size:1.4rem;margin:0 0 .75rem}dl{margin:1rem 0}dt{font-size:.8rem;color:#55554f}dd{margin:0 0 .5rem}
a.btn,button{display:inline-block;font:inherit;padding:.5rem 1rem;margin:.25rem .5rem .25rem 0;border:1px solid #1c1c1a;border-radius:.3rem;background:#1c1c1a;color:#fff;text-decoration:none;cursor:pointer}
button.quiet,a.quiet{background:#fff;color:#1c1c1a}form{display:inline}small{color:#55554f}
@media (prefers-color-scheme:dark){body{background:#151513;color:#ecece6}main{background:#1f1f1c;border-color:#3a3a35}dt,small{color:#aaa9a0}a.btn,button{background:#ecece6;color:#151513;border-color:#ecece6}button.quiet,a.quiet{background:#1f1f1c;color:#ecece6}}`

// cspStyleHash lets the one inline <style> run under a CSP with no 'unsafe-inline'.
var cspStyleHash = func() string {
	sum := sha256.Sum256([]byte(css))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

type pageData struct {
	Title, Heading, Message, Next string
	Email, Name, Requested        string
	Signed                        bool
	Login                         bool
	Refresh                       string
}

var tmpl = template.Must(template.New("screen").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title><style>` + css + `</style></head>
<body><main>
<h1>{{.Heading}}</h1>
<p>{{.Message}}</p>
{{if .Signed}}<dl><dt>Signed in as</dt><dd>{{if .Name}}{{.Name}} · {{end}}{{.Email}}</dd>
{{if .Requested}}<dt>Requested</dt><dd>{{.Requested}}</dd>{{end}}</dl>{{end}}
{{if .Next}}<p>{{.Next}}</p>{{end}}
{{if .Login}}<a class="btn" href="/api/auth/google/start">Sign in with Google</a>
<p><small>Antares uses Google only to confirm who you are. It receives your name, email address and picture. There is no password.</small></p>{{end}}
{{if .Refresh}}<a class="btn quiet" href="{{.Refresh}}">Refresh</a>{{end}}
{{if .Signed}}<form method="post" action="/api/auth/logout"><button class="quiet" type="submit">Sign out</button></form>{{end}}
</main></body></html>`))

func render(w http.ResponseWriter, status int, d pageData) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src "+cspStyleHash+"; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, d)
}

// screen serves one status page. A user whose state does not match the page is
// sent to the one that does, so a stale tab never shows the wrong promise.
func (a *Auth) screen(p page) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, signed := a.current(r)
		if signed {
			if st := StateOf(u); st != p.state {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, pathFor(st), http.StatusSeeOther)
				return
			}
		} else if p != loginPage && p != errorPage {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, loginPage.path, http.StatusSeeOther)
			return
		}
		d := pageData{Signed: signed}
		if signed {
			d.Email, d.Name, d.Requested = u.Email, u.DisplayName, u.CreatedAt
		}
		switch p {
		case loginPage:
			d.Title, d.Heading, d.Login = "Sign in", "Sign in to Antares", true
			d.Message = "Access is by invitation: after you sign in, an administrator has to approve your account before you can chat."
		case pendingPage:
			d.Title, d.Heading, d.Refresh = "Waiting for approval", "Waiting for administrator approval", p.path
			d.Message = "Your account has been created, but nobody has approved it yet, so chat is not available."
			d.Next = "You do not need to do anything else. Ask the person who runs this service to approve you, then refresh this page."
		case rejectedPage:
			d.Title, d.Heading = "Access not approved", "Your access request was not approved"
			d.Message = "An administrator reviewed your account and did not approve it. Chat is not available."
			d.Next = "If you think this is a mistake, contact the person who runs this service. You can sign out and use a different Google account."
		case suspendedPage:
			d.Title, d.Heading = "Access disabled", "Your access is disabled"
			d.Message = "An administrator has suspended this account. Chat is not available and no conversations are shown."
			d.Next = "Contact the person who runs this service to ask for access to be restored."
		case setupPage:
			d.Title, d.Heading, d.Refresh = "Setting up", "Your account is approved — your workspace is being prepared", p.path
			d.Message = "Chat will open as soon as your private workspace is ready. Until then it is not available."
			d.Next = "This usually does not need anything from you. Refresh in a few minutes; if it stays like this, contact the person who runs this service."
		case errorPage:
			d.Title, d.Heading = "Something went wrong", "Something went wrong"
			switch {
			case signed:
				d.Message = "Your account is approved, but its workspace is not working, so chat is not available."
				d.Next = "This needs an administrator. Contact the person who runs this service."
			case r.URL.Query().Get("code") == "login_failed":
				d.Heading = "Sign-in did not complete"
				d.Message = "Google sign-in could not be verified, so you have not been signed in. Nothing was changed on your account."
				d.Next = "Go back and try signing in again."
			default:
				d.Message = "The page you asked for could not be shown."
			}
			if !signed {
				d.Login = true
			}
		}
		render(w, http.StatusOK, d)
	}
}

// Landing guards "/": the signed-out go to login, the not-yet-eligible to their
// status screen, and only an ACTIVE user reaches the app shell.
func (a *Auth) Landing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.current(r)
		dest := loginPage.path
		if ok {
			if StateOf(u) == StateActive {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
				return
			}
			dest = pathFor(StateOf(u))
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, dest, http.StatusSeeOther)
	})
}
