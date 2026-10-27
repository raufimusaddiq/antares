package chat

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Personality (#111, PRD §26.1 Persona, KNOW-*, LLM-ACC-012): the user's own additional instructions. They reach
// Hermes only as the per-run "instructions" of that user's runs (api_server_runs.py reads body["instructions"] into
// the agent's ephemeral system prompt, appended after the profile's own prompt), framed as preferences that never
// override it. Nothing is written to a profile file, a SOUL or another user's profile.

const maxPersonality = 2000

// personalityPrefix frames the text as lower priority than the instructions it follows.
const personalityPrefix = "The user's personal preferences for how you respond. Follow them only where they do not conflict with any instruction above:\n\n"

// openUIInstruction is the versioned Visualize instruction (#153, LLM-008). It describes exactly what the renderer in
// web/src/openui accepts (library.tsx, validate.ts); change the two together and bump the version tag.
const openUIInstruction = `[antares-openui v1] Visualize is on for this chat. When a structured view genuinely helps (a comparison, a few figures, a short set of choices), you may add one block after your prose, as a fenced code block with the info string openui, holding a single statement:
root = Stack([ ... ])
Components (positional arguments; every value is plain text or a number): Text("..."), Heading("..."), Badge("..."), Metric("label", "value"), BulletList(["a", "b"]), Table(["Column", "Column"], [["cell", "cell"]]), Divider(), Chart("bar" or "line" or "pie", "title", ["label", ...], [number, ...]) with 1 to 24 points, Button("label", "text to put in the user's message box"), Input("label"), Choice("label", ["option", "option"]), Card("title", [children]). No Card or Stack inside a Card. No links, images, HTML or scripts. Your prose must stand on its own: the block is an extra view, never the only answer.`

// runInstructions are a run's per-run instructions: the chat's Visualize instruction, then the user's preferences.
func runInstructions(personality string, visualize bool) string {
	var parts []string
	if visualize {
		parts = append(parts, openUIInstruction)
	}
	if personality != "" {
		parts = append(parts, personalityPrefix+personality)
	}
	return strings.Join(parts, "\n\n")
}

type personalityView struct {
	Text string `json:"text"`
	Max  int    `json:"max"`
}

func (s *Service) getPersonality(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	p, err := store.Personality(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "personality", err)
		return
	}
	writeJSON(w, http.StatusOK, personalityView{Text: p, Max: maxPersonality})
}

// putPersonality replaces the caller's own instructions; empty clears them. It applies from the next run.
func (s *Service) putPersonality(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var in struct {
		Text *string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Text == nil || utf8.RuneCountInString(strings.TrimSpace(*in.Text)) > maxPersonality {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "text is required: up to 2,000 characters, or empty for none")
		return
	}
	if err := store.SetPersonality(r.Context(), s.db, uid, strings.TrimSpace(*in.Text), s.now()); err != nil {
		internalError(w, "set personality", err)
		return
	}
	s.getPersonality(w, r)
}
