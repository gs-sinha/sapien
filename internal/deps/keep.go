//go:build deps

// Package deps pins dependencies that are planned but not yet imported by any
// package, so `go mod tidy` does not drop them between build phases.
package deps

import (
	_ "cel.dev/cel-go/cel"
	_ "github.com/coder/websocket"
	_ "github.com/creack/pty"
	_ "github.com/fsnotify/fsnotify"
	_ "github.com/go-chi/chi/v5"
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
	_ "github.com/zalando/go-keyring"
	_ "golang.org/x/sync/errgroup"
)
