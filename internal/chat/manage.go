package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Chat management (issue #148, PRD §26.1 session/history, §21.5 destructive operations).
//
// Rename, pin and archive are changes to Antares' own index of the owner's chats: Hermes' session
// list is never what the sidebar shows (PRD §9A.2, "filter server-side owner index first"), so
// nothing upstream needs to hear about them and nothing is claimed about upstream. Delete is the
// one operation that also changes Hermes, and it is ordered so that no step can report a success
// that did not happen: claim the chat (PENDING_DELETE), delete the session upstream, and only a
// confirmed upstream deletion (or Hermes' own "no such session") lets Antares remove its records.
// Any other answer leaves the chat in place as DELETE_FAILED, visible and retryable.

const (
	actRename    = "chat.rename"
	actPin       = "chat.pin"
	actUnpin     = "chat.unpin"
	actArchive   = "chat.archive"
	actUnarchive = "chat.unarchive"
	actDelete    = "chat.delete"
)

// settleTimeout bounds the local steps after Hermes has answered. They run on a context that
// survives the browser going away: a delete Hermes has completed must not strand the chat.
const settleTimeout = 10 * time.Second

// audit records one management action. Metadata only: the actor, the action, the chat's opaque id
// and the outcome. No title, no session id, no profile and no text of the chat ever goes in.
func (s *Service) audit(ctx context.Context, uid, action, chatID, outcome string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	if err := store.Audit(ctx, s.db, uid, action, "chat", chatID, outcome, "req_"+hex.EncodeToString(b), s.now()); err != nil {
		log.Printf("chat: audit %s: %v", action, err)
	}
}

type patchChatBody struct {
	Title    *string `json:"title"`
	Pinned   *bool   `json:"pinned"`
	Archived *bool   `json:"archived"`
}

// patchChat changes exactly one of the chat's title, pin or archive state. One field per request
// keeps each change a single audited action with a single outcome.
func (s *Service) patchChat(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	var body patchChatBody
	if !decode(w, r, &body) {
		return
	}
	n := 0
	for _, set := range []bool{body.Title != nil, body.Pinned != nil, body.Archived != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Send exactly one of title, pinned or archived")
		return
	}
	var action string
	var err error
	switch {
	case body.Title != nil:
		if !utf8.ValidString(*body.Title) {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The title is not valid text")
			return
		}
		title := cleanTitle(*body.Title)
		if title == "" {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "A chat needs a name")
			return
		}
		action, err = actRename, store.RenameChat(r.Context(), s.db, uid, id, title)
	case body.Pinned != nil:
		action = actUnpin
		if *body.Pinned {
			action = actPin
		}
		err = store.SetChatPinned(r.Context(), s.db, uid, id, *body.Pinned, s.now())
	default:
		action = actUnarchive
		if *body.Archived {
			action = actArchive
		}
		err = store.SetChatArchived(r.Context(), s.db, uid, id, *body.Archived)
	}
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
		return
	case errors.Is(err, store.ErrChatState):
		s.audit(r.Context(), uid, action, id, "refused:CHAT_STATE")
		apiError(w, http.StatusConflict, "CHAT_STATE", "This chat can't be changed that way right now")
		return
	case err != nil:
		s.audit(r.Context(), uid, action, id, "failed:INTERNAL")
		internalError(w, action, err)
		return
	}
	s.audit(r.Context(), uid, action, id, "ok")
	// A chat that came back from the archive (or was renamed) must show as the index now has it,
	// including the model choice getChat returns.
	s.getChat(w, r)
}

// deleteChat deletes the user's own chat, here and upstream. See the file comment for the order.
func (s *Service) deleteChat(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	// Ownership first: a foreign or unknown chat is a 404 before anything else is learned.
	chat, err := store.GetChat(r.Context(), s.db, uid, id)
	if errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get chat", err)
		return
	}
	b, bindingID, err := s.gate(r.Context(), uid) // default deny: no healthy binding, no Hermes call, no change
	if err != nil {
		s.gateError(w, err)
		return
	}
	if chat.BindingID != bindingID {
		// The session lives on a profile this account no longer holds; asking the current one would
		// answer "not found" and look like success.
		apiError(w, http.StatusConflict, "CHAT_STATE", "This chat belongs to an earlier workspace and can't be deleted from here")
		return
	}
	adv, err := s.hx.SessionDeleteAdvertised(r.Context(), b)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if !adv {
		apiError(w, http.StatusNotImplemented, hermes.CodeCapabilityNA, "The assistant does not offer deleting conversations")
		return
	}
	claimed, err := store.BeginDeleteChat(r.Context(), s.db, uid, id, s.now())
	switch {
	case errors.Is(err, store.ErrNoChat):
		notFound(w)
		return
	case errors.Is(err, store.ErrRunActive):
		apiError(w, http.StatusConflict, hermes.CodeRunConflict, "Stop the reply in progress before deleting this chat")
		return
	case err != nil:
		internalError(w, "begin delete", err)
		return
	}
	err = s.hx.DeleteSession(r.Context(), b, claimed.HermesSessionID)
	// From here the browser's connection no longer decides how this ends.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), settleTimeout)
	defer cancel()
	if err != nil && !errors.Is(err, hermes.ErrSessionGone) {
		if merr := store.MarkDeleteFailed(ctx, s.db, uid, id); merr != nil {
			log.Printf("chat: mark delete failed: %v", merr)
		}
		s.audit(ctx, uid, actDelete, id, "failed:"+hermes.CodeOf(err))
		upstreamError(w, err)
		return
	}
	_, err = store.FinishDeleteChat(ctx, s.db, uid, id)
	if err != nil {
		// Hermes no longer has the session but the local records are still here. The chat stays
		// DELETE_FAILED, so a retry (Hermes answers "not found", which finishes the job) can complete it.
		if merr := store.MarkDeleteFailed(ctx, s.db, uid, id); merr != nil {
			log.Printf("chat: mark delete failed: %v", merr)
		}
		s.audit(ctx, uid, actDelete, id, "failed:LOCAL_CLEANUP")
		internalError(w, "finish delete", err)
		return
	}
	s.audit(ctx, uid, actDelete, id, "ok")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
