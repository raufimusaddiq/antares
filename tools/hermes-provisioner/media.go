package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// Media Hermes' tools generate (an image, a voice reply, a video) is a file in the profile's own cache, and the runs
// route Antares uses names it only as MEDIA:<path>. /media returns that file's bytes, for that profile only: the path
// must sit under /opt/data/profiles/<slug>/cache/ and have a media extension, and inside the container it must still
// resolve there (no symlink out) and be a regular file under maxMediaBytes.

const maxMediaBytes = 25 << 20

var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif",
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".m4a": "audio/mp4",
	".mp4": "video/mp4", ".webm": "video/webm",
}

// mediaScript prints the file's bytes, or exits 3 when it is not a regular file inside the cache, is too large or
// missing. From the trusted profiles directory it opens the profile, its cache and each further component relative to
// the one before with O_NOFOLLOW (no trailing slash anywhere, so the flag holds for every one of them),
// and checks and reads the opened descriptor: a symlink anywhere on the way (or swapped in while it runs) is refused,
// never followed. The profiles directory, the slug and the path are arguments.
const mediaScript = `
import os, stat, sys
base, slug, path, limit = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
rel = os.path.relpath(path, os.path.join(base, slug, "cache"))
parts = rel.split(os.sep)
if rel.startswith("..") or os.path.isabs(rel) or "" in parts or ".." in parts or "." in parts:
    sys.exit(3)
D = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
try:
    fd = os.open(base, os.O_RDONLY | os.O_DIRECTORY)  # the trusted profiles directory
    for p in [slug, "cache"] + parts[:-1]:
        nfd = os.open(p, D, dir_fd=fd)
        os.close(fd)
        fd = nfd
    ffd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)  # a FIFO never blocks the read
    st = os.fstat(ffd)
    if not stat.S_ISREG(st.st_mode) or st.st_size > limit:
        sys.exit(3)
    with os.fdopen(ffd, "rb") as f:
        sys.stdout.buffer.write(f.read(limit + 1)[:limit])
except OSError:
    sys.exit(3)
`

var errMediaPath = errors.New("not a media file in this profile's cache")

// mediaPath checks the requested path without touching the filesystem and returns its media type.
func mediaPath(slug, path string) (string, error) {
	root := profilesDir + "/" + slug + "/cache/"
	if filepath.Clean(path) != path || !strings.HasPrefix(path, root) {
		return "", errMediaPath
	}
	mt, ok := mediaTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return "", errMediaPath
	}
	return mt, nil
}

func (s *server) media(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req struct {
		Slug string `json:"slug"`
		Path string `json:"path"`
	}
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateSlug(req.Slug); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mt, err := mediaPath(req.Slug, req.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	ctx, cancel := opCtx(r) // a read: no mutation lock
	defer cancel()
	out, _, err := s.h.r.Run(ctx, "", "python3", "-c", mediaScript, profilesDir, req.Slug, req.Path, strconv.Itoa(maxMediaBytes))
	var ee interface{ ExitCode() int }
	switch {
	case err == nil && len(out) > 0:
	case err == nil || (errors.As(err, &ee) && ee.ExitCode() == 3):
		writeErr(w, http.StatusNotFound, errMediaPath.Error()) // refused in the container, or empty
		return
	default:
		writeErr(w, http.StatusBadGateway, "could not read the media in the container") // missing file or a broken exec
		return
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(out))
}
