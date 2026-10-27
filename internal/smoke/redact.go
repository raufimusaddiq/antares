package smoke

import "github.com/raufimusaddiq/antares/internal/redact"

// Redactor removes credentials from every string that leaves the tool: the session values it was given, exactly, and
// every credential-shaped value (the same patterns the app's log uses, internal/redact).
type Redactor = redact.Known
