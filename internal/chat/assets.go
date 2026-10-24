package chat

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Voice staging (PRD §21.4, issue #82). The deployed Hermes has no speech-to-text route
// and no file-input route (docs/hermes/PHASE0-FINDINGS.md: audio_api false, stt and tts
// toolsets disabled), so a staged recording goes nowhere upstream: nothing here calls
// Hermes. These routes only keep an owner's own recording, and only when the user asks.
const (
	maxAudioBytes     = 10 << 20 // one recording
	maxUserAudioBytes = 50 << 20 // everything one account has staged (all asset kinds)
	maxImageBytes     = 8 << 20  // one image (MEDIA-EXT-001)
	maxImagePixels    = 25_000_000
)

// imageOK reports whether b really is the image format mt names and is within the pixel
// budget. Only the header is decoded, so a decompression bomb costs nothing here. WebP is
// not accepted: its dimensions cannot be read without a new dependency.
func imageOK(mt string, b []byte) (bool, bool) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || "image/"+format != mt {
		return false, false
	}
	return true, cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width)*int64(cfg.Height) <= maxImagePixels
}

// audioMagic reports whether b starts like the container its declared type names. A body
// that does not is rejected, so a mislabelled file never becomes a served "audio" asset.
func audioMagic(mt string, b []byte) bool {
	switch mt {
	case "audio/webm":
		return bytes.HasPrefix(b, []byte{0x1A, 0x45, 0xDF, 0xA3})
	case "audio/ogg":
		return bytes.HasPrefix(b, []byte("OggS"))
	case "audio/mp4":
		return len(b) >= 8 && string(b[4:8]) == "ftyp"
	case "audio/mpeg":
		return bytes.HasPrefix(b, []byte("ID3")) || (len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0)
	case "audio/wav":
		return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE"
	}
	return false
}

func (s *Service) uploadAsset(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	img := mt == "image/png" || mt == "image/jpeg" || mt == "image/gif"
	if err != nil || (!knownAudio(mt) && !img) {
		apiError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Only audio/webm, audio/ogg, audio/mp4, audio/mpeg, audio/wav recordings and image/png, image/jpeg, image/gif images can be uploaded")
		return
	}
	limit := int64(maxAudioBytes)
	if img {
		limit = maxImageBytes
	}
	tooBig := func() {
		apiError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The file is larger than the "+strconv.FormatInt(limit>>20, 10)+" MiB limit")
	}
	if r.ContentLength > limit {
		tooBig()
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		tooBig()
		return
	case err != nil:
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The upload was interrupted; nothing was saved")
		return
	case len(body) == 0:
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The file is empty")
		return
	case !img && !audioMagic(mt, body):
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The file is not a valid "+mt+" recording")
		return
	}
	if img {
		real, inBudget := imageOK(mt, body)
		if !real {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "The file is not a valid "+mt+" image")
			return
		}
		if !inBudget {
			apiError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The image is larger than "+strconv.Itoa(maxImagePixels/1_000_000)+" megapixels")
			return
		}
	}
	a, err := store.CreateAsset(r.Context(), s.db, uid, mt, body, maxUserAudioBytes)
	switch {
	case errors.Is(err, store.ErrAssetQuota):
		apiError(w, http.StatusRequestEntityTooLarge, "STORAGE_QUOTA", "Your staged recordings would exceed "+strconv.Itoa(maxUserAudioBytes>>20)+" MiB; delete some first")
	case err != nil:
		internalError(w, "create asset", err)
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "mediaType": a.MediaType, "sizeBytes": a.Size, "createdAt": a.CreatedAt})
	}
}

func knownAudio(mt string) bool {
	switch mt {
	case "audio/webm", "audio/ogg", "audio/mp4", "audio/mpeg", "audio/wav":
		return true
	}
	return false
}

func (s *Service) getAsset(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "assetId")
	if !ok {
		notFound(w)
		return
	}
	a, content, err := store.GetAsset(r.Context(), s.db, uid, id)
	if errors.Is(err, store.ErrNoAsset) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get asset", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.MediaType)
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// Download is the default. ?inline=1 lets the page render the asset in an <img> or an
	// <audio> element, and only for the raster image and audio types upload validated by
	// content: never an HTML or SVG payload. The policy keeps a directly opened asset inert.
	h.Set("Content-Disposition", "attachment")
	if r.URL.Query().Get("inline") == "1" && (strings.HasPrefix(a.MediaType, "image/") || knownAudio(a.MediaType)) {
		h.Set("Content-Disposition", "inline")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}
	// ServeContent answers Range requests, so audio can seek without a second pipeline.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
}

func (s *Service) deleteAsset(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id, ok := pathID(r, "assetId")
	if !ok {
		notFound(w)
		return
	}
	err := store.DeleteAsset(r.Context(), s.db, uid, id)
	switch {
	case errors.Is(err, store.ErrNoAsset):
		notFound(w)
	case err != nil:
		internalError(w, "delete asset", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
