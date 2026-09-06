// Command sapien is Sapien's one-shot CLI, serve daemon, and MCP bridge
// entrypoint (PLAN §1). Today it wires only the CLI; sapien serve/mcp land
// once internal/engine has a Local implementation.
package main

import (
	"os"

	"github.com/growsimplee/sapien/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
