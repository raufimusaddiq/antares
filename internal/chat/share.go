package chat

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Share a chat by link (#250). The owner's share takes a frozen copy of the chat's turns, through the same gated
// transcript read as history; later turns are not added. Anyone holding the link can read that copy without signing
// in. The link is a random token the server keeps only as a hash, so it is shown once, when it is made.
//
// The public read touches no Hermes and returns the title, the date and each turn's role and text: no account, chat
// or profile identity. Media the assistant made (a MEDIA: tag naming a file in the owner's profile) is replaced by a
// note, so no path and no file leaves through a link.

const maxShareBytes = 2 << 20

var shareTokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type shareTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type shareStatusView struct {
	Shared    bool   `json:"shared"`
	CreatedAt string `json:"createdAt,omitempty"`
	Turns     int    `json:"turns,omitempty"`
}

func shareHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// shareText is a turn's text as a link shows it: the assistant's media tags become a note.
func shareText(text string) string {
	text = strings.ReplaceAll(text, "[[audio_as_voice]]", "")
	if !strings.Contains(text, "MEDIA:") {
		return text
	}
	return mediaTag.ReplaceAllStringFunc(text, func(tag string) string {
		m := mediaTag.FindStringSubmatch(tag)
		tail := m[3][len(strings.TrimRight(m[3], ".,;:!?)]}>")):]
		return "[media not shared]" + tail
	})
}

func (s *Service) createShare(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	c, err := store.GetChat(r.Context(), s.db, uid, id)
	if errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get chat", err)
		return
	}
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		s.gateError(w, err)
		return
	}
	all, err := s.hx.Transcript(r.Context(), b, c.HermesSessionID)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if len(all) == 0 {
		apiError(w, http.StatusConflict, "INVALID_REQUEST", "There is nothing in this chat to share yet")
		return
	}
	turns := make([]shareTurn, len(all))
	for i, m := range all {
		turns[i] = shareTurn{Role: m.Role, Text: shareText(m.Text)}
	}
	body, err := json.Marshal(turns)
	if err != nil {
		internalError(w, "encode share", err)
		return
	}
	if len(body) > maxShareBytes {
		apiError(w, http.StatusRequestEntityTooLarge, "INVALID_REQUEST", "This conversation is too long to share by link")
		return
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	if err := store.PutShare(r.Context(), s.db, uid, c.ID, shareHash(token), c.Title, string(body), len(turns), now); errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "put share", err)
		return
	}
	s.audit(r.Context(), uid, "chat.share", c.ID, "ok")
	st, err := store.GetShareStatus(r.Context(), s.db, uid, c.ID)
	if err != nil {
		internalError(w, "share status", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "createdAt": st.CreatedAt, "turns": st.Turns})
}

func (s *Service) getShare(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	if !s.ownChat(w, r, uid, id) {
		return
	}
	st, err := store.GetShareStatus(r.Context(), s.db, uid, id)
	if errors.Is(err, store.ErrNoShare) {
		writeJSON(w, http.StatusOK, shareStatusView{})
		return
	} else if err != nil {
		internalError(w, "share status", err)
		return
	}
	writeJSON(w, http.StatusOK, shareStatusView{Shared: true, CreatedAt: st.CreatedAt, Turns: st.Turns})
}

func (s *Service) deleteShare(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	if !s.ownChat(w, r, uid, id) {
		return
	}
	if err := store.DeleteShare(r.Context(), s.db, uid, id); err != nil {
		internalError(w, "delete share", err)
		return
	}
	s.audit(r.Context(), uid, "chat.unshare", id, "ok")
	w.WriteHeader(http.StatusNoContent)
}

// readShared is the public read: no session, no gate, no Hermes. An unknown, stopped or replaced link, and one whose
// chat or owner may no longer be read, are the same 404.
func (s *Service) readShared(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !shareTokenRE.MatchString(token) {
		notFound(w)
		return
	}
	sh, err := store.ReadShare(r.Context(), s.db, shareHash(token))
	if errors.Is(err, store.ErrNoShare) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "read share", err)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, http.StatusOK, map[string]any{"title": sh.Title, "sharedAt": sh.CreatedAt, "turns": json.RawMessage(sh.Turns)})
}

// PublicHandlers are the routes that answer without a session, keyed as in auth.PublicRoutes.
func (s *Service) PublicHandlers() map[string]http.Handler {
	return map[string]http.Handler{"GET /api/shared/{token}": http.HandlerFunc(s.readShared)}
}
