// Package ui embeds and serves the built single-page inspector app (PLAN
// §34c) at /ui/. internal/server mounts Handler() directly; nothing else
// in this repo depends on this package.
//
// internal/ui/dist is committed with a placeholder index.html so `go
// build` and `go test` never require Node. `make ui` (Vite 4 + React 18 +
// TypeScript, built under ui/) overwrites dist with the real app; the
// build is otherwise untouched by this package.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

// distDirFS is distFS re-rooted at dist/, so index.html and assets/* sit
// at the top level instead of under a "dist/" prefix -- the shape
// HandlerFS expects, and the shape fs.Sub always produces for a directory
// embed pattern, so mustSub can only fail if this package's own //go:embed
// directive above stops matching a "dist" directory, a build-time error a
// test FS passed to HandlerFS need not reproduce.
var distDirFS = mustSub(distFS, "dist")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("internal/ui: " + err.Error())
	}
	return sub
}
