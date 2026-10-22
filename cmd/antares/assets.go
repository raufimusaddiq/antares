package main

import (
	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/webui"
)

// newServer serves the React bundle embedded in the binary. ANTARES_STATIC_DIR
// replaces it for frontend development only.
func newServer(ready server.Checker, staticDir string) *server.Server {
	if staticDir != "" {
		return server.New(ready, staticDir)
	}
	return server.New(ready, "").WithAssets(webui.FS())
}
