package auth

import (
	"net/http"
	"time"

	"github.com/raufimusaddiq/antares/internal/obs"
	"github.com/raufimusaddiq/antares/internal/store"
)

var started = time.Now()

// GET /api/admin/metrics (issue #114, PRD §13.2). Admin only, and aggregates only: counts from the database and the
// running process's counters (streams, denials, upstream latency since start). No id, name, title or content.
func (a *Auth) adminMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := store.ReadMetrics(r.Context(), a.db, a.now())
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Could not read the metrics")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"database":     m,
		"process":      obs.Snapshot(),
		"processSince": started.UTC().Format(time.RFC3339),
	})
}
