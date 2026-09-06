# Releasing Sapien

A release is a `v*` tag on `main`. Pushing the tag runs the Release
workflow (`.github/workflows/release.yml`), which runs goreleaser
(`.goreleaser.yaml`). The Homebrew tap is still updated by hand; the
steps and the reason are below.

## What the pipeline produces

| Artifact | Where |
|---|---|
| `sapien_<ver>_{darwin,linux}_{amd64,arm64}.tar.gz` (binary, README, LICENSE) | GitHub release `v<ver>` |
| `checksums.txt` (sha256 of the archives) | same release |
| `ghcr.io/gs-sinha/sapien:<ver>` and `:latest`, multi-arch linux/amd64 + linux/arm64 | GitHub Packages |

Windows is not built: `internal/daemon` uses Unix-only syscalls. Add
`windows` back to `goos` in `.goreleaser.yaml` once the daemon is ported.

Archive names are a contract: `scripts/install.sh` and the Homebrew
formula download `sapien_<ver>_<os>_<arch>.tar.gz` and verify it against
`checksums.txt`. Do not rename them.

## Cutting a release

1. On `main`: move the `## [Unreleased]` section of `CHANGELOG.md` to
   `## [X.Y.Z] - YYYY-MM-DD`. If the UI changed, `make ui` and commit the
   rebuilt `internal/ui/dist` (it is embedded in the binary).
2. Gate: `go test -race ./...` green, and the UI suite (`cd ui && npm test`)
   if the UI changed.
3. Push `main` and wait for CI.
4. Tag and push:

   ```sh
   git tag -a vX.Y.Z -m "Sapien vX.Y.Z"
   git push origin vX.Y.Z
   gh run list --workflow Release --limit 1   # then: gh run watch <id>
   ```

5. Verify:

   ```sh
   gh release view vX.Y.Z --json assets --jq '.assets[].name'
   curl -fsSL https://raw.githubusercontent.com/gs-sinha/sapien/main/scripts/install.sh | sh -s -- --prefix /tmp/sapien-check
   /tmp/sapien-check/sapien version
   go install github.com/gs-sinha/sapien/cmd/sapien@latest   # proxy can lag a few minutes after the tag
   ```

   A binary from `go install` reports `sapien dev`: `go install` does not
   pass the version ldflags. Only the archives carry the version string.

## Updating the Homebrew tap (manual)

The tap is https://github.com/gs-sinha/homebrew-tap, one file:
`Formula/sapien.rb`. Users run `brew install gs-sinha/tap/sapien`.

For each release, in a checkout of the tap:

1. `gh release download vX.Y.Z --repo gs-sinha/sapien -p checksums.txt`
2. In `Formula/sapien.rb` set `version "X.Y.Z"`, the four `url` lines to
   `.../releases/download/vX.Y.Z/sapien_X.Y.Z_<os>_<arch>.tar.gz`, and each
   `sha256` from `checksums.txt` (darwin_arm64, darwin_amd64, linux_arm64,
   linux_amd64, in that order in the file).
3. Keep the explicit `version` line. `brew audit` calls it redundant; it is
   not. Without it Homebrew scans the version from the URL and, on the
   arm64 archives, gets `64`, so the install lands in `Cellar/sapien/64`.
4. Commit as `sapien X.Y.Z`, push `main`.
5. Verify without touching the local machine:

   ```sh
   docker run --rm -e HOMEBREW_NO_AUTO_UPDATE=1 homebrew/brew:latest \
     bash -lc 'brew install gs-sinha/tap/sapien && sapien version && brew test sapien'
   ```

   A local `brew install` on macOS needs the Command Line Tools
   (`xcode-select --install`); with Xcode alone Homebrew refuses any formula
   that has no bottle ("Xcode alone is not sufficient").

### Wiring the tap into goreleaser (not done yet)

`.goreleaser.yaml` already has a `brews:` block for the tap with
`skip_upload: true`. To let goreleaser write the formula on every release:

1. Create a fine-grained personal access token with **Contents: read and
   write** on `gs-sinha/homebrew-tap` only.
2. Add it as the repository secret `HOMEBREW_TAP_TOKEN` on `gs-sinha/sapien`
   (the workflow already passes it through).
3. Set `skip_upload: false` in the `brews:` block and commit.

Goreleaser emits an explicit `version` line, so the "64" problem does not
come back. Until these three steps are done, the manual procedure above
is the procedure. `Formula/sapien.rb` in this repo is a reference copy
only; nothing reads it.

## If Actions is unavailable

v1.0.0 was assembled by hand when the hosting org's Actions were locked.
The equivalent of the pipeline, from a clean checkout of the tag:

```sh
VER=X.Y.Z; OUT=/tmp/sapien-release; mkdir -p "$OUT"
for t in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  os=${t%/*}; arch=${t#*/}; d="$OUT/build_${os}_${arch}"; mkdir -p "$d"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X github.com/gs-sinha/sapien/internal/cli.Version=$VER -X github.com/gs-sinha/sapien/internal/cli.Commit=$(git rev-parse --short HEAD) -X github.com/gs-sinha/sapien/internal/cli.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o "$d/sapien" ./cmd/sapien
  cp README.md LICENSE "$d/" && tar -C "$d" -czf "$OUT/sapien_${VER}_${os}_${arch}.tar.gz" sapien README.md LICENSE
done
(cd "$OUT" && shasum -a 256 sapien_${VER}_*.tar.gz > checksums.txt)
gh release create v$VER "$OUT"/sapien_${VER}_*.tar.gz "$OUT/checksums.txt" --title "Sapien v$VER" --notes-file NOTES.md
```

Container images are not covered by the manual path.

## Not wired

- npm: `packages/npm` is set up for `@gs-sinha/sapien` but nothing has been
  published; the scope has to exist on npm first.
- GitHub Packages visibility: the first push of `ghcr.io/gs-sinha/sapien`
  may create the package as private; check it in the package settings.
