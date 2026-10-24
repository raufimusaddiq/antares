package chat

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// History paging (PRD HIST-002, HIST-003, PERF-001). Hermes returns a whole
// transcript or nothing, so the app contract is built over one bounded read:
//
//   - the newest page is the last `limit` turns; nextCursor points at the turns
//     before it;
//   - a turn's identity is its position in the transcript (seq, 0 = oldest),
//     which does not move when newer turns are appended, so pages never overlap
//     and never skip;
//   - a cursor carries the position it stops at plus a hash of the turn there
//     (bound to the chat). If the transcript no longer has that turn in that
//     place (compaction, rewind) the answer is HISTORY_CHANGED and the client
//     reloads the newest page: it is never handed a page that silently
//     disagrees with the one before it.
//
// A cursor is not authority. The chat is looked up by owner first and the
// transcript is the owner's own; a cursor from another chat fails its hash.

const (
	defaultHistoryPage = 50
	maxHistoryPage     = 100
)

// CodeHistoryChanged is the API code for a cursor that no longer matches.
const CodeHistoryChanged = "HISTORY_CHANGED"

func turnHash(chatID string, m hermes.Message) string {
	h := sha256.New()
	for _, part := range []string{chatID, m.Role, m.ID, m.CreatedAt, m.Text} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func encodeHistoryCursor(chatID string, before int, boundary hermes.Message) string {
	raw := "v1." + strconv.Itoa(before) + "." + turnHash(chatID, boundary)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeHistoryCursor returns the exclusive upper bound and the boundary hash.
func decodeHistoryCursor(c string) (before int, hash string, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(raw) > 64 {
		return 0, "", false
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 || parts[0] != "v1" || len(parts[2]) != 16 {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n <= 0 || n > 1<<24 {
		return 0, "", false
	}
	return n, parts[2], true
}

// query parses the query string strictly. net/http's r.URL.Query() drops a
// malformed pair silently, which here would turn a damaged cursor into "no
// cursor" and hand back the newest page again.
func query(r *http.Request) (url.Values, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	return q, err == nil
}

func historyLimit(q url.Values) (int, bool) {
	v := q.Get("limit")
	if v == "" {
		return defaultHistoryPage, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, false
	}
	return min(n, maxHistoryPage), true // the server ceiling wins over a larger ask
}

// pageHistory cuts one page out of the transcript. before < 0 means "newest".
func pageHistory(chatID string, all []hermes.Message, before, limit int, hash string) (page []hermes.Message, next string, err error) {
	end := len(all)
	if before >= 0 {
		if before >= len(all) || turnHash(chatID, all[before]) != hash {
			return nil, "", errHistoryChanged
		}
		end = before
	}
	start := max(0, end-limit)
	page = all[start:end]
	if start > 0 {
		next = encodeHistoryCursor(chatID, start, all[start])
	}
	return page, next, nil
}

var errHistoryChanged = errors.New("chat: history cursor no longer matches the transcript")

func (s *Service) getMessages(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
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
	before, hash := -1, ""
	if c := q.Get("cursor"); c != "" {
		if before, hash, ok = decodeHistoryCursor(c); !ok {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The cursor is not valid")
			return
		}
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
	read := s.now()
	all, err := s.hx.Transcript(r.Context(), b, c.HermesSessionID)
	if err != nil {
		upstreamError(w, err)
		return
	}
	// The transcript is in hand: bring a stale search index of this chat up to date from it.
	if stale, err := store.StaleChats(r.Context(), s.db, uid, c.ID, 1, time.Time{}); err == nil && len(stale) == 1 {
		s.indexTranscript(r.Context(), uid, c, all, read)
	}
	if runs, err := store.ChatRunImages(r.Context(), s.db, uid, c.ID); err == nil && len(runs) > 0 {
		linkRunImages(all, uid, runs)
	}
	page, next, err := pageHistory(c.ID, all, before, limit, hash)
	if err == nil {
		page = append([]hermes.Message(nil), page...) // stamp a copy, not the transcript
		for i := range page {
			page[i].Ref = turnHash(c.ID, page[i])
		}
	}
	if err != nil {
		apiError(w, http.StatusConflict, CodeHistoryChanged, "The conversation changed; reload it")
		return
	}
	if page == nil {
		page = []hermes.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": page, "nextCursor": next, "total": len(all)})
}

// The chat list pages by keyset (store.ListChats); its cursor is opaque too.
// A forged one can only name a position inside the caller's own list.
func encodeChatCursor(c store.ChatCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1\x00" + c.UpdatedAt + "\x00" + c.ID))
}

func decodeChatCursor(s string) (*store.ChatCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) > 128 {
		return nil, false
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] == "" || !idRE.MatchString(parts[2]) {
		return nil, false
	}
	return &store.ChatCursor{UpdatedAt: parts[1], ID: parts[2]}, true
}
