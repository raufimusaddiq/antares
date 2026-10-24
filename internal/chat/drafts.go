package chat

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Drafts and slash templates (PRD §21.6, UXP-006/007, issue #107). Both are owner-scoped
// text the server stores and returns verbatim. Nothing here reaches Hermes, and a template
// body is never interpreted: inserting one is a client text edit.

var templateNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

type draftBody struct {
	Body string `json:"body"`
}

type draftView struct {
	Body      string `json:"body"`
	Revision  int64  `json:"revision"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

func writeDraft(w http.ResponseWriter, status int, d store.Draft) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(d.Revision, 10)+`"`)
	writeJSON(w, status, draftView{Body: d.Body, Revision: d.Revision, UpdatedAt: d.UpdatedAt})
}

// ifMatch reads the revision the writer last saw: If-Match: "N" (N=0 is "no draft yet").
// A write without it is refused rather than treated as "overwrite", so no client can
// clobber a newer draft by forgetting the header.
func ifMatch(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		apiError(w, http.StatusPreconditionRequired, "REVISION_REQUIRED", "Send the draft revision you last saw in If-Match")
		return 0, false
	}
	n, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64)
	if err != nil || n < 0 {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "If-Match is not a draft revision")
		return 0, false
	}
	return n, true
}

func draftConflict(w http.ResponseWriter, cur store.Draft) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(cur.Revision, 10)+`"`)
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":   map[string]string{"code": "REVISION_CONFLICT", "message": "This draft changed on another device"},
		"current": draftView{Body: cur.Body, Revision: cur.Revision, UpdatedAt: cur.UpdatedAt},
	})
}

func validDraft(w http.ResponseWriter, b draftBody) bool {
	if len(b.Body) > store.MaxDraftBytes || !utf8.ValidString(b.Body) || strings.ContainsRune(b.Body, 0) {
		apiError(w, http.StatusUnprocessableEntity, "INVALID_REQUEST", "The draft is too long or not valid text")
		return false
	}
	return true
}

// draftAPI wires one scope (a chat's draft or the new-chat draft) to the three verbs.
// chatID is "" for the new-chat draft.
type draftAPI struct {
	get func(r *http.Request, uid string) (store.Draft, error)
	put func(r *http.Request, uid, body string, rev int64) (store.Draft, error)
	del func(r *http.Request, uid string, rev int64) (store.Draft, error)
	// resolve reports the scope's id from the path (false = not found); the chat's own draft
	// needs a well-formed chat id, the new-chat draft needs none.
	resolve func(r *http.Request) (string, bool)
}

func (s *Service) chatDraftAPI() draftAPI {
	return draftAPI{
		resolve: func(r *http.Request) (string, bool) { return pathID(r, "chatId") },
		get: func(r *http.Request, uid string) (store.Draft, error) {
			return store.GetChatDraft(r.Context(), s.db, uid, r.PathValue("chatId"))
		},
		put: func(r *http.Request, uid, body string, rev int64) (store.Draft, error) {
			return store.PutChatDraft(r.Context(), s.db, uid, r.PathValue("chatId"), body, rev, s.now())
		},
		del: func(r *http.Request, uid string, rev int64) (store.Draft, error) {
			return store.DeleteChatDraft(r.Context(), s.db, uid, r.PathValue("chatId"), rev)
		},
	}
}

func (s *Service) newChatDraftAPI() draftAPI {
	return draftAPI{
		resolve: func(*http.Request) (string, bool) { return "", true },
		get: func(r *http.Request, uid string) (store.Draft, error) {
			return store.GetNewChatDraft(r.Context(), s.db, uid)
		},
		put: func(r *http.Request, uid, body string, rev int64) (store.Draft, error) {
			return store.PutNewChatDraft(r.Context(), s.db, uid, body, rev, s.now())
		},
		del: func(r *http.Request, uid string, rev int64) (store.Draft, error) {
			return store.DeleteNewChatDraft(r.Context(), s.db, uid, rev)
		},
	}
}

func (a draftAPI) handlers(s *Service) (get, put, del http.HandlerFunc) {
	// Ownership is resolved first in every verb, before the body or headers are looked
	// at, so a foreign chat id answers exactly like an unknown one.
	fail := func(w http.ResponseWriter, err error) {
		if errors.Is(err, store.ErrNoChat) {
			notFound(w)
			return
		}
		internalError(w, "draft", err)
	}
	get = func(w http.ResponseWriter, r *http.Request) {
		uid, _ := user(r)
		if _, ok := a.resolve(r); !ok {
			notFound(w)
			return
		}
		d, err := a.get(r, uid)
		if err != nil {
			fail(w, err)
			return
		}
		writeDraft(w, http.StatusOK, d)
	}
	put = func(w http.ResponseWriter, r *http.Request) {
		uid, _ := user(r)
		if _, ok := a.resolve(r); !ok {
			notFound(w)
			return
		}
		if _, err := a.get(r, uid); err != nil {
			fail(w, err)
			return
		}
		rev, ok := ifMatch(w, r)
		if !ok {
			return
		}
		var b draftBody
		if !decode(w, r, &b) || !validDraft(w, b) {
			return
		}
		d, err := a.put(r, uid, b.Body, rev)
		switch {
		case errors.Is(err, store.ErrRevisionConflict):
			draftConflict(w, d)
		case err != nil:
			fail(w, err)
		default:
			writeDraft(w, http.StatusOK, d)
		}
	}
	del = func(w http.ResponseWriter, r *http.Request) {
		uid, _ := user(r)
		if _, ok := a.resolve(r); !ok {
			notFound(w)
			return
		}
		if _, err := a.get(r, uid); err != nil {
			fail(w, err)
			return
		}
		rev, ok := ifMatch(w, r)
		if !ok {
			return
		}
		cur, err := a.del(r, uid, rev)
		switch {
		case errors.Is(err, store.ErrRevisionConflict):
			draftConflict(w, cur)
		case err != nil:
			fail(w, err)
		default:
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
		}
	}
	return
}

// ---- templates -------------------------------------------------------------------

type templateBody struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type templateView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func viewTemplate(t store.Template) templateView {
	return templateView{ID: t.ID, Name: t.Name, Body: t.Body, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// validTemplate normalises the name to lower case (so "Foo" and "foo" are one name) and
// bounds both fields. The body is arbitrary text; it is stored, never executed.
func validTemplate(w http.ResponseWriter, b *templateBody) bool {
	b.Name = strings.ToLower(strings.TrimSpace(b.Name))
	switch {
	case !templateNameRE.MatchString(b.Name):
		apiError(w, http.StatusUnprocessableEntity, "INVALID_REQUEST", "A template name is 1-32 letters, digits, - or _")
	case b.Body == "" || len(b.Body) > store.MaxTemplateBody || !utf8.ValidString(b.Body) || strings.ContainsRune(b.Body, 0):
		apiError(w, http.StatusUnprocessableEntity, "INVALID_REQUEST", "A template body is 1-8192 bytes of text")
	default:
		return true
	}
	return false
}

func templateStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoTemplate):
		notFound(w)
	case errors.Is(err, store.ErrTemplateName):
		apiError(w, http.StatusConflict, "TEMPLATE_NAME_TAKEN", "You already have a template with that name")
	case errors.Is(err, store.ErrTooManyTemplates):
		apiError(w, http.StatusUnprocessableEntity, "TOO_MANY_TEMPLATES", "You have reached the template limit")
	default:
		internalError(w, "template", err)
	}
}

func (s *Service) listTemplates(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	ts, err := store.ListTemplates(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "list templates", err)
		return
	}
	out := make([]templateView, 0, len(ts))
	for _, t := range ts {
		out = append(out, viewTemplate(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

func (s *Service) createTemplate(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var b templateBody
	if !decode(w, r, &b) || !validTemplate(w, &b) {
		return
	}
	t, err := store.CreateTemplate(r.Context(), s.db, uid, b.Name, b.Body, s.now())
	if err != nil {
		templateStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewTemplate(t))
}

func (s *Service) putTemplate(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "templateId")
	if !ok {
		notFound(w)
		return
	}
	var b templateBody
	if !decode(w, r, &b) || !validTemplate(w, &b) {
		return
	}
	t, err := store.UpdateTemplate(r.Context(), s.db, uid, id, b.Name, b.Body, s.now())
	if err != nil {
		templateStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewTemplate(t))
}

func (s *Service) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "templateId")
	if !ok {
		notFound(w)
		return
	}
	if err := store.DeleteTemplate(r.Context(), s.db, uid, id); err != nil {
		templateStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
