package chat

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// getMedia serves a file Hermes generated for the caller (an image, a voice reply, a video), named in a reply as
// MEDIA:<path>. The profile is the caller's own binding, and the helper serves only that profile's cache. The bytes are the caller's own, so they are served private, never sniffed,
// and sandboxed if opened directly.
func (s *Service) getMedia(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	b, _, err := s.gate(r.Context(), uid)
	if err != nil || s.media == nil {
		notFound(w)
		return
	}
	// The helper decides whether the path is in this profile's cache (it knows the layout; Antares does not): here
	// only a clean absolute path is passed on, with the caller's own profile, never one the browser names.
	path := r.URL.Query().Get("path")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		notFound(w)
		return
	}
	data, mt, err := s.media.Media(r.Context(), b.Profile, path)
	if errors.Is(err, hermes.ErrMediaNotFound) || (err == nil && !mediaType(mt)) {
		notFound(w)
		return
	} else if err != nil {
		apiError(w, http.StatusBadGateway, "HERMES_UNAVAILABLE", "The assistant's media could not be read right now")
		return
	}
	h := w.Header()
	h.Set("Content-Type", mt)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	_, _ = w.Write(data)
}

func mediaType(mt string) bool {
	return strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "audio/") || strings.HasPrefix(mt, "video/")
}
