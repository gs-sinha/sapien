# @gs-sinha/sapien

npm wrapper around the [sapien](https://github.com/gs-sinha/sapien) binary.
Sapien is a local-first, agent-native API workspace engine, written in Go
and shipped as a single static binary — this package exists only so MCP
hosts and quick scripts that assume `npx` can launch it without a separate
install step.

## Usage

```sh
npx @gs-sinha/sapien version
npx @gs-sinha/sapien mcp --workspace .
```

The first invocation downloads the matching `sapien` release binary for
your OS/arch from GitHub Releases into `~/.cache/sapien-npm/<version>/` and
verifies its checksum; later invocations reuse the cached binary. Every
argument is passed straight through to `sapien`, with stdio inherited, so
this behaves exactly like calling `sapien` directly.

If you already have a `sapien` binary (a local build, or one installed by
Homebrew or `scripts/install.sh`), point at it instead of downloading a
second copy:

```sh
SAPIEN_BINARY=/usr/local/bin/sapien npx @gs-sinha/sapien mcp --workspace .
```

## MCP host configuration

Most MCP hosts (Claude Code, Codex, Cowork, or any generic host reading an
`mcpServers` block) can point directly at `npx` instead of a fixed binary
path — useful when you don't want to install `sapien` separately on every
machine that runs the host:

```json
{
  "mcpServers": {
    "sapien": {
      "command": "npx",
      "args": ["-y", "@gs-sinha/sapien", "mcp", "--workspace", "/path/to/workspace"]
    }
  }
}
```

See [`docs/mcp.md`](https://github.com/gs-sinha/sapien/blob/main/docs/mcp.md)
in the main repo for the full host setup guide, including the
`sapien mcp config --client <name>` helper that prints this snippet (and
the binary-path equivalent) for you.

## Environment variables

| Variable | Effect |
|---|---|
| `SAPIEN_BINARY` | Use this binary instead of downloading one. |
| `SAPIEN_VERSION` | Download this release version (default: this package's own version, with a `v` prefix). |
| `SAPIEN_CACHE_DIR` | Cache directory (default: `~/.cache/sapien-npm`). |

## Notes

- This package has no npm dependencies. Archive extraction shells out to
  the system `tar` (present on macOS, Linux, and Windows 10 1803+, which
  ships `bsdtar` — it reads both `.tar.gz` and `.zip`).
- `postinstall` best-effort pre-downloads the binary so the first real
  invocation doesn't pay the download cost; it never fails `npm install`.
- The published version tracks sapien release tags: version `X.Y.Z` here
  downloads release `vX.Y.Z`.
