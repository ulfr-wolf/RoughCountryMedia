// Package web holds the embedded browser UI.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS returns the UI files rooted at static/.
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
