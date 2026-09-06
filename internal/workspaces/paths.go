package workspaces

import (
	"os"
	"path/filepath"

	"github.com/gs-sinha/sapien/internal/domain"
)

// workspaceFile turns a workspace directory into the path workspace.Load
// wants. A caller that already has the file path (rather than its directory)
// is passed through unchanged, so both forms work as a selector.
func workspaceFile(dir string) string {
	if filepath.Base(dir) == domain.WorkspaceFileName {
		return dir
	}
	return filepath.Join(dir, domain.WorkspaceFileName)
}

func filepathBase(dir string) string { return filepath.Base(dir) }

func selfPID() int { return os.Getpid() }
