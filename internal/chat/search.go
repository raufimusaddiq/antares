package chat

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Chat search (PRD §21.3, UXP-003). Search is over the caller's own chats and
// nothing else, and that is a property of the query, not of what is rendered:
// store.SearchChats joins the index to the caller's chats on the owner id, so a
// row of another user is unreachable whatever the terms are.
//
// The index is derived. It is filled from the owner's own Hermes transcript
// (the same read as history), a few chats per search at most, and a chat is
// re-read after a run ends in it. A search says how much of the account is
// indexed; it never presents a partial index as complete.
//
// Results are ordered by recency (chat activity, then turn), not relevance:
// there is no score because none is computed.

const (
	defaultSearchPage = 20
	maxSearchPage     = 50
	maxQueryRunes     = 200
	minQueryRunes     = 2
	maxQueryTerms     = 6
	backfillPerSearch = 3 // chats read from Hermes by one search request, at most
	backfillBudget    = 25 * time.Second
	snippetRunes      = 180
	snippetLead       = 60
)

// Segment is a run of snippet text. Hit marks the matched span. The text is data:
// the browser renders it as text, so message content is never interpreted as markup.
type Segment struct {
	Text string `json:"text"`
	Hit  bool   `json:"hit,omitempty"`
}

type searchResult struct {
	ChatID    string    `json:"chatId"`
	ChatTitle string    `json:"chatTitle"`
	Kind      string    `json:"kind"` // "title" | "message"
	Turn      *int      `json:"turn,omitempty"`
	Role      string    `json:"role"`
	CreatedAt string    `json:"createdAt,omitempty"`
	Snippet   []Segment `json:"snippet"`
}

type indexState struct {
	Complete bool `json:"complete"`
	Chats    int  `json:"chats"`
	Pending  int  `json:"pending"` // chats not indexed yet or stale: a later search catches up
	Partial  int  `json:"partial"` // chats cut short by a size or quota limit: they stay partly searchable
}

// parseSearchTerms validates and folds the query. The fold is store.Fold, the
// same function that folded the stored text.
func parseSearchTerms(raw string) ([]string, bool) {
	if !utf8.ValidString(raw) {
		return nil, false
	}
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	raw = strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(raw); n < minQueryRunes || n > maxQueryRunes {
		return nil, false
	}
	seen := map[string]bool{}
	var terms []string
	for _, t := range strings.Fields(store.Fold(raw)) {
		if !seen[t] {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	if len(terms) == 0 || len(terms) > maxQueryTerms {
		return nil, false
	}
	return terms, true
}

func encodeSearchCursor(c store.SearchCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte("s1\x00" + c.CreatedAt + "\x00" + c.ChatID + "\x00" + strconv.Itoa(c.Seq)))
}

// decodeSearchCursor: a forged cursor can only name a position inside the
// caller's own result list.
func decodeSearchCursor(s string) (*store.SearchCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) > 160 {
		return nil, false
	}
	p := strings.Split(string(raw), "\x00")
	if len(p) != 4 || p[0] != "s1" || p[1] == "" || !idRE.MatchString(p[2]) {
		return nil, false
	}
	n, err := strconv.Atoi(p[3])
	if err != nil || n < store.TitleSeq || n > 1<<24 {
		return nil, false
	}
	return &store.SearchCursor{CreatedAt: p[1], ChatID: p[2], Seq: n}, true
}

// snippet cuts a window of body around the first match and marks every match
// inside it. Fold maps rune for rune, so a position in the folded text is the
// same position in body.
func snippet(body string, terms []string) []Segment {
	text := []rune(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, body))
	folded := []rune(store.Fold(string(text)))
	if len(folded) != len(text) {
		return []Segment{{Text: clipRunes(string(text), snippetRunes)}}
	}
	marked := make([]bool, len(text))
	first := -1
	for _, t := range terms {
		tr := []rune(t)
		for i := 0; i+len(tr) <= len(folded); i++ {
			if !runesAt(folded, i, tr) {
				continue
			}
			if first < 0 || i < first {
				first = i
			}
			for j := i; j < i+len(tr); j++ {
				marked[j] = true
			}
		}
	}
	if first < 0 {
		first = 0
	}
	start := max(0, first-snippetLead)
	end := min(len(text), start+snippetRunes)
	var segs []Segment
	if start > 0 {
		segs = append(segs, Segment{Text: "…"})
	}
	for i := start; i < end; {
		j := i
		for j < end && marked[j] == marked[i] {
			j++
		}
		segs = append(segs, Segment{Text: string(text[i:j]), Hit: marked[i]})
		i = j
	}
	if end < len(text) {
		segs = append(segs, Segment{Text: "…"})
	}
	return segs
}

func runesAt(hay []rune, i int, needle []rune) bool {
	for k, r := range needle {
		if hay[i+k] != r {
			return false
		}
	}
	return true
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// ---- indexing ---------------------------------------------------------------------

// indexTranscript writes the owner's transcript into the owner's index. read is
// when the transcript was fetched, so an invalidation that happened after it
// keeps the chat stale.
func (s *Service) indexTranscript(ctx context.Context, userID string, chat store.Chat, msgs []hermes.Message, read time.Time) {
	docs := make([]store.IndexDoc, 0, len(msgs))
	for _, m := range msgs {
		docs = append(docs, store.IndexDoc{Seq: m.Seq, Role: m.Role, Text: m.Text, CreatedAt: m.CreatedAt})
	}
	if _, err := store.ReplaceChatIndex(ctx, s.db, userID, chat.ID, docs, read); err != nil {
		log.Printf("chat: search index: %v", err)
	}
}

// refreshIndex reads up to limit stale chats (all, or just onlyChat) from Hermes
// into the index, newest activity first. It stops at the deadline. Chats it
// cannot read stay stale and are reported as pending.
func (s *Service) refreshIndex(ctx context.Context, userID, onlyChat string, b hermes.Binding, limit int) {
	ctx, cancel := context.WithTimeout(ctx, backfillBudget)
	defer cancel()
	stale, err := store.StaleChats(ctx, s.db, userID, onlyChat, limit)
	if err != nil {
		log.Printf("chat: search index: %v", err)
		return
	}
	for _, c := range stale {
		read := s.now()
		msgs, err := s.hx.Transcript(ctx, b, c.HermesSessionID)
		if err != nil {
			log.Printf("chat: search index: transcript read failed: %s", hermes.CodeOf(err))
			if ctx.Err() != nil {
				return
			}
			continue
		}
		s.indexTranscript(ctx, userID, c, msgs, read)
	}
}

// ---- handlers ---------------------------------------------------------------------

var (
	globalSearchParams = map[string]bool{"q": true, "cursor": true, "limit": true, "role": true, "chatId": true}
	chatSearchParams   = map[string]bool{"q": true, "cursor": true, "limit": true, "role": true}
)

func (s *Service) searchAll(w http.ResponseWriter, r *http.Request) {
	s.search(w, r, "", globalSearchParams)
}

func (s *Service) searchOne(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	s.search(w, r, id, chatSearchParams)
}

func (s *Service) search(w http.ResponseWriter, r *http.Request, pathChat string, allowed map[string]bool) {
	uid, _ := user(r)
	bad := func(msg string) { apiError(w, http.StatusBadRequest, "INVALID_REQUEST", msg) }
	q, ok := query(r)
	if !ok {
		bad("The query string is not valid")
		return
	}
	for k := range q {
		if !allowed[k] { // a profile, key or session id in a search is refused, not ignored
			bad("The request carries a parameter the server does not accept")
			return
		}
	}
	terms, ok := parseSearchTerms(q.Get("q"))
	if !ok {
		bad("The search text must be 2 to 200 characters and at most 6 words")
		return
	}
	limit := defaultSearchPage
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			bad("limit must be a positive number")
			return
		}
		limit = min(n, maxSearchPage)
	}
	role := q.Get("role")
	if role != "" && role != "user" && role != "assistant" && role != store.RoleTitle {
		bad("role must be user, assistant or title")
		return
	}
	var after *store.SearchCursor
	if c := q.Get("cursor"); c != "" {
		if after, ok = decodeSearchCursor(c); !ok {
			bad("The cursor is not valid")
			return
		}
	}
	scope := pathChat
	if scope == "" && q.Has("chatId") {
		if scope = q.Get("chatId"); !idRE.MatchString(scope) {
			notFound(w)
			return
		}
	}
	if scope != "" { // a chat that is not the caller's is a 404, however it was named
		if _, err := store.GetChat(r.Context(), s.db, uid, scope); errors.Is(err, store.ErrNoChat) {
			notFound(w)
			return
		} else if err != nil {
			internalError(w, "get chat", err)
			return
		}
	}
	// Default deny: the same gate as every Hermes-backed read, before the index is touched.
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		s.gateError(w, err)
		return
	}
	if after == nil { // later pages do not trigger more reads
		perSearch := backfillPerSearch
		if scope != "" {
			perSearch = 1
		}
		s.refreshIndex(r.Context(), uid, scope, b, perSearch)
	}
	hits, more, err := store.SearchChats(r.Context(), s.db, store.SearchQuery{UserID: uid, Terms: terms, ChatID: scope, Role: role, Limit: limit, After: after})
	if err != nil {
		internalError(w, "search", err)
		return
	}
	total, pending, partial, err := store.IndexStatus(r.Context(), s.db, uid, scope)
	if err != nil {
		internalError(w, "search status", err)
		return
	}
	out := make([]searchResult, 0, len(hits))
	for _, h := range hits {
		res := searchResult{ChatID: h.ChatID, ChatTitle: h.ChatTitle, Kind: "message", Role: h.Role, CreatedAt: h.CreatedAt, Snippet: snippet(h.Body, terms)}
		if h.Seq == store.TitleSeq {
			res.Kind = "title"
		} else {
			t := h.Seq
			res.Turn = &t
		}
		out = append(out, res)
	}
	next := ""
	if more && len(hits) > 0 {
		l := hits[len(hits)-1]
		next = encodeSearchCursor(store.SearchCursor{CreatedAt: l.ChatCreatedAt, ChatID: l.ChatID, Seq: l.Seq})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":    out,
		"nextCursor": next,
		"index":      indexState{Complete: pending == 0 && partial == 0, Chats: total, Pending: pending, Partial: partial},
	})
}
