// Package web embeds the built React app (customer /menu and staff /admin/) into the binary.
//
// The release build copies frontend/dist into ui/ before `go build` (scripts/build-release.*).
// A plain `go build` embeds only ui/.gitkeep: the API works, and Ready reports false.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:ui
var files embed.FS

// UI returns the embedded frontend build rooted at its top directory.
func UI() fs.FS {
	sub, err := fs.Sub(files, "ui")
	if err != nil {
		panic(err) // the directive above guarantees ui/ exists
	}
	return sub
}
