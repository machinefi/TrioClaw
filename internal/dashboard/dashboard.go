// Package dashboard serves the embedded web dashboard.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:static
var staticFiles embed.FS

// Handler returns an http.Handler that serves the embedded dashboard files.
func Handler() http.Handler {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("dashboard: embedded static files not found: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}
