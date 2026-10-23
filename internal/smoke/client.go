package smoke

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SessionCookie is the name of Antares' session cookie. Its value is a bearer
// credential for the account.
const SessionCookie = "__Host-antares_session"

const maxBody = 4 << 20

// Session is one signed-in account as the smoke test sees it.
type Session struct {
	Name   string // "admin", "A", "B", "pending": for messages only
	Cookie string // value of SessionCookie
}

// Client talks to a deployed Antares the way a browser does: cookie in, JSON out.
// It never sends an Authorization header (Antares refuses one) and never follows
// a redirect, so a login bounce is visible instead of silently landing on a page.
type Client struct {
	Base   string // e.g. https://antares.example.com, no trailing slash
	Origin string // Origin header on mutations; must equal ANTARES_PUBLIC_ORIGIN
	HTTP   *http.Client
	Red    *Redactor
}

// NewClient builds a Client with a per-request timeout.
func NewClient(base, origin string, red *Redactor) *Client {
	base = strings.TrimRight(base, "/")
	if origin == "" {
		origin = base
	}
	return &Client{Base: base, Origin: strings.TrimRight(origin, "/"), Red: red, HTTP: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Response is one answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into v.
func (r *Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// ErrCode is the API error code ("ACCOUNT_PENDING", "NOT_FOUND", ...), or "".
func (r *Response) ErrCode() string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(r.Body, &e) != nil {
		return ""
	}
	return e.Error.Code
}

// Brief describes the response for a failure message: status and error code,
// never the body (a body can echo anything).
func (r *Response) Brief() string {
	if c := r.ErrCode(); c != "" {
		return fmt.Sprintf("HTTP %d %s", r.Status, c)
	}
	return fmt.Sprintf("HTTP %d", r.Status)
}

func (c *Client) request(ctx context.Context, s *Session, method, path string, hdr map[string]string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, errors.New("bad request: " + c.Red.Clean(err.Error()))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", c.Origin) // Antares' same-origin check for cookie mutations
	}
	if s != nil && s.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: s.Cookie})
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Do performs one request as s (nil for anonymous) and reads the whole answer.
func (c *Client) Do(ctx context.Context, s *Session, method, path string, hdr map[string]string, body any) (*Response, error) {
	req, err := c.request(ctx, s, method, path, hdr, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %s", method, path, c.Red.Clean(unwrap(err)))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		return nil, fmt.Errorf("%s %s: response unreadable or over %d bytes", method, path, maxBody)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// unwrap gives the cause of a transport error without the URL wrapper.
func unwrap(err error) string {
	var ue interface{ Unwrap() error }
	for errors.As(err, &ue) {
		inner := ue.Unwrap()
		if inner == nil {
			break
		}
		err = inner
	}
	return err.Error()
}

// SSEEvent is one server-sent event.
type SSEEvent struct{ Name, Data string }

// Stream opens GET path as an event stream and calls fn for every event until
// fn returns stop=true, the stream ends, or ctx is done. It returns the HTTP
// status when the stream was refused (not 200) and no events were read.
func (c *Client) Stream(ctx context.Context, s *Session, path string, fn func(SSEEvent) (stop bool)) (status int, err error) {
	req, err := c.request(ctx, s, http.MethodGet, path, map[string]string{"Accept": "text/event-stream"}, nil)
	if err != nil {
		return 0, err
	}
	streaming := *c.HTTP
	streaming.Timeout = 0 // the caller's context bounds a stream
	resp, err := streaming.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %s", path, c.Red.Clean(unwrap(err)))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var ev SSEEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Name != "" || ev.Data != "" {
				if fn(ev) {
					return resp.StatusCode, nil
				}
			}
			ev = SSEEvent{}
		case strings.HasPrefix(line, "event:"):
			ev.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			ev.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return resp.StatusCode, fmt.Errorf("event stream broke: %s", c.Red.Clean(err.Error()))
	}
	return resp.StatusCode, nil
}
