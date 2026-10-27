// Package redact removes credentials from text that leaves the process: the app's log (installed as the log writer, so
// every log call passes through it) and the smoke tool's report. It is pattern-based: the shapes a session cookie, a
// bearer token, a JWT, a provider key or a key=value secret take. Known exact secrets can be added on top.
package redact

import (
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Marker replaces every removed value.
const Marker = "[redacted]"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(__Host-antares_[A-Za-z0-9_]+=)[^;\s"]+`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|password)["']?\s*[:=]\s*["']?)[^\s"',;]{6,}`),
	regexp.MustCompile(`\b(sk-)[A-Za-z0-9_-]{16,}`),
}

// Clean returns s with every credential-shaped value replaced by Marker.
func Clean(s string) string {
	for _, p := range patterns {
		if p.NumSubexp() > 0 {
			s = p.ReplaceAllString(s, "${1}"+Marker)
		} else {
			s = p.ReplaceAllString(s, Marker)
		}
	}
	return s
}

// Known is Clean plus exact values registered at run time (for instance the sessions a smoke run was given).
type Known struct {
	mu      sync.Mutex
	secrets []string
}

// Add registers a secret value. Very short values are ignored: replacing "ab" everywhere would destroy the text
// without protecting anything.
func (k *Known) Add(s string) {
	s = strings.TrimSpace(s)
	if len(s) < 8 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, have := range k.secrets {
		if have == s {
			return
		}
	}
	k.secrets = append(k.secrets, s)
	sort.Slice(k.secrets, func(i, j int) bool { return len(k.secrets[i]) > len(k.secrets[j]) })
}

// Clean removes every registered value, then every credential-shaped one.
func (k *Known) Clean(s string) string {
	if k == nil {
		return Clean(s)
	}
	k.mu.Lock()
	known := append([]string(nil), k.secrets...)
	k.mu.Unlock()
	for _, v := range known {
		s = strings.ReplaceAll(s, v, Marker)
	}
	return Clean(s)
}

type writer struct{ w io.Writer }

// Writer wraps w so every write is cleaned first. log.SetOutput(redact.Writer(os.Stderr)) makes it the only way a log
// line leaves the process. It reports the length it was given, so callers see a complete write.
func Writer(w io.Writer) io.Writer { return writer{w} }

func (r writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(r.w, Clean(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
