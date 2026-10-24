package chat

import (
	"context"
	"errors"
	"log"
	"slices"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Run approvals and clarification (PRD §9A.7, issue #104).
//
// When Hermes asks for a decision it blocks that tool call until it is answered; Antares keeps the run's
// stream open, records the prompt in the database and tells the browser. Nothing is decided for the user:
// there is no timer, default or "remember" path that answers a prompt, and a prompt Antares cannot present
// safely fails the run closed (the pre-#104 behaviour) instead of being waved through.

// ErrPromptStale is an owned prompt that is no longer open: answered, settled with its run, or gone upstream.
var ErrPromptStale = errors.New("chat: the prompt is no longer waiting for an answer")

// ErrBadChoice is an approval choice the prompt did not offer, or an answer that is empty or too long.
var ErrBadChoice = errors.New("chat: not an allowed answer")

// approvalChoices are the only decisions a user can send. Hermes also knows "session" and "always"
// (a standing grant); Antares never offers or accepts them, so one click can never widen what a model
// may do for the rest of a session.
var approvalChoices = []string{"once", "deny"}

const maxAnswer = 16 << 10 // Hermes refuses a longer clarification answer

// holdPrompt records a prompt Hermes raised and announces it. It returns false when the prompt cannot be
// answered safely, and the caller then stops the run.
func (s *Service) holdPrompt(ctx context.Context, l *liveRun, userID, runID string, b hermes.Binding, n hermes.Normalized) bool {
	p := store.Prompt{UserID: userID, RunID: runID, Kind: store.PromptApproval, HermesID: n.PromptID, Generation: n.Gen,
		Text: n.Text, Detail: n.Detail, Choices: n.Choices}
	endpoint := "run_approval"
	if n.Kind == hermes.KindClarify {
		p.Kind, endpoint = store.PromptClarify, "run_clarify"
	}
	if p.HermesID == "" || (p.Kind == store.PromptClarify && p.Generation == nil) {
		return false // nothing to answer with
	}
	if p.Kind == store.PromptApproval {
		p.Choices = slices.DeleteFunc(slices.Clone(p.Choices), func(c string) bool { return !slices.Contains(approvalChoices, c) })
		if !slices.Contains(p.Choices, "deny") {
			return false // a user must always be able to refuse
		}
	}
	if ok, err := s.hx.Advertises(ctx, b, endpoint); err != nil || !ok {
		return false // Hermes does not say where an answer goes
	}
	rec, created, err := store.InsertPrompt(s.base, s.db, p, s.now())
	if err != nil {
		log.Printf("chat: run %s: could not record a prompt: %v", runID, err)
		return false
	}
	if created {
		l.emit("run.prompt", viewPrompt(rec), false)
	}
	return true
}

// Respond delivers the user's decision or answer for one of their own open prompts. Ownership comes first
// (a foreign run or prompt is store.ErrNoChat before Hermes is contacted), then the gate, then a claim that
// lets exactly one request send. kind is store.PromptApproval (value is "once" or "deny") or
// store.PromptClarify (value is the answer text).
func (s *Service) Respond(ctx context.Context, userID, runID, promptID, kind, value string) error {
	run, err := store.GetRun(ctx, s.db, userID, runID)
	if err != nil {
		return err
	}
	if store.RunTerminal(run.Status) || run.HermesRunID == "" {
		return ErrPromptStale
	}
	b, _, err := s.gate(ctx, userID)
	if err != nil {
		return err
	}
	p, err := store.ClaimPrompt(ctx, s.db, userID, runID, promptID, kind)
	switch {
	case errors.Is(err, store.ErrPromptNotPending):
		return ErrPromptStale
	case err != nil:
		return err
	}
	release := func(to, decision string) {
		fctx := context.WithoutCancel(ctx)
		if ferr := store.FinishPrompt(fctx, s.db, userID, p.ID, to, decision, s.now()); ferr != nil {
			log.Printf("chat: run %s: could not record a prompt outcome: %v", runID, ferr)
		}
	}
	if (kind == store.PromptApproval && !slices.Contains(p.Choices, value)) || (kind == store.PromptClarify && (value == "" || len(value) > maxAnswer)) {
		release(store.PromptPending, "")
		return ErrBadChoice
	}
	if kind == store.PromptApproval {
		err = s.hx.RespondApproval(ctx, b, run.HermesRunID, p.HermesID, p.Generation, value)
	} else {
		err = s.hx.RespondClarify(ctx, b, run.HermesRunID, p.HermesID, p.Generation, value)
	}
	switch {
	case hermes.CodeOf(err) == hermes.CodePromptStale && err != nil:
		release(store.PromptSettled, "")
		return ErrPromptStale
	case err != nil:
		release(store.PromptPending, "") // not delivered: still open, the user may retry
		return err
	}
	decision := ""
	if kind == store.PromptApproval {
		decision = value
	}
	release(store.PromptAnswered, decision)
	if l := s.liveFor(runID); l != nil {
		l.emit("run.prompt.settled", map[string]any{"promptId": p.ID, "outcome": "answered"}, false)
	}
	return nil
}

type promptView struct {
	ID        string   `json:"promptId"`
	Kind      string   `json:"kind"`
	Text      string   `json:"text"`
	Detail    string   `json:"detail,omitempty"`
	Choices   []string `json:"choices"`
	CreatedAt string   `json:"createdAt"`
}

// viewPrompt never carries the upstream request id or generation: only the server sends those back.
func viewPrompt(p store.Prompt) promptView {
	c := p.Choices
	if c == nil {
		c = []string{}
	}
	return promptView{ID: p.ID, Kind: p.Kind, Text: p.Text, Detail: p.Detail, Choices: c, CreatedAt: p.CreatedAt}
}
