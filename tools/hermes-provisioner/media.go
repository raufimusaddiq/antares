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

// mediaScript prints the file's bytes, or exits 3 when it resolves outside the cache, is not a regular file or is too
// large. The root and the path are arguments.
const mediaScript = `
import os, stat, sys
root, path, limit = sys.argv[1], sys.argv[2], int(sys.argv[3])
real = os.path.realpath(path)
if not real.startswith(root) or not stat.S_ISREG(os.stat(real).st_mode) or os.path.getsize(real) > limit:
    sys.exit(3)
with open(real, "rb") as f:
    sys.stdout.buffer.write(f.read())
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
	out, _, err := s.h.r.Run(ctx, "", "python3", "-c", mediaScript, profilesDir+"/"+req.Slug+"/cache/", req.Path, strconv.Itoa(maxMediaBytes))
	if err != nil || len(out) == 0 {
		writeErr(w, http.StatusNotFound, errMediaPath.Error())
		return
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(out))
}
