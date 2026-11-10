// Package writing holds the built-in writing rules (specs/005-antislop-writing): one text, versioned, sent as run
// instructions. It condenses the copywriting rules of anti-slop (MIT, Copyright (c) 2026 Miqdad Badjuber (antislop)).
package writing

import (
	_ "embed"
	"errors"
	"strings"
)

// Version is the label recorded on a run or check that carried the rules. Changing the text means a release that bumps it.
const Version = "v1"

// MaxChars is the most the text may be (it rides on every chat run).
const MaxChars = 3000

//go:embed rules.md
var embedded string

// text is read through a variable so a test can replace it (FR-009: the rules cannot be loaded).
var text = embedded

// Text is the rules text, "" when it fails Check.
func Text() string {
	if Check() != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

// Check reports why the text must not be sent: missing, wrong header, no licence credit, or too long.
func Check() error {
	t := strings.TrimSpace(text)
	switch {
	case t == "":
		return errors.New("writing rules: text is missing")
	case !strings.HasPrefix(t, "writing-rules "+Version):
		return errors.New("writing rules: text does not start with its version line")
	case !strings.Contains(t, "MIT") || !strings.Contains(t, "Miqdad Badjuber (antislop)"):
		return errors.New("writing rules: licence credit is missing")
	case len([]rune(t)) > MaxChars:
		return errors.New("writing rules: text is over 3,000 characters")
	}
	return nil
}

// SetTextForTest replaces the text until the returned function is called. Tests only.
func SetTextForTest(s string) (restore func()) {
	old := text
	text = s
	return func() { text = old }
}
