package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// POST /memory {"slug": ...}: what the assistant remembers in one profile, read only (owner decision 2026-11-01, #248;
// Hermes v0.22.5 has no memory route). Hermes keeps it in memories/USER.md (about the user) and memories/MEMORY.md (its
// own notes); entries are split with Hermes' own parser, so the list matches what the agent loads. Nothing is written:
// Hermes' loader would create the memories directory, so the files are read directly instead. A profile that does not
// exist is a 404; a profile with no memory yet is two empty lists.

const (
	maxMemoryFile  = 64 << 10 // Hermes caps them near 2 KB; a hand-edited file may be larger, never this large
	maxMemoryEntry = 4000
	maxMemoryItems = 500
	maxMemoryOut   = 256 << 10
)

// memoryScript runs in the Hermes container as the hermes user. From the trusted profiles directory it opens the
// profile, memories/ and each file relative to the one before with O_NOFOLLOW, so a symlink anywhere is refused, never
// followed. argv: profiles directory, slug, byte limit. Exit 3: no such profile. A file that is not a regular file, or
// is larger than the limit (only a hand edit could make it so), does not fail the read: its list is marked partial, with
// the whole entries that fit.
const memoryScript = `
import json, os, stat, sys
try:
    from tools.memory_tool_store import MemoryStore
    parse = MemoryStore._parse_entries
except Exception:  # a private Hermes name: if it moves, split the same way rather than fail the panel
    parse = lambda raw: [e for e in (x.strip() for x in raw.split("\n\u00a7\n")) if e]
base, slug, limit = sys.argv[1], sys.argv[2], int(sys.argv[3])
D = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
try:
    root = os.open(base, os.O_RDONLY | os.O_DIRECTORY)
    pfd = os.open(slug, D, dir_fd=root)
except OSError:
    sys.exit(3)
out = {"user": [], "memory": [], "partial": []}
try:
    mfd = os.open("memories", D, dir_fd=pfd)
except FileNotFoundError:
    mfd = None
for key, name in (("user", "USER.md"), ("memory", "MEMORY.md")):
    if mfd is None:
        continue
    try:
        ffd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=mfd)
    except FileNotFoundError:
        continue
    st = os.fstat(ffd)
    if not stat.S_ISREG(st.st_mode):
        os.close(ffd)
        out["partial"].append(key)  # not a file Hermes wrote: shown as unreadable, never followed
        continue
    with os.fdopen(ffd, "rb") as f:
        data = f.read(limit + 1)
    if len(data) > limit:
        data = data[:limit]
        out["partial"].append(key)
    text = data.decode("utf-8-sig", errors="replace")
    if key in out["partial"]:
        text = text.rsplit("\n\u00a7\n", 1)[0]  # drop the entry the cut went through
    out[key] = list(dict.fromkeys(parse(text)))
sys.stdout.write(json.dumps(out))
`

// memoryCmd runs the script from the Hermes code root, so Hermes' own parser is the one imported.
const memoryCmd = `cd /opt/hermes && exec "$1" -c "$2" "$3" "$4" "$5"`

func (s *server) memory(w http.ResponseWriter, r *http.Request) {
	slug, _, _, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := opCtx(r) // a read: no mutation lock
	defer cancel()
	out, _, err := s.h.r.Run(ctx, "", "sh", "-c", memoryCmd, "sh", hermesPython, memoryScript, profilesDir, slug, strconv.Itoa(maxMemoryFile))
	var ee interface{ ExitCode() int }
	switch {
	case err == nil:
	case errors.As(err, &ee) && ee.ExitCode() == 3:
		writeErr(w, http.StatusNotFound, errNoProfile.Error())
		return
	default:
		writeErr(w, http.StatusBadGateway, "reading the profile's memory failed")
		return
	}
	var d struct {
		User    []string `json:"user"`
		Memory  []string `json:"memory"`
		Partial []string `json:"partial"`
	}
	if len(out) > maxMemoryOut || json.Unmarshal([]byte(out), &d) != nil {
		writeErr(w, http.StatusBadGateway, "the memory reader answered in an unexpected shape")
		return
	}
	partial := []string{}
	for _, k := range d.Partial {
		if k == "user" || k == "memory" {
			partial = append(partial, k)
		}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"user": clipEntries(d.User), "memory": clipEntries(d.Memory), "partial": partial})
}

func clipEntries(in []string) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		if len(out) >= maxMemoryItems {
			break
		}
		if r := []rune(e); len(r) > maxMemoryEntry {
			e = string(r[:maxMemoryEntry])
		}
		out = append(out, e)
	}
	return out
}
