package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Per-chat message queue (PRD §27.4 QUEUE-001..004, §29.1, issue #110). A queued message is a normal new
// turn sent after the chat's run has ended; it is never steer (steer is guidance into the running run).
// The server is authoritative: the queue lives in SQLite, and a message leaves it only through
// dispatchNext, which claims it with a compare-and-set and admits it through the same checks as a typed
// message, under the stable key "queue-<id>".

const queueDispatchTimeout = 30 * time.Second

type queueItemView struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Input    string `json:"input"`
	Status   string `json:"status"`
}

type queueView struct {
	Items []queueItemView `json:"items"`
	// Paused is why automatic sending is off ("" when it is on): USER, or what stopped it.
	Paused string `json:"paused"`
}

func viewQueue(q store.Queue) queueView {
	v := queueView{Items: make([]queueItemView, 0, len(q.Items)), Paused: q.Paused}
	for _, it := range q.Items {
		v.Items = append(v.Items, queueItemView{ID: it.ID, Position: it.Ordinal, Input: it.Input, Status: it.Status})
	}
	return v
}

type queueInputBody struct {
	Input string `json:"input"`
}

type queuePauseBody struct {
	Paused *bool `json:"paused"`
}

func validInput(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= maxInput && utf8.ValidString(s)
}

// ownChat answers 404 unless chatID is the caller's own live chat. It runs before a body is read, so a
// foreign chat is refused the same way whatever the request carries.
func (s *Service) ownChat(w http.ResponseWriter, r *http.Request, uid, chatID string) bool {
	_, err := store.GetChat(r.Context(), s.db, uid, chatID)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "get chat", err)
	default:
		return true
	}
	return false
}

// writeQueue answers with the chat's queue as stored now.
func (s *Service) writeQueue(w http.ResponseWriter, r *http.Request, uid, chatID string, status int) {
	q, err := store.GetQueue(r.Context(), s.db, uid, chatID)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "get queue", err)
	default:
		writeJSON(w, status, viewQueue(q))
	}
}

func (s *Service) getQueue(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	s.writeQueue(w, r, uid, chatID, http.StatusOK)
}

// enqueue adds a message to the end of the chat's queue. If the chat is free and its queue is not
// paused, the head is sent at once, so a message queued just as the run ended is not left waiting.
func (s *Service) enqueue(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	if !s.ownChat(w, r, uid, chatID) {
		return
	}
	var body queueInputBody
	if !decode(w, r, &body) {
		return
	}
	if !validInput(body.Input) {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The message is empty, too long or not valid text")
		return
	}
	_, err := store.Enqueue(r.Context(), s.db, uid, chatID, body.Input, s.now())
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
		return
	case errors.Is(err, store.ErrQueueFull):
		apiError(w, http.StatusConflict, "QUEUE_FULL", "This chat's queue is full; send or remove a queued message first")
		return
	case err != nil:
		internalError(w, "enqueue", err)
		return
	}
	s.dispatchIfFree(r.Context(), uid, chatID)
	s.writeQueue(w, r, uid, chatID, http.StatusCreated)
}

func (s *Service) editQueued(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok1 := pathID(r, "chatId")
	itemID, ok2 := pathID(r, "itemId")
	if !ok1 || !ok2 {
		notFound(w)
		return
	}
	if !s.ownChat(w, r, uid, chatID) {
		return
	}
	var body queueInputBody
	if !decode(w, r, &body) {
		return
	}
	if !validInput(body.Input) {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The message is empty, too long or not valid text")
		return
	}
	s.answerItem(w, r, uid, chatID, store.EditQueued(r.Context(), s.db, uid, chatID, itemID, body.Input, s.now()))
}

func (s *Service) removeQueued(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok1 := pathID(r, "chatId")
	itemID, ok2 := pathID(r, "itemId")
	if !ok1 || !ok2 {
		notFound(w)
		return
	}
	s.answerItem(w, r, uid, chatID, store.RemoveQueued(r.Context(), s.db, uid, chatID, itemID))
}

// answerItem answers an edit or remove. An item that is not the caller's, or is already being sent, is a 404.
func (s *Service) answerItem(w http.ResponseWriter, r *http.Request, uid, chatID string, err error) {
	switch {
	case errors.Is(err, store.ErrNoItem), errors.Is(err, store.ErrNoChat):
		notFound(w)
	case err != nil:
		internalError(w, "queue item", err)
	default:
		s.writeQueue(w, r, uid, chatID, http.StatusOK)
	}
}

// pauseQueue turns automatic sending off or back on. Resuming is the owner's explicit recovery after a
// failure, so a free chat sends its head at once.
func (s *Service) pauseQueue(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	if !s.ownChat(w, r, uid, chatID) {
		return
	}
	var body queuePauseBody
	if !decode(w, r, &body) {
		return
	}
	if body.Paused == nil {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "paused is required")
		return
	}
	reason := ""
	if *body.Paused {
		reason = "USER"
	}
	if err := store.SetQueuePaused(r.Context(), s.db, uid, chatID, reason); errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "pause queue", err)
		return
	}
	if !*body.Paused {
		s.dispatchIfFree(r.Context(), uid, chatID)
	}
	s.writeQueue(w, r, uid, chatID, http.StatusOK)
}

// sendNext is the owner's "Send next": it sends the head even from a paused queue, and only once the
// chat's run has ended (a busy chat is RUN_CONFLICT). The queue stays paused if it was.
func (s *Service) sendNext(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	run, rf, err := s.dispatchNext(r.Context(), uid, chatID, true)
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
	case errors.Is(err, store.ErrNoItem):
		apiError(w, http.StatusConflict, "QUEUE_EMPTY", "Nothing is queued in this chat")
	case errors.Is(err, store.ErrQueueBusy):
		apiError(w, http.StatusConflict, hermes.CodeRunConflict, "A queued message is already being sent")
	case err != nil:
		internalError(w, "send next", err)
	case rf != nil:
		rf.replay(w)
	default:
		writeJSON(w, http.StatusAccepted, viewRun(run))
	}
}

// dispatchIfFree sends the head of an unpaused queue when the chat has no run. A busy chat, an empty or
// paused queue, or a dispatch already in flight is not an error: the run's end tries again.
func (s *Service) dispatchIfFree(ctx context.Context, uid, chatID string) {
	if _, _, err := s.dispatchNext(ctx, uid, chatID, false); err != nil && !errors.Is(err, store.ErrNoItem) && !errors.Is(err, store.ErrQueueBusy) && !errors.Is(err, store.ErrNoChat) {
		log.Printf("chat: queue dispatch for chat %s: %v", chatID, err)
	}
}

// dispatchNext claims the head of the queue (QUEUED -> SUBMITTING, exactly once) and admits it as a new
// turn under its stable key. Admitted, the item leaves the queue; refused, it goes back to the head and
// the queue is paused with the refusal's code, except for a busy chat, whose run's end dispatches again.
// A refusal is returned as rf for the caller to answer with; err is a claim or store failure.
func (s *Service) dispatchNext(ctx context.Context, uid, chatID string, explicit bool) (run store.Run, rf *refusal, err error) {
	item, err := store.ClaimNext(ctx, s.db, uid, chatID, explicit, s.now())
	if err != nil {
		return store.Run{}, nil, err
	}
	rf = &refusal{}
	r := (&http.Request{Header: http.Header{}}).WithContext(ctx)
	run, ok := s.admit(rf, r, uid, chatID, item.IdempotencyKey(), item.Input, nil)
	// The settle runs even if ctx ended: a claim must not stay SUBMITTING behind a dropped request.
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := store.SettleClaim(settle, s.db, uid, item.ID, ok, s.now()); err != nil {
		log.Printf("chat: queue item %s: could not settle its claim: %v", item.ID, err)
	}
	if ok {
		return run, nil, nil
	}
	if code := rf.code(); code != hermes.CodeRunConflict {
		if err := store.PauseQueueIfWaiting(settle, s.db, uid, chatID, code); err != nil {
			log.Printf("chat: queue for chat %s: could not pause: %v", chatID, err)
		}
	}
	return store.Run{}, rf, nil
}

// afterRun is told the stored status of a run that has stopped, before its end is announced. A completed
// run lets the queue send its next message; anything else (failed, cancelled, timed out, disconnected) pauses a queue that has
// messages waiting, because it needs the owner's decision, not a blind retry.
func (s *Service) afterRun(run store.Run) {
	if !store.RunTerminal(run.Status) && run.Status != store.RunDisconnected {
		return // still going: its own end decides
	}
	if run.Status == store.RunCompleted {
		ctx, cancel := context.WithTimeout(s.base, queueDispatchTimeout)
		defer cancel()
		s.dispatchIfFree(ctx, run.UserID, run.ChatID)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.base), 10*time.Second)
	defer cancel()
	if err := store.PauseQueueIfWaiting(ctx, s.db, run.UserID, run.ChatID, run.Status); err != nil {
		log.Printf("chat: queue for chat %s: could not pause: %v", run.ChatID, err)
	}
}

// refusal records what admit answered for a dispatch, so the run-end hook (no browser waiting) can read
// the code and "Send next" can pass the same answer on.
type refusal struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (f *refusal) Header() http.Header {
	if f.h == nil {
		f.h = http.Header{}
	}
	return f.h
}

func (f *refusal) WriteHeader(status int) { f.status = status }

func (f *refusal) Write(b []byte) (int, error) {
	if f.status == 0 {
		f.status = http.StatusOK
	}
	return f.body.Write(b)
}

// code is the API error code admit wrote, or HERMES_UNAVAILABLE if it cannot be read.
func (f *refusal) code() string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(f.body.Bytes(), &e) != nil || e.Error.Code == "" {
		return hermes.CodeHermesUnavailable
	}
	return e.Error.Code
}

func (f *refusal) replay(w http.ResponseWriter) {
	for k, v := range f.h {
		w.Header()[k] = v
	}
	w.WriteHeader(f.status)
	_, _ = w.Write(f.body.Bytes())
}
