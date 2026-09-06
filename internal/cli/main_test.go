package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points HOME and SAPIEN_CONFIG at throwaway directories for the
// whole package. Several commands write to the user's own files (`mcp
// config --write` installs host entries under $HOME and records
// default_workspace in the user config), and a test that forgets to isolate
// them rewrites the developer's real configuration -- which happened once
// (a temp workspace ended up as default_workspace and in Claude Desktop's
// config). Individual tests may still override either variable.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "sapien-cli-home-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	os.Setenv("SAPIEN_CONFIG", filepath.Join(home, ".sapien", "config.yaml"))
	os.Setenv("SAPIEN_WORKSPACE", "")
	os.Exit(m.Run())
}
