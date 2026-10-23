package chat

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Artifacts (PRD §21.8). An artifact is a closed fenced block tagged `html` or
// `svg` in a turn of the caller's own transcript. It has no id of its own: it is
// named by (chat, turn seq, index within the turn) plus the turn's ref, and every read goes through
// the owner-scoped chat lookup and the caller's own Hermes transcript, so a
// reference into someone else's chat is the same 404 as one that never existed.
//
// The text is hostile. It is never parsed or rewritten here. Previews are served
// with a CSP `sandbox` (no scripts, opaque origin, no forms/popups/top
// navigation) and `default-src 'none'`; downloads are attachments.
//
// The fence rules and index order must match web/src/artifacts.ts.

const (
	maxArtifactBytes = 512 << 10
	maxArtifacts     = 32
)

var artifactOpen = regexp.MustCompile("^\\s{0,3}(`{3,})\\s*([A-Za-z0-9_-]*)\\s*$")

type artifact struct{ kind, body string }

func findArtifacts(text string) []artifact {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text), "\n")
	var out []artifact
	for i := 0; i < len(lines); {
		m := artifactOpen.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		closeRE := regexp.MustCompile("^\\s{0,3}`{" + strconv.Itoa(len(m[1])) + ",}\\s*$")
		j := i + 1
		for j < len(lines) && !closeRE.MatchString(lines[j]) {
			j++
		}
		if j >= len(lines) { // unterminated: still streaming, not an artifact
			break
		}
		kind := strings.ToLower(m[2])
		body := strings.Join(lines[i+1:j], "\n")
		if (kind == "html" || kind == "svg") && len(out) < maxArtifacts && len(body) <= maxArtifactBytes {
			out = append(out, artifact{kind, body})
		}
		i = j + 1
	}
	return out
}

// artifactFor resolves a reference for the caller, or writes the refusal.
func (s *Service) artifactFor(w http.ResponseWriter, r *http.Request) (artifact, int, int, bool) {
	uid, _ := user(r)
	id, ok := pathID(r, "chatId")
	seq, e1 := strconv.Atoi(r.PathValue("seq"))
	n, e2 := strconv.Atoi(r.PathValue("n"))
	if !ok || e1 != nil || e2 != nil || seq < 0 || n < 0 {
		notFound(w)
		return artifact{}, 0, 0, false
	}
	c, err := store.GetChat(r.Context(), s.db, uid, id)
	if errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return artifact{}, 0, 0, false
	} else if err != nil {
		internalError(w, "get chat", err)
		return artifact{}, 0, 0, false
	}
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		s.gateError(w, err)
		return artifact{}, 0, 0, false
	}
	all, err := s.hx.Transcript(r.Context(), b, c.HermesSessionID)
	if err != nil {
		upstreamError(w, err)
		return artifact{}, 0, 0, false
	}
	// seq alone is a position, and compaction moves positions: the caller must also quote
	// the turn's hash (the ref the history API stamped on it), so a stale seq is a 404
	// rather than another turn's artifact.
	ref := r.URL.Query().Get("ref")
	for _, m := range all {
		if m.Seq != seq {
			continue
		}
		if m.Role == "assistant" && ref != "" && ref == turnHash(c.ID, m) {
			if as := findArtifacts(m.Text); n < len(as) {
				return as[n], seq, n, true
			}
		}
		break
	}
	notFound(w)
	return artifact{}, 0, 0, false
}

func (s *Service) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	a, seq, n, ok := s.artifactFor(w, r)
	if !ok {
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="antares-turn%d-artifact%d.%s"`, seq, n+1, a.kind))
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(a.body))
}

// previewCSP: `sandbox` with no tokens gives the document an opaque origin with
// no scripts, forms, popups, top navigation or storage, even if it is opened
// directly instead of in the page's iframe. default-src 'none' stops it fetching.
const previewCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; frame-ancestors 'self'"

func (s *Service) previewArtifact(w http.ResponseWriter, r *http.Request) {
	a, _, _, ok := s.artifactFor(w, r)
	if !ok {
		return
	}
	h := w.Header()
	if a.kind == "svg" {
		h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	} else {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
	h.Set("Content-Security-Policy", previewCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(a.body))
}
