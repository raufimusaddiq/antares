package blog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"

	"github.com/raufimusaddiq/antares/internal/auth"
)

var (
	idRE    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	assetRE = idRE
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code, msg string, extra map[string]any) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	body := map[string]any{"error": map[string]string{"code": code, "message": msg, "requestId": "req_" + hex.EncodeToString(b)}}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func notFound(w http.ResponseWriter) { apiError(w, http.StatusNotFound, "NOT_FOUND", "Not found", nil) }
func invalid(w http.ResponseWriter, msg string) {
	apiError(w, http.StatusBadRequest, "INVALID_REQUEST", msg, nil)
}

// fail logs the cause server-side and tells the browser nothing of it.
func fail(w http.ResponseWriter, what string, err error) {
	log.Printf("blog: %s: %v", what, err)
	apiError(w, http.StatusInternalServerError, "INTERNAL", "Something went wrong", nil)
}

func writeAsset(w http.ResponseWriter, a Asset, cache string) {
	w.Header().Set("Content-Type", a.Type)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Length", strconv.Itoa(len(a.Data)))
	_, _ = w.Write(a.Data)
}

func me(r *http.Request) (string, bool) {
	u, ok := auth.UserFrom(r.Context())
	return u.ID, ok
}

// decode reads a small JSON body with unknown fields refused: a profile, route prefix, key, header or session id from
// the browser is a 400, never ignored.
func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			apiError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "That is too large", nil)
			return false
		}
		invalid(w, "The request body is not valid")
		return false
	}
	return true
}

// emptyBody accepts {} or no body for the POSTs that take none.
func emptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	var v struct{}
	return decode(w, r, 1<<10, &v)
}

// own wraps a handler that needs the caller and a well-formed post id; a malformed id is the same 404 as a foreign one.
func own(h func(w http.ResponseWriter, r *http.Request, uid, id string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := me(r)
		id := r.PathValue("id")
		if !ok || !idRE.MatchString(id) {
			notFound(w)
			return
		}
		h(w, r, uid, id)
	})
}

// reply maps the store's errors to the contract's answers.
func reply(w http.ResponseWriter, p Post, err error) {
	var c ConflictError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, p)
	case errors.Is(err, ErrNotFound):
		notFound(w)
	case errors.As(err, &c):
		apiError(w, http.StatusConflict, "CONFLICT", "The post changed", map[string]any{"latest": c.Latest})
	case errors.Is(err, ErrSlugTaken):
		apiError(w, http.StatusConflict, "SLUG_TAKEN", "That address is used by another published post", nil)
	case errors.Is(err, ErrInvalid):
		apiError(w, http.StatusUnprocessableEntity, "INVALID_REQUEST", "That cannot be saved or published as it is", nil)
	case errors.Is(err, ErrTooMany):
		apiError(w, http.StatusUnprocessableEntity, "LIMIT_REACHED", "A limit was reached", nil)
	default:
		fail(w, "request", err)
	}
}

// Handlers are the author's routes, keyed as in auth.BlogRoutes. Auth wraps each in the Active guard and the
// same-origin check; every one acts on the caller's own posts only.
func (s *Service) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/blog/capabilities": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Writing and publishing need nothing outside Antares. Checking and web search arrive with the Hermes
			// check; until then they are not offered.
			writeJSON(w, http.StatusOK, map[string]bool{"writing": true, "check": false, "webSearch": false})
		}),
		"GET /api/blog/posts": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, _ := me(r)
			l, err := s.List(r.Context(), uid)
			if err != nil {
				fail(w, "list", err)
				return
			}
			writeJSON(w, http.StatusOK, l)
		}),
		"POST /api/blog/posts": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, _ := me(r)
			if !emptyBody(w, r) {
				return
			}
			p, err := s.Create(r.Context(), uid)
			reply(w, p, err)
		}),
		"GET /api/blog/posts/{id}": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			p, err := s.Get(r.Context(), uid, id)
			reply(w, p, err)
		}),
		"PUT /api/blog/posts/{id}": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			var b struct {
				BaseVersion *int            `json:"baseVersion"`
				Title       *string         `json:"title"`
				Summary     *string         `json:"summary"`
				Doc         json.RawMessage `json:"doc"`
			}
			if !decode(w, r, maxDocBytes+4096, &b) {
				return
			}
			if b.BaseVersion == nil {
				invalid(w, "baseVersion is required")
				return
			}
			c := Change{Title: b.Title, Summary: b.Summary}
			if len(b.Doc) > 0 {
				clean, err := CleanDoc(b.Doc)
				if err != nil {
					apiError(w, http.StatusUnprocessableEntity, "INVALID_DOCUMENT", "The document has something that is not allowed", nil)
					return
				}
				c.Doc = clean
			}
			p, err := s.Autosave(r.Context(), uid, id, *b.BaseVersion, c)
			reply(w, p, err)
		}),
		"PATCH /api/blog/posts/{id}/settings": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			var b struct {
				Slug         *string `json:"slug"`
				Summary      *string `json:"summary"`
				CoverAssetID *string `json:"coverAssetId"`
			}
			if !decode(w, r, 4<<10, &b) {
				return
			}
			if b.CoverAssetID != nil && *b.CoverAssetID != "" && !assetRE.MatchString(*b.CoverAssetID) {
				invalid(w, "That image is not one of this post's")
				return
			}
			p, err := s.Settings(r.Context(), uid, id, b.Slug, b.Summary, b.CoverAssetID)
			reply(w, p, err)
		}),
		"DELETE /api/blog/posts/{id}": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			if err := s.Delete(r.Context(), uid, id); errors.Is(err, ErrNotFound) {
				notFound(w)
			} else if err != nil {
				fail(w, "delete", err)
			} else {
				writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			}
		}),
		"POST /api/blog/posts/{id}/publish": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			var b struct {
				Mode string `json:"mode"`
			}
			if !decode(w, r, 1<<10, &b) {
				return
			}
			switch b.Mode {
			case "original":
				p, err := s.PublishOriginal(r.Context(), uid, id)
				reply(w, p, err)
			case "enriched":
				apiError(w, http.StatusNotImplemented, "CAPABILITY_UNAVAILABLE", "Checking is not available yet", nil)
			default:
				invalid(w, "mode must be original or enriched")
			}
		}),
		"POST /api/blog/posts/{id}/unpublish": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			if !emptyBody(w, r) {
				return
			}
			p, err := s.Unpublish(r.Context(), uid, id)
			reply(w, p, err)
		}),
		"POST /api/blog/posts/{id}/duplicate": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			if !emptyBody(w, r) {
				return
			}
			p, err := s.Duplicate(r.Context(), uid, id)
			reply(w, p, err)
		}),
		"POST /api/blog/posts/{id}/assets": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			r.Body = http.MaxBytesReader(w, r.Body, maxAssetBytes)
			data, err := io.ReadAll(r.Body)
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				apiError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "That image is too large", nil)
				return
			} else if err != nil {
				invalid(w, "The upload could not be read")
				return
			}
			aid, err := s.AddAsset(r.Context(), uid, id, data)
			switch {
			case errors.Is(err, ErrNotImage):
				apiError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA", "Use PNG, JPEG, WebP or GIF", nil)
			case errors.Is(err, ErrNotFound):
				notFound(w)
			case errors.Is(err, ErrTooMany):
				apiError(w, http.StatusUnprocessableEntity, "LIMIT_REACHED", "This post has the most images it can hold", nil)
			case err != nil:
				fail(w, "upload", err)
			default:
				writeJSON(w, http.StatusOK, map[string]string{"assetId": aid, "url": assetURL(id, aid)})
			}
		}),
		"GET /api/blog/posts/{id}/assets/{assetId}": own(func(w http.ResponseWriter, r *http.Request, uid, id string) {
			aid := r.PathValue("assetId")
			if !assetRE.MatchString(aid) {
				notFound(w)
				return
			}
			a, err := s.OwnAsset(r.Context(), uid, id, aid)
			if errors.Is(err, ErrNotFound) {
				notFound(w)
			} else if err != nil {
				fail(w, "asset", err)
			} else {
				writeAsset(w, a, "private, max-age=300")
			}
		}),
	}
}
