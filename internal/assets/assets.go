// Package assets owns the embedded frontend bundle.
//
// It exists because //go:embed cannot reach upward out of its own package
// directory: a binary under cmd/ can never embed ../../frontend/dist. Vite
// therefore writes its build here (frontend/vite.config.ts sets
// build.outDir), one package embeds it, and every entry point serves a
// byte-identical UI from the same source.
package assets

import (
	"embed"
	"io/fs"
)

// FS is the bundle as embedded, rooted one level above index.html.
//
// Wails wants this shape: application.AssetFileServerFS walks the tree to
// locate index.html and subs to whatever directory holds it, so the wrapping
// directory name is not load-bearing for the desktop panel.
//
// all: is required — Vite emits files the default embed pattern would skip,
// and a missing font or icon does not fail the build, it 404s at runtime.
//
//go:embed all:dist
var FS embed.FS

// Dist returns the bundle rooted at index.html, which is what the web
// dashboard serves. The error is impossible for a well-formed embed and is
// returned rather than panicked so a boot path can report it like any other
// startup failure.
func Dist() (fs.FS, error) {
	return fs.Sub(FS, "dist")
}
