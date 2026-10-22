// Package webui embeds the built React bundle (web/ → internal/webui/dist).
// The bundle is committed so `go build` needs no Node; a test fails when the
// web sources change without a rebuilt bundle. Production runs no Node.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the bundle rooted at its index.html.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("webui: " + err.Error()) // dist is embedded at compile time
	}
	return sub
}
