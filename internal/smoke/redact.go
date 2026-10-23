// Package smoke is the deploy gate (issue #53): one scenario that does what a
// user does against a deployed Antares, over HTTPS, and says which step failed.
//
// It is a client. It holds no Hermes credential and never calls Hermes; every
// answer it checks comes through Antares' public API.
package smoke

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

const redacted = "[redacted]"

// Redactor removes credentials from every string that leaves the tool. Values it
// was told about (session cookies) are replaced exactly; the patterns catch the
// shapes a credential takes if something unexpected echoes one back.
type Redactor struct {
	mu      sync.Mutex
	secrets []string
}

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(__Host-antares_[A-Za-z0-9_]+=)[^;\s"]+`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|password)["']?\s*[:=]\s*["']?)[^\s"',;]{6,}`),
}

// Add registers a secret value. Very short values are ignored: replacing "ab"
// everywhere would destroy the report without protecting anything.
func (r *Redactor) Add(s string) {
	s = strings.TrimSpace(s)
	if len(s) < 8 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, have := range r.secrets {
		if have == s {
			return
		}
	}
	r.secrets = append(r.secrets, s)
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

// Clean returns s with every known secret and credential-shaped value removed.
func (r *Redactor) Clean(s string) string {
	if r == nil {
		return s
	}
	r.mu.Lock()
	known := append([]string(nil), r.secrets...)
	r.mu.Unlock()
	for _, k := range known {
		s = strings.ReplaceAll(s, k, redacted)
	}
	for i, re := range patterns {
		if i == 2 { // a bare JWT has no prefix group to keep
			s = re.ReplaceAllString(s, redacted)
			continue
		}
		s = re.ReplaceAllString(s, "${1}"+redacted)
	}
	return s
}
