package blog

import (
	"errors"
	"net/http"

	"github.com/raufimusaddiq/antares/internal/store"
)

// AdminHandlers are the administrator's post routes, keyed "METHOD /path" as auth registers them (admin.go). Auth wraps each
// in the Admin guard and, for the POSTs, the same-origin check. Only published content and the takedown state are ever
// returned (see takedown.go): there is no draft, version, check or reader field in any type here.
func (s *Service) AdminHandlers() map[string]http.Handler {
	adminErr := func(w http.ResponseWriter, what string, err error) {
		switch {
		case errors.Is(err, ErrNotFound):
			notFound(w)
		case errors.Is(err, ErrAlreadyTakenDown):
			apiError(w, http.StatusConflict, "ALREADY_TAKEN_DOWN", "Someone already took this post down. Nothing was changed.", nil)
		case errors.Is(err, ErrNotTakenDown):
			apiError(w, http.StatusConflict, "NOT_TAKEN_DOWN", "This post is not taken down. Nothing was changed.", nil)
		case errors.Is(err, ErrBadReason):
			invalid(w, "A takedown needs a reason of 1 to 500 characters")
		case errors.Is(err, ErrInvalid):
			invalid(w, "The request is not valid")
		default:
			fail(w, what, err)
		}
	}
	return map[string]http.Handler{
		"GET /api/admin/blog-posts": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			items, next, err := s.AdminList(r.Context(), q.Get("state"), q.Get("q"), q.Get("after"))
			if err != nil {
				adminErr(w, "admin list", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
		}),
		"GET /api/admin/blog-posts/{postId}": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			v, err := s.AdminGet(r.Context(), r.PathValue("postId"))
			if err != nil {
				adminErr(w, "admin get", err)
				return
			}
			writeJSON(w, http.StatusOK, v)
		}),
		"POST /api/admin/blog-posts/{postId}/take-down": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, _ := me(r)
			var b struct {
				Reason string `json:"reason"`
			}
			if !decode(w, r, 4<<10, &b) {
				return
			}
			p, err := s.TakeDown(r.Context(), actor, r.PathValue("postId"), b.Reason, store.NewID())
			if err != nil {
				adminErr(w, "take down", err)
				return
			}
			writeJSON(w, http.StatusOK, p)
		}),
		"POST /api/admin/blog-posts/{postId}/restore": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, _ := me(r)
			if !emptyBody(w, r) {
				return
			}
			p, note, err := s.Restore(r.Context(), actor, r.PathValue("postId"), store.NewID())
			if err != nil {
				adminErr(w, "restore", err)
				return
			}
			if note {
				writeJSON(w, http.StatusOK, map[string]any{"note": "The author has unpublished it; it stays unpublished."})
				return
			}
			writeJSON(w, http.StatusOK, p)
		}),
	}
}

// noticeHandlers are the author's one-time takedown notices, added to Handlers: the unseen ones, and the mark that one was seen.
func (s *Service) noticeHandlers() map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/blog/notices": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, _ := me(r)
			n, err := s.Notices(r.Context(), uid)
			if err != nil {
				fail(w, "notices", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"notices": n})
		}),
		"POST /api/blog/notices/{id}/seen": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			if !emptyBody(w, r) {
				return
			}
			if err := s.MarkSeen(r.Context(), uid, id); err != nil {
				if errors.Is(err, ErrNotFound) {
					notFound(w)
					return
				}
				fail(w, "notice seen", err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	}
}
