package chat

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// Hermes' media tools (an image, a voice reply, a video) name the file they generated as MEDIA:<path>, a path in the
// user's own profile cache. That path, and the profile it names, never reaches the browser: the history API replaces
// each tag with MEDIA:<id>, where the id is the kind of media and the path sealed with the server's key and bound to
// the user (sealMediaTags). GET /api/media?id= opens it again for that user only and asks the helper, with the user's
// own profile, for the bytes.

var mediaTag = regexp.MustCompile(`MEDIA:(?:"([^"\n]*)"|'([^'\n]*)'|(\S+))`)

var mediaKinds = map[string]string{
	".png": "image", ".jpg": "image", ".jpeg": "image", ".webp": "image", ".gif": "image",
	".mp3": "audio", ".ogg": "audio", ".wav": "audio", ".m4a": "audio",
	".mp4": "video", ".webm": "video",
}

func mediaAAD(uid string) string { return "antares-media:" + uid }

// sealMediaTags replaces every MEDIA:<path> in an assistant reply with MEDIA:<kind>-<id>. A tag that is not a media
// file is dropped. Nothing else in the text changes.
func (s *Service) sealMediaTags(uid, text string) string {
	if !strings.Contains(text, "MEDIA:") {
		return text
	}
	return mediaTag.ReplaceAllStringFunc(text, func(tag string) string {
		m := mediaTag.FindStringSubmatch(tag)
		path, tail := m[1]+m[2], ""
		if m[3] != "" {
			path = strings.TrimRight(m[3], ".,;:!?)]}>")
			tail = m[3][len(path):]
		}
		kind := mediaKinds[strings.ToLower(filepath.Ext(path))]
		if kind == "" {
			return tail
		}
		sealed, err := s.box.Seal(path, mediaAAD(uid))
		if err != nil {
			return tail
		}
		return "MEDIA:" + kind + "-" + base64.RawURLEncoding.EncodeToString(sealed) + tail
	})
}

// getMedia serves one generated file to the user its id was issued to. The bytes are the user's own: private, never
// sniffed, sandboxed if opened directly, with Range support so audio and video can seek.
func (s *Service) getMedia(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id := r.URL.Query().Get("id")
	_, rest, ok := strings.Cut(id, "-")
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if !ok || err != nil || len(id) > 4096 {
		notFound(w)
		return
	}
	path, err := s.box.Open(raw, mediaAAD(uid))
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		notFound(w) // another user's id, a forged one, or a stale key
		return
	}
	b, _, err := s.gate(r.Context(), uid)
	if err != nil || s.media == nil {
		notFound(w)
		return
	}
	data, mt, err := s.media.Media(r.Context(), b.Profile, path)
	switch {
	case errors.Is(err, hermes.ErrMediaNotFound):
		notFound(w)
		return
	case err != nil:
		apiError(w, http.StatusBadGateway, "HERMES_UNAVAILABLE", "The assistant's media could not be read right now")
		return
	case !mediaType(mt):
		notFound(w)
		return
	}
	h := w.Header()
	h.Set("Content-Type", mt)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store") // re-checked on every read: never reused after a sign-out or suspension
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

// streamMedia takes the MEDIA: tags out of a reply as it streams. A tag can arrive split over deltas, so the tail of
// the text that may still be one (an unfinished tag, or the start of "MEDIA:") is held back and returned as the new
// hold; the rest goes out without tags. A tag that grows past 8 KiB unfinished is cut down to its opening, so the hold
// stays small and the tag's remaining pieces are still stripped.
func streamMedia(hold, delta string) (out, rest string) {
	s := hold + delta
	cut := len(s)
	// Left to right over the tags: a complete one is stepped over whole (a "MEDIA:" inside a quoted path is part of
	// it), the first unfinished one starts the hold.
	for i := 0; ; {
		j := strings.Index(s[i:], "MEDIA:")
		if j < 0 {
			break
		}
		start := i + j
		tail := s[start+len("MEDIA:"):]
		var end int
		if tail != "" && (tail[0] == '"' || tail[0] == '\'') {
			end = strings.IndexAny(tail[1:], tail[:1]+"\n")
			if end >= 0 {
				end += 2 // the opening and closing quote
			}
		} else {
			end = strings.IndexFunc(tail, unicode.IsSpace)
		}
		if end < 0 {
			cut = start
			break
		}
		i = start + len("MEDIA:") + end
	}
	if cut == len(s) {
		for k := len("MEDIA:") - 1; k > 0; k-- {
			if strings.HasSuffix(s, "MEDIA:"[:k]) {
				cut = len(s) - k
				break
			}
		}
	}
	rest = s[cut:]
	if len(rest) > 8<<10 && strings.HasPrefix(rest, "MEDIA:") {
		// An oversized unfinished tag: drop what arrived of it, keep only its opening, so the pieces still to come are
		// still recognised as the same tag and stripped (the hold stays bounded).
		rest = "MEDIA:"
		if q := s[cut+len("MEDIA:"):]; q != "" && (q[0] == '"' || q[0] == '\'') {
			rest += q[:1]
		}
	}
	return stripMediaTags(s[:cut]), rest
}

// stripMediaTags removes every MEDIA:<…> tag from text (for the live stream, the search index and its snippets).
func stripMediaTags(text string) string {
	if !strings.Contains(text, "MEDIA:") {
		return text
	}
	return mediaTag.ReplaceAllString(text, "")
}

func mediaType(mt string) bool {
	return strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "audio/") || strings.HasPrefix(mt, "video/")
}
