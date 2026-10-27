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
	chatGet, chatPut, chatDel := s.chatDraftAPI().handlers(s)
	newGet, newPut, newDel := s.newChatDraftAPI().handlers(s)
	return map[string]http.Handler{
		"GET /api/chats/{chatId}/draft":                                chatGet,
		"PUT /api/chats/{chatId}/draft":                                chatPut,
		"DELETE /api/chats/{chatId}/draft":                             chatDel,
		"GET /api/me/new-chat-draft":                                   newGet,
		"PUT /api/me/new-chat-draft":                                   newPut,
		"DELETE /api/me/new-chat-draft":                                newDel,
		"GET /api/me/templates":                                        http.HandlerFunc(s.listTemplates),
		"POST /api/me/templates":                                       http.HandlerFunc(s.createTemplate),
		"PUT /api/me/templates/{templateId}":                           http.HandlerFunc(s.putTemplate),
		"DELETE /api/me/templates/{templateId}":                        http.HandlerFunc(s.deleteTemplate),
		"GET /api/chats":                                               http.HandlerFunc(s.listChats),
		"POST /api/chats":                                              http.HandlerFunc(s.createChat),
		"GET /api/chats/{chatId}":                                      http.HandlerFunc(s.getChat),
		"PATCH /api/chats/{chatId}":                                    http.HandlerFunc(s.patchChat),
		"DELETE /api/chats/{chatId}":                                   http.HandlerFunc(s.deleteChat),
		"GET /api/chats/{chatId}/messages":                             http.HandlerFunc(s.getMessages),
		"GET /api/chats/{chatId}/messages/{seq}/artifacts/{n}":         http.HandlerFunc(s.downloadArtifact),
		"GET /api/chats/{chatId}/messages/{seq}/artifacts/{n}/preview": http.HandlerFunc(s.previewArtifact),
		"GET /api/chats/{chatId}/search":                               http.HandlerFunc(s.searchOne),
		"GET /api/chats/{chatId}/usage":                                http.HandlerFunc(s.chatUsage),
		"GET /api/models":                                              http.HandlerFunc(s.listModels),
		"PUT /api/chats/{chatId}/model":                                http.HandlerFunc(s.putChatModel),
		"GET /api/chats/{chatId}/settings":                             http.HandlerFunc(s.getChatSettings),
		"PUT /api/chats/{chatId}/settings":                             http.HandlerFunc(s.putChatModel),
		"GET /api/me/model-visibility":                                 http.HandlerFunc(s.getModelVisibility),
		"PUT /api/me/model-visibility":                                 http.HandlerFunc(s.putModelVisibility),
		"GET /api/me/budget":                                           http.HandlerFunc(s.getBudget),
		"PUT /api/me/budget":                                           http.HandlerFunc(s.putBudget),
		"GET /api/search/chats":                                        http.HandlerFunc(s.searchAll),
		"GET /api/capabilities":                                        http.HandlerFunc(s.capabilities),
		"GET /api/chats/{chatId}/input":                                http.HandlerFunc(s.getChatInput),
		"POST /api/chats/{chatId}/messages":                            http.HandlerFunc(s.postMessage),
		"GET /api/chats/{chatId}/queue":                                http.HandlerFunc(s.getQueue),
		"POST /api/chats/{chatId}/queue":                               http.HandlerFunc(s.enqueue),
		"PATCH /api/chats/{chatId}/queue":                              http.HandlerFunc(s.pauseQueue),
		"POST /api/chats/{chatId}/queue/next":                          http.HandlerFunc(s.sendNext),
		"PATCH /api/chats/{chatId}/queue/{itemId}":                     http.HandlerFunc(s.editQueued),
		"DELETE /api/chats/{chatId}/queue/{itemId}":                    http.HandlerFunc(s.removeQueued),
		"GET /api/runs/{runId}":                                        http.HandlerFunc(s.getRun),
		"GET /api/runs/{runId}/events":                                 http.HandlerFunc(s.runEvents),
		"POST /api/runs/{runId}/cancel":                                http.HandlerFunc(s.cancelRun),
		"POST /api/runs/{runId}/steer":                                 http.HandlerFunc(s.steerRun),
		"POST /api/runs/{runId}/approve":                               http.HandlerFunc(s.approveRun),
		"POST /api/runs/{runId}/clarify":                               http.HandlerFunc(s.clarifyRun),

		"POST /api/assets":                    http.HandlerFunc(s.uploadAsset),
		"GET /api/assets/{assetId}":           http.HandlerFunc(s.getAsset),
		"DELETE /api/assets/{assetId}":        http.HandlerFunc(s.deleteAsset),
		"POST /api/chats/{chatId}/regenerate": http.HandlerFunc(s.noTurnMutation("regenerate")),
		"POST /api/chats/{chatId}/edit-turn":  http.HandlerFunc(s.noTurnMutation("edit-turn")),
		"POST /api/chats/{chatId}/rewind":     http.HandlerFunc(s.noTurnMutation("rewind")),
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
		if status != http.StatusTooManyRequests {
			panic("chat: RATE_LIMITED requires HTTP 429")
		}
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
	case hermes.CodeSteerRefused:
		apiError(w, http.StatusConflict, code, "The run is not accepting guidance (it may have just finished)")
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
	// Model is the chat's saved choice; null means the profile's default.
	Model       *modelChoiceView `json:"model"`
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
	ActiveRunID string           `json:"activeRunId,omitempty"`
	Pinned      bool             `json:"pinned"`
	Archived    bool             `json:"archived"`
	// DeleteFailed marks a chat whose delete did not complete; it still exists and can be retried.
	DeleteFailed bool `json:"deleteFailed,omitempty"`
}

func viewChat(c store.Chat) chatView {
	return chatView{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, ActiveRunID: c.ActiveRunID,
		Pinned: c.PinnedAt != "", Archived: c.Status == "ARCHIVED", DeleteFailed: c.DeleteFailed}
}

type runView struct {
	ID        string `json:"runId"`
	ChatID    string `json:"chatId"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	// Prompts are the decisions Hermes is waiting for on this run, read from the database: the run
	// cannot proceed past them, and a reload shows them again. Only getRun fills it.
	Prompts []promptView `json:"prompts,omitempty"`
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
	return decodeMax(w, r, v, maxBodyBytes)
}

// decodeMax is decode with its own body cap, for a route whose legitimate body can be larger than a chat message.
func decodeMax(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
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
	chats, more, err := store.ListChats(r.Context(), s.db, uid, limit, after, q.Get("archived") == "1")
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
		next = encodeChatCursor(store.ChatCursor{Pinned: last.PinnedAt != "", UpdatedAt: last.UpdatedAt, ID: last.ID})
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
		v := viewChat(c)
		m, err := store.GetChatModel(r.Context(), s.db, uid, id)
		if err != nil {
			internalError(w, "get chat model", err)
			return
		}
		v.Model = choiceView(m)
		writeJSON(w, http.StatusOK, v)
	}
}

// ---- runs -------------------------------------------------------------------------

type postMessageBody struct {
	Input string `json:"input"`
	// ImageAssetIDs name the caller's own staged images (POST /api/assets) to send with the text.
	ImageAssetIDs []string `json:"imageAssetIds"`
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
	if run, ok := s.admit(w, r, uid, chatID, idem, body.Input, body.ImageAssetIDs); ok {
		// A repeat of the same Idempotency-Key is the same run: nothing is sent again.
		writeJSON(w, http.StatusAccepted, viewRun(run))
	}
}

// admit runs every check a new turn needs (ownership, gate, model policy, images, per-user run cap,
// budget, a free chat) and starts the run. A refusal is written to w and ok is false. A typed message
// and a queued one (queue.go) both come through here, so a queued message is re-checked at dispatch.
func (s *Service) admit(w http.ResponseWriter, r *http.Request, uid, chatID, idem, input string, imageIDs []string) (store.Run, bool) {
	// Ownership first: a foreign chat is a 404 before anything else is learned.
	chat, err := store.GetChat(r.Context(), s.db, uid, chatID)
	if errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return store.Run{}, false
	} else if err != nil {
		internalError(w, "get chat", err)
		return store.Run{}, false
	}
	b, _, err := s.gate(r.Context(), uid) // no run is admitted for an account that could not start it
	if err != nil {
		s.gateError(w, err)
		return store.Run{}, false
	}
	if s.refuseUnrunnableChoice(w, r, b, uid, chatID) {
		return store.Run{}, false
	}
	imgs, ok := s.resolveImages(w, r, b, uid, chatID, imageIDs)
	if !ok {
		return store.Run{}, false
	}
	// A busy chat answers RUN_CONFLICT (or the replay of its own run) from AdmitRun;
	// the per-user cap only decides about a chat that is free.
	if chat.ActiveRunID == "" {
		if n, err := store.ActiveRunCount(r.Context(), s.db, uid); err != nil {
			internalError(w, "count runs", err)
			return store.Run{}, false
		} else if n >= maxActiveRunsPerUser {
			apiError(w, http.StatusTooManyRequests, hermes.CodeRateLimited, "Too many runs in progress; retry after a run finishes")
			return store.Run{}, false
		}
	}
	// Budget: a free chat at or over its limit takes no new message (a reply
	// already running is not interrupted). A busy chat answers RUN_CONFLICT as
	// before, and AdmitRunWithin hands a replayed key its original run.
	var budget int64
	if chat.ActiveRunID == "" {
		if budget, err = store.TokenBudget(r.Context(), s.db, uid); err != nil {
			internalError(w, "token budget", err)
			return store.Run{}, false
		}
	}
	run, created, err := store.AdmitRunWithin(r.Context(), s.db, uid, chatID, idem, s.now(), budget)
	switch {
	case errors.Is(err, store.ErrBudget):
		apiError(w, http.StatusForbidden, "BUDGET_REACHED", "This conversation has reached the token limit you set; raise or remove it to continue")
		return store.Run{}, false
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
		return store.Run{}, false
	case errors.Is(err, store.ErrRunActive):
		apiError(w, http.StatusConflict, hermes.CodeRunConflict, "This chat already has a run in progress")
		return store.Run{}, false
	case err != nil:
		internalError(w, "admit run", err)
		return store.Run{}, false
	}
	if created {
		if imgs != nil {
			if err := store.AttachRunImages(r.Context(), s.db, uid, run.ID, turnDigest(uid, run.ID, input), imgs.assets()); err != nil {
				log.Printf("chat: run %s: could not record its images: %v", run.ID, err)
			}
		}
		s.start(run, input, imgs)
	}
	return run, true
}

func (s *Service) getRun(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "runId")
	if !ok {
		notFound(w)
		return
	}
	run, err := store.GetRun(r.Context(), s.db, uid, id)
	var open []store.Prompt
	if err == nil {
		open, err = store.OpenPrompts(r.Context(), s.db, uid, id)
	}
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "get run", err)
	default:
		v := viewRun(run)
		for _, p := range open {
			v.Prompts = append(v.Prompts, viewPrompt(p))
		}
		writeJSON(w, http.StatusOK, v)
	}
}

type approveBody struct {
	PromptID string `json:"promptId"`
	Choice   string `json:"choice"`
}

type clarifyBody struct {
	PromptID string `json:"promptId"`
	Answer   string `json:"answer"`
}

// approveRun and clarifyRun answer a prompt on the caller's own run (PRD §9A.7). The body names the prompt
// by Antares' own id; the upstream request id and generation are looked up from the stored prompt and never
// read from the request. A run, prompt or user that does not match is 404, indistinguishable from a
// missing one.
func (s *Service) approveRun(w http.ResponseWriter, r *http.Request) {
	var body approveBody
	s.answerPrompt(w, r, &body, store.PromptApproval, func() (string, string) { return body.PromptID, body.Choice })
}

func (s *Service) clarifyRun(w http.ResponseWriter, r *http.Request) {
	var body clarifyBody
	s.answerPrompt(w, r, &body, store.PromptClarify, func() (string, string) { return body.PromptID, body.Answer })
}

func (s *Service) answerPrompt(w http.ResponseWriter, r *http.Request, body any, kind string, fields func() (promptID, value string)) {
	uid, _ := user(r)
	id, ok := pathID(r, "runId")
	if !ok {
		notFound(w)
		return
	}
	// Ownership before anything about the body is judged, so a foreign run answers like a missing one.
	if _, err := store.GetRun(r.Context(), s.db, uid, id); errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get run", err)
		return
	}
	if !decode(w, r, body) {
		return
	}
	promptID, value := fields()
	if !idRE.MatchString(promptID) || !utf8.ValidString(value) {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The prompt or the answer is not valid")
		return
	}
	err := s.Respond(r.Context(), uid, id, promptID, kind, value)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case errors.Is(err, ErrPromptStale):
		apiError(w, http.StatusConflict, hermes.CodePromptStale, "That question is no longer waiting for an answer")
	case errors.Is(err, ErrBadChoice):
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "That is not an answer this prompt accepts")
	case err != nil:
		s.gateError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"runId": id, "promptId": promptID, "accepted": true})
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

type steerBody struct {
	Input string `json:"input"`
}

// steerRun sends guidance into the caller's own running run (PRD §21.5; Hermes
// POST /v1/runs/{id}/steer). 404 for a run that is not the caller's, 409 when it
// is over or Hermes declines. A user-supplied profile, key or session never
// reaches this far: decode rejects unknown fields.
func (s *Service) steerRun(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "runId")
	if !ok {
		notFound(w)
		return
	}
	var body steerBody
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Input) == "" || len(body.Input) > maxInput || !utf8.ValidString(body.Input) {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The guidance is empty, too long or not valid text")
		return
	}
	err := s.Steer(r.Context(), uid, id, body.Input)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case errors.Is(err, ErrNotSteerable):
		apiError(w, http.StatusConflict, hermes.CodeSteerRefused, "The run is not accepting guidance (it may have just finished)")
	case err != nil:
		s.gateError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"runId": id, "accepted": true})
	}
}

// noTurnMutation answers regenerate, edit-turn and rewind. The deployed Hermes has
// no route that retries, edits or truncates a turn (its fork copies the whole
// session, docs/runbooks/turn-controls.md), and Antares will not emulate one by
// writing rows or touching Hermes' store. Ownership is still checked first, so a
// foreign chat is indistinguishable from a missing one; an owned chat gets an
// honest 501. Nothing is sent to Hermes and no run, stream or row is touched.
func (s *Service) noTurnMutation(op string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _ := user(r)
		id, ok := pathID(r, "chatId")
		if !ok {
			notFound(w)
			return
		}
		if _, err := store.GetChat(r.Context(), s.db, uid, id); errors.Is(err, store.ErrNoChat) {
			notFound(w)
			return
		} else if err != nil {
			internalError(w, "get chat", err)
			return
		}
		apiError(w, http.StatusNotImplemented, hermes.CodeCapabilityNA, "Hermes has no way to "+op+" a turn; start a new chat instead")
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
	// #150: register, then re-check the session once. A sign-out before this point is in the database; one after it
	// marks this stream, and the mark is taken under the same lock the batch is written under.
	if s.streamOpenHook != nil {
		s.streamOpenHook()
	}
	st, unregister := s.registerStream(uid, auth.SessionHash(r))
	defer unregister()
	if !auth.SessionLive(r.Context(), s.db, r, s.now()) {
		writeEvent(w, "", "run.disconnected", []byte(`{"code":"UNAUTHENTICATED"}`))
		fl.Flush()
		return
	}
	for {
		evs, lost, done, wait := l.since(last)
		st.mu.Lock()
		if st.revoked {
			writeEvent(w, "", "run.disconnected", []byte(`{"code":"UNAUTHENTICATED"}`))
			fl.Flush()
			st.mu.Unlock()
			return
		}
		if lost {
			writeEvent(w, "", "run.resumed", []byte(`{"replace":true}`))
		}
		for _, e := range evs {
			writeEvent(w, l.eventID(e.seq), e.name, e.data)
			last = e.seq
		}
		fl.Flush()
		st.mu.Unlock()
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
			// And the session: a sign-out (here or everywhere) or a revocation must end it too (#150).
			if !auth.SessionLive(r.Context(), s.db, r, s.now()) {
				writeEvent(w, "", "run.disconnected", []byte(`{"code":"UNAUTHENTICATED"}`))
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
