package chat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

const (
	maxBodyBytes = 64 << 10
	maxInput     = 32 << 10
)

var (
	idRE   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	idemRE = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
)

// Handlers returns the chat routes, keyed "METHOD /path" exactly as in
// auth.ChatRoutes. The auth layer wraps each in the Active guard (and the CSRF
// check for mutations); a handler can therefore rely on auth.UserFrom.
func (s *Service) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/chats":                    http.HandlerFunc(s.listChats),
		"POST /api/chats":                   http.HandlerFunc(s.createChat),
		"GET /api/chats/{chatId}":           http.HandlerFunc(s.getChat),
		"GET /api/chats/{chatId}/messages":  http.HandlerFunc(s.getMessages),
		"GET /api/chats/{chatId}/search":    http.HandlerFunc(s.searchOne),
		"GET /api/search/chats":             http.HandlerFunc(s.searchAll),
		"POST /api/chats/{chatId}/messages": http.HandlerFunc(s.postMessage),
		"GET /api/runs/{runId}":             http.HandlerFunc(s.getRun),
		"GET /api/runs/{runId}/events":      http.HandlerFunc(s.runEvents),
		"POST /api/runs/{runId}/cancel":     http.HandlerFunc(s.cancelRun),
	}
}

// ---- answers -------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code, msg string) {
	if code == hermes.CodeRateLimited {
		auth.WriteRateLimited(w, msg, 0)
		return
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg, "requestId": "req_" + hex.EncodeToString(b)}})
}

// upstreamError turns any failure into the fixed API vocabulary. The text of
// err is never used: it may carry a URL.
func upstreamError(w http.ResponseWriter, err error) {
	switch code := hermes.CodeOf(err); code {
	case hermes.CodeTenantNotReady:
		apiError(w, http.StatusConflict, code, "Your workspace is not ready")
	case hermes.CodeUpstreamTimeout:
		apiError(w, http.StatusGatewayTimeout, code, "The assistant did not answer in time")
	case hermes.CodeRateLimited:
		apiError(w, http.StatusTooManyRequests, code, "The assistant is busy; try again shortly")
	case hermes.CodeRunConflict:
		apiError(w, http.StatusConflict, code, "This chat already has a run in progress")
	case hermes.CodeCapabilityNA:
		apiError(w, http.StatusNotImplemented, code, "This is not available on the assistant")
	default:
		apiError(w, http.StatusServiceUnavailable, hermes.CodeHermesUnavailable, "The assistant is unavailable")
	}
}

func notFound(w http.ResponseWriter) { apiError(w, http.StatusNotFound, "NOT_FOUND", "Not found") }

func internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("chat: %s: %v", what, err) // store errors carry no credential or upstream URL
	apiError(w, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
}

// ---- views ----------------------------------------------------------------------

type chatView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	ActiveRunID string `json:"activeRunId,omitempty"`
}

func viewChat(c store.Chat) chatView {
	return chatView{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, ActiveRunID: c.ActiveRunID}
}

type runView struct {
	ID        string `json:"runId"`
	ChatID    string `json:"chatId"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func viewRun(r store.Run) runView {
	return runView{ID: r.ID, ChatID: r.ChatID, Status: r.Status, ErrorCode: r.ErrorCode, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// ---- helpers ----------------------------------------------------------------------

func user(r *http.Request) (string, bool) {
	u, ok := auth.UserFrom(r.Context())
	return u.ID, ok
}

// pathID returns a well-formed app id, or false. A malformed id is a 404, the
// same as an unknown one.
func pathID(r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	return id, idRE.MatchString(id)
}

// decode reads a small JSON body with unknown fields refused. A field the
// server alone may choose (profile, key, session id, …) is therefore a 400,
// never ignored and never forwarded.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !(errors.Is(err, io.EOF) && isEmptyOK(v)) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			apiError(w, http.StatusRequestEntityTooLarge, "INVALID_REQUEST", "Request is too large")
		} else {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid or carries a field the server does not accept")
		}
		return false
	}
	if dec.More() {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid")
		return false
	}
	return true
}

func isEmptyOK(v any) bool { _, ok := v.(*createChatBody); return ok }

// ---- chats ------------------------------------------------------------------------

func (s *Service) listChats(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	q, ok := query(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The query string is not valid")
		return
	}
	limit, ok := historyLimit(q)
	if !ok {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a positive number")
		return
	}
	var after *store.ChatCursor
	if c := q.Get("cursor"); c != "" {
		if after, ok = decodeChatCursor(c); !ok {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The cursor is not valid")
			return
		}
	}
	chats, more, err := store.ListChats(r.Context(), s.db, uid, limit, after)
	if err != nil {
		internalError(w, "list chats", err)
		return
	}
	out := make([]chatView, 0, len(chats))
	for _, c := range chats {
		out = append(out, viewChat(c))
	}
	next := ""
	if more && len(chats) > 0 {
		last := chats[len(chats)-1]
		next = encodeChatCursor(store.ChatCursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": out, "nextCursor": next})
}

type createChatBody struct {
	Title string `json:"title"`
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) > 120 {
		s = string([]rune(s)[:120])
	}
	return s
}

func (s *Service) createChat(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var body createChatBody
	if !decode(w, r, &body) {
		return
	}
	b, bindingID, err := s.gate(r.Context(), uid)
	if err != nil {
		s.gateError(w, err)
		return
	}
	sid, err := s.hx.CreateSession(r.Context(), b)
	if err != nil {
		upstreamError(w, err)
		return
	}
	c, err := store.CreateChat(r.Context(), s.db, uid, bindingID, sid, cleanTitle(body.Title), s.now())
	if err != nil {
		// The upstream session exists with no owner row; it is unreachable from
		// Antares and holds no content. Say so in the log, without the id's route.
		log.Printf("chat: upstream session created but not recorded: %v", err)
		apiError(w, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	writeJSON(w, http.StatusCreated, viewChat(c))
}

// gateError answers a gate failure: TENANT_NOT_READY, or a store fault.
func (s *Service) gateError(w http.ResponseWriter, err error) {
	var he *hermes.Error
	if errors.As(err, &he) {
		upstreamError(w, err)
		return
	}
	internalError(w, "gate", err)
}

func (s *Service) getChat(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	c, err := store.GetChat(r.Context(), s.db, uid, id)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "get chat", err)
	default:
		writeJSON(w, http.StatusOK, viewChat(c))
	}
}

// ---- runs -------------------------------------------------------------------------

type postMessageBody struct {
	Input string `json:"input"`
}

func (s *Service) postMessage(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if !idemRE.MatchString(idem) {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "An Idempotency-Key header of 8 to 128 letters, digits, - or _ is required")
		return
	}
	var body postMessageBody
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Input) == "" || len(body.Input) > maxInput || !utf8.ValidString(body.Input) {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The message is empty, too long or not valid text")
		return
	}
	// Ownership first: a foreign chat is a 404 before anything else is learned.
	chat, err := store.GetChat(r.Context(), s.db, uid, chatID)
	if errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get chat", err)
		return
	}
	if _, _, err := s.gate(r.Context(), uid); err != nil { // no run is admitted for an account that could not start it
		s.gateError(w, err)
		return
	}
	// A busy chat answers RUN_CONFLICT (or the replay of its own run) from AdmitRun;
	// the per-user cap only decides about a chat that is free.
	if chat.ActiveRunID == "" {
		if n, err := store.ActiveRunCount(r.Context(), s.db, uid); err != nil {
			internalError(w, "count runs", err)
			return
		} else if n >= maxActiveRunsPerUser {
			apiError(w, http.StatusTooManyRequests, hermes.CodeRateLimited, "Too many runs in progress; retry after a run finishes")
			return
		}
	}
	run, created, err := store.AdmitRun(r.Context(), s.db, uid, chatID, idem, s.now())
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
		return
	case errors.Is(err, store.ErrRunActive):
		apiError(w, http.StatusConflict, hermes.CodeRunConflict, "This chat already has a run in progress")
		return
	case err != nil:
		internalError(w, "admit run", err)
		return
	}
	if created {
		s.start(run, body.Input)
	}
	// A repeat of the same Idempotency-Key is the same run: nothing is sent again.
	writeJSON(w, http.StatusAccepted, viewRun(run))
}

func (s *Service) getRun(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "runId")
	if !ok {
		notFound(w)
		return
	}
	run, err := store.GetRun(r.Context(), s.db, uid, id)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "get run", err)
	default:
		writeJSON(w, http.StatusOK, viewRun(run))
	}
}

func (s *Service) cancelRun(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "runId")
	if !ok {
		notFound(w)
		return
	}
	run, stopped, err := s.Cancel(r.Context(), uid, id)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "cancel run", err)
	default:
		v := struct {
			runView
			UpstreamStopped bool `json:"upstreamStopped"`
		}{viewRun(run), stopped}
		writeJSON(w, http.StatusOK, v)
	}
}

// ---- events (SSE) -------------------------------------------------------------------

func (s *Service) runEvents(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "runId")
	if !ok {
		notFound(w)
		return
	}
	run, err := store.GetRun(r.Context(), s.db, uid, id)
	if errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get run", err)
		return
	}
	l, err := s.attach(r.Context(), run)
	if err != nil {
		s.gateError(w, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, "INTERNAL", "Streaming is not supported here")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "retry: 3000\n\n")
	fl.Flush()

	last := l.parseEventID(r.Header.Get("Last-Event-ID"))
	tick := time.NewTicker(s.heartbeat)
	defer tick.Stop()
	for {
		evs, lost, done, wait := l.since(last)
		if lost {
			writeEvent(w, "", "run.resumed", []byte(`{"replace":true}`))
		}
		for _, e := range evs {
			writeEvent(w, l.eventID(e.seq), e.name, e.data)
			last = e.seq
		}
		fl.Flush()
		if done {
			return
		}
		select {
		case <-wait:
		case <-tick.C:
			// Re-check the account while a stream is open: a suspension must end it.
			if _, err := store.ActiveBinding(r.Context(), s.db, uid); err != nil {
				writeEvent(w, "", "run.disconnected", []byte(`{"code":"TENANT_NOT_READY"}`))
				fl.Flush()
				return
			}
			_, _ = io.WriteString(w, ": keep-alive\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-s.base.Done():
			return
		}
	}
}

func writeEvent(w io.Writer, id, name string, data []byte) {
	var b strings.Builder
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	b.WriteString("event: " + name + "\n")
	b.WriteString("data: " + string(data) + "\n\n") // marshalled JSON never contains a raw newline
	_, _ = io.WriteString(w, b.String())
}
