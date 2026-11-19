// Package web embeds the built React dashboard (web/dist) into lpsd.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built frontend, rooted at dist/.
// Before `npm run build` it contains only .gitkeep and lpsd serves a hint page.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
