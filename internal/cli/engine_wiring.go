package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/engine/remote"
	"github.com/gs-sinha/sapien/internal/errs"
)

// noDaemonEnv, when set to "1", forces every one-shot CLI invocation (and
// `sapien mcp`, see mcp.go) to use engine.Local even if a live daemon is
// running for the workspace. Tests and CI set this so a run never depends
// on, or interferes with, a background daemon process left over from a
// previous invocation.
const noDaemonEnv = "SAPIEN_NO_DAEMON"

// init wires App.Engine (via the NewEngine seam declared in app.go) to
// PLAN §4's "one-shot CLI" rule: a live, version-matching daemon for the
// workspace is used over HTTP (engine.Remote); otherwise the engine runs
// in-process (engine.Local). This never starts a daemon -- only `sapien
// serve` (serve.go) and `sapien mcp` (daemonctl.go's spawnDaemon) do that.
func init() {
	NewEngine = func(ws *domain.Workspace) (engine.Engine, error) {
		if os.Getenv(noDaemonEnv) == "1" {
			slog.Default().Debug("engine selected", "engine", "local", "reason", noDaemonEnv+"=1")
			return local.Open(ws, local.Options{})
		}

		info, err := daemon.Find(context.Background(), ws, Version)
		if err != nil {
			if errs.CodeOf(err) != errs.Conflict {
				return nil, err
			}
			// A live daemon from another build (the usual case: `make
			// build` while an agent session kept the old daemon alive).
			// PLAN §4 first had the CLI report it and hint at `sapien serve
			// --restart`; real use showed that is exactly the command the
			// developer runs next anyway, so do the safe half of it here:
			// stop the stale daemon so two builds never share the database,
			// then run in-process. The CLI still never starts a daemon; the
			// next `sapien mcp` or `sapien serve` does, with this build.
			slog.Default().Debug("stopping daemon from another build", "error", err.Error())
			if rerr := replaceStaleDaemon(context.Background(), ws, Version); rerr != nil {
				return nil, errs.As(err).WithHint("could not stop it (" + rerr.Error() + "); run `sapien serve --restart`")
			}
			slog.Default().Debug("engine selected", "engine", "local", "reason", "stale daemon stopped")
			return local.Open(ws, local.Options{})
		}
		if info == nil {
			slog.Default().Debug("engine selected", "engine", "local", "reason", "no daemon running")
			return local.Open(ws, local.Options{})
		}

		slog.Default().Debug("engine selected", "engine", "remote", "pid", info.PID, "port", info.Port)
		return remote.New(fmt.Sprintf("http://127.0.0.1:%d", info.Port), info.Token)
	}
}
