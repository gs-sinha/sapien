# Getting started

Sapien indexes a set of API service repos into a searchable catalog, runs
flows against them, and captures the operational knowledge an agent or
human picks up along the way. This walks through the CLI end to end.

## Install

From source, which is the reliable route until a tagged release ships
(needs Go 1.25+):

```sh
git clone https://github.com/gs-sinha/sapien.git && cd sapien
make build                                        # writes bin/sapien
ln -s "$PWD/bin/sapien" /usr/local/bin/sapien     # or add bin/ to PATH
```

Once a release is tagged:

```sh
brew install gs-sinha/tap/sapien                # Homebrew
curl -fsSL https://raw.githubusercontent.com/gs-sinha/sapien/main/scripts/install.sh | sh
npx @gs-sinha/sapien version                     # no install, via npx
go install github.com/gs-sinha/sapien/cmd/sapien@latest
```

Confirm it works:

```sh
sapien version
```

## Create a workspace

A workspace groups the services you work with, plus the environments,
flows, and memories that describe how to call them.

```sh
sapien init ~/my-workspace
cd ~/my-workspace
```

Keep one workspace per system, holding every service the team calls
together, from however many repos: search, `get_context`, memories, and
flows all span a workspace, and the MCP entry binds one workspace. A
second workspace is for an unrelated system, not for a second repo.

`init` creates the folder if it does not exist yet, and writes a
committable `sapien.workspace.yaml`, `flows/`, `memories/`,
`environments/local.yaml`, and a gitignored `.sapien/` directory for the
local index and daemon state. `--name` sets the workspace name
explicitly (default: the directory name). Your services' docs and
contracts do not need to exist yet; they belong to the service repos and
are registered next.

## Working with more than one workspace

A second workspace is for an unrelated system, and Sapien can hold
several at once. Commands pick one in this order: `--workspace`, then
`$SAPIEN_WORKSPACE`, then the nearest `sapien.workspace.yaml` at or above
the current directory, and finally the default workspace.

```sh
sapien workspace list          # every registered one; * is this one
sapien workspace current       # what this directory resolves to
sapien workspace use platform  # set the default, by name or path
sapien workspace add ~/other   # register one you already have
sapien workspace forget ~/old  # unregister (the directory is untouched)
```

`sapien init` registers what it creates, and so does opening a workspace
with `--workspace`, so the list mostly fills itself in. `use` sets the
*fallback*, not an override: inside a workspace, that workspace still
wins.

One `sapien serve` serves every registered workspace, opening each on
first use, so the UI's picker (top of the nav) and MCP's
`switch_workspace` both switch without restarting anything. Each
workspace keeps its own database, index, and daemon lock — a catalog,
flow, memory, or run belongs to exactly one workspace, and an id from one
never resolves in another.

## Install the MCP server everywhere

`sapien mcp config --client <name> --write` installs the MCP entry for
an agent host so it can talk to this workspace. The command needs to
find a workspace: run it inside one, or pass `--workspace <dir>`.

```sh
sapien mcp config --client claude-code --write
```

It defaults to `--scope user`, so the `sapien` MCP server becomes
available in every session that client runs on the machine, in any
repo, because the entry bakes in the workspace's absolute path: `sapien
mcp --workspace /abs/workspace`. `--scope local` ties it to
the directory the command is run in instead; `--scope project` writes a
`.mcp.json` in that directory for the team to commit. It resolves the
running binary's own absolute path, so it works even when `sapien` is a
symlink, for example `/usr/local/bin/sapien -> <repo>/bin/sapien`.
Re-running `--write` for `claude-code` replaces an existing `sapien`
entry instead of failing.

Supported clients:

- `claude-code` runs `claude mcp add --scope <scope> sapien -- <abs
  sapien> mcp --workspace <abs workspace>`.
- `codex` appends an entry to `~/.codex/config.toml`.
- `cursor` merges an `mcpServers.sapien` entry into
  `~/.cursor/mcp.json`, creating the file if it does not exist.
- `cowork` merges the entry into Claude Desktop's
  `claude_desktop_config.json`, which Cowork reads; restart Claude Desktop.
- `generic` prints an `mcpServers` JSON block for you to paste into the
  host's own config.

Codex and Cursor configs are global by nature, so both are available
everywhere too, the same as `claude-code` with `--scope user`.

Verify with:

```sh
claude mcp list
```

and, in a new Claude Code session in any directory, ask it to "list the
Sapien services".

## Register services

Most services are not documented well enough for `service add` alone to
produce a useful catalog entry, since it only reads what already sits on
disk. The recommended way to onboard a service that has no `api/`
package yet is to open an agent, Claude Code, Codex, or Cursor, in that
service's own repo and ask it to onboard the service to Sapien. Because
the MCP server is installed everywhere, as covered above, the agent
already has Sapien's tools available there. It reads the code, writes
the `api/` package, and calls the MCP tool `add_service` with the repo's
absolute path, which does the same work as `service add` below. See
[`onboarding.md`](onboarding.md) for the full walkthrough.

When a service already has an `api/openapi.yaml` (plus an optional
`api/service.yaml`, `api/docs/**`, `api/flows/*.flow.yaml`), `service
add` reads and indexes it directly. Point it at a local checkout or a
git remote:

```sh
# local: a repo you already have checked out
sapien service add ../order-service

# git: cloned and kept in a managed cache under ~/.sapien/repos
sapien service add git@github.com:company/allocation-service.git \
  --ref main --subdir api

# override discovery, or set an explicit name
sapien service add ../rider-service --name riders --contract openapi/rider.yaml
```

A location is treated as a git remote when it has a `http://`,
`https://`, `ssh://`, `git://`, or `file://` scheme, or looks like
`user@host:path`; anything else is a local path. A relative local path resolves against the current
directory, the same as any other shell command; a path that lands
inside the workspace folder is stored relative to it, and one outside is
stored absolute. Running `sapien service add "$PWD" --workspace
/abs/workspace` from inside the service repo is the shell equivalent of
the agent's `add_service` call.

`service list` shows what's registered, `service sync [name]` re-reads a
source and reindexes it (every service if `name` is omitted), and
`service remove <name>` unregisters one.

```sh
sapien service list
sapien service sync                 # re-index everything
sapien service remove riders
```

## Find and inspect operations

```sh
sapien search "allocate rider"
sapien search "allocate rider" --service allocation-service --method POST
sapien search "polling delay" --docs           # search documentation instead

sapien describe allocation-service.allocate
sapien describe allocation-service.allocate --fields --examples
```

`describe` accepts an operation ID (`<service>.<operationId>`) or, when
that's unambiguous, `METHOD /path`. `docs list`, `docs search <query>`,
and `docs show <service> <path> [--section <heading>]` browse narrative
documentation the same way.

## Call an operation directly

`sapien call` makes one request as a one-step run — useful for poking at
an API before writing a flow:

```sh
sapien call allocation-service.allocate \
  --body '{"orderId":"ord_1"}'

sapien call rider-service.getRider -p riderId=R123

sapien call order-service.createOrder \
  --body @order.json \
  -H 'X-Debug: 1' \
  --save-as smoke-create        # writes smoke-create.flow.yaml
```

`-p key=val` binds a path/query/header parameter (repeatable, JSON-decoded
when it parses); `--body` takes inline JSON or `@file`; `-H` adds an extra
header. The exit code follows the run's outcome (0 pass, 1 assertion
failure, 2 error) — see [`flows.md`](flows.md) for the full table.

## Environments and secrets

Environments (`environments/<name>.yaml`, committable) hold base URLs,
non-secret vars, and auth config; `sapien.workspace.yaml` names a
`default_environment`.

```sh
sapien env list
sapien env show staging
sapien env use staging              # sets the workspace default
```

A service's own `service.yaml` can declare `environments: { <name>: {
base_url } }` hints, but those hints are only advice: at call time it is
always the workspace's own `environments/<name>.yaml` file that decides a
service's base URL, and a hint by itself does not make that environment
runnable. A fresh workspace only has `environments/local.yaml` (created by
`sapien init`), so running against any other environment name fails until
a file for it exists too. The error says which registered services declare
that name, if any, and points at the command below.

`sapien env scaffold` closes that gap: it reads every registered service's
`service.yaml` hints and creates or updates `environments/<name>.yaml` for
each name any service declares, filling in `services.<name>.base_url` for
services that have no entry yet (or an empty one). A newly created file is
marked `production: true` when the name starts with "prod" or contains
"production", `false` otherwise. It never overwrites an existing, different
base_url unless you pass `--force`; everything else already in the file
(vars, auth, transport, entries for other services) is left untouched.

```sh
sapien service add ./services/rider-service   # its service.yaml declares a "stage" hint
sapien env scaffold                            # creates environments/stage.yaml from that hint
sapien env scaffold --force                    # also overwrites conflicting base_urls
```

Once an environment file exists, `sapien env probe [name]` (default: the
workspace default environment) checks that it is actually reachable before
you run anything for real: it sends a GET to every service's base_url and
prints the HTTP status (or the connection error) and latency for each. It
honors the environment's `transport.timeout_ms` and `transport.insecure_tls`
settings unless you pass `--timeout` explicitly, and it exits 0 only when
every service answered with some HTTP response, 1 if any failed to
connect.

```sh
sapien env probe staging
sapien env probe staging --json
sapien env probe                                # the workspace default environment
```

Every command that executes something takes `--env <name>` (falling back
to the workspace default). Secrets referenced as `${secret.NAME}` in an
environment's `auth:` block are never written to a file:

```sh
sapien secret set STAGING_TOKEN --stdin <<< "s3cr3t"
sapien secret list                  # names only; values are never printed
sapien secret rm STAGING_TOKEN
```

Resolution order at call time: process env `SAPIEN_SECRET_<NAME>` (what CI
uses, see [`ci.md`](ci.md)), then the OS keychain. Calling an environment
with `production: true` requires `--allow-production` on `call` and `flow
run`, or the command exits 3 (blocked).

## Open the UI

```sh
sapien ui
```

`sapien ui` finds or starts the workspace's daemon (like `sapien mcp`) and
opens a browser at its `/ui/session` URL, which exchanges the daemon's
bearer token for an HttpOnly session cookie and redirects into the app at
`/ui/`. `--no-open` prints the URL instead of launching a browser; `--json`
prints `{"url", "port"}`.

The UI is served by the daemon itself, not a separate process: it shows
flows, runs (with every step's request, response, and timings), services,
operations, examples, memories, and a live event feed, all updating from
the same event stream the MCP layer and CLI already share. There is no LLM
in it — it is the inspector beside whatever agent or chat client you use
Sapien from.

## Where to next

- [`onboarding.md`](onboarding.md) — onboard an undocumented service with an agent.
- [`flows.md`](flows.md) — chain calls into a flow, assert on responses, run in CI.
- [`memory.md`](memory.md) — capture and retrieve the knowledge you pick up along the way.
- [`examples.md`](examples.md): save known-good requests and replay them in a call or a flow.
- [`mcp.md`](mcp.md) — let Claude Code, Codex, or Cowork author flows and memories for you.
- [`architecture.md`](architecture.md) — how the pieces fit together.
