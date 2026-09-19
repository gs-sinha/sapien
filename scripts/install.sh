#!/bin/sh
# Install (or upgrade) the sapien CLI from GitHub Releases.
#
#   curl -fsSL https://gs-sinha.github.io/sapien/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/gs-sinha/sapien/main/scripts/install.sh | sh
#
# It detects the OS (macOS, Linux) and CPU (amd64, arm64; an Apple silicon
# Mac running the shell under Rosetta still gets the arm64 build), downloads
# the matching release archive, verifies it against the release's
# checksums.txt, and installs the binary.
#
# Re-running it upgrades in place (PLAN §34f item 4, the script-install
# counterpart of `sapien upgrade`): a sapien already on PATH at the target
# version is left alone; a Homebrew install is deferred to `brew upgrade`
# rather than fought; a symlink into a source checkout (a dev build) is
# never overwritten by accident. See the "existing install" section below.
#
# Env vars:
#   SAPIEN_VERSION   version to install, e.g. "v1.2.0" (default: latest release)
#   SAPIEN_PREFIX    install directory (default: the existing sapien's
#                    directory, if any, else /usr/local/bin when writable,
#                    otherwise ~/.local/bin); same as --prefix
#
# Flags:
#   --prefix <dir>   install into <dir> instead of the default
#   --version <ver>  same as SAPIEN_VERSION
#   --force          install even if an existing sapien on PATH is already
#                    at this version
#   -h, --help       print this help
#
# POSIX sh only: no bashisms, so this also runs under dash/ash (Alpine, CI
# containers).
set -eu

REPO="gs-sinha/sapien"
VERSION="${SAPIEN_VERSION:-}"
PREFIX="${SAPIEN_PREFIX:-}"
FORCE=0
# Tracks whether PREFIX came from the user (flag or env) rather than this
# script's own default, so the dev-build-symlink refusal below knows
# whether the user has taken responsibility for where this lands.
PREFIX_EXPLICIT=0
[ -n "$PREFIX" ] && PREFIX_EXPLICIT=1

usage() {
  cat <<'EOF'
Install (or upgrade) the sapien CLI from GitHub Releases.

Usage: install.sh [--prefix <dir>] [--version <ver>] [--force]

  --prefix <dir>   install directory (default: the existing sapien's
                   directory, if any, else /usr/local/bin when writable,
                   otherwise ~/.local/bin); env SAPIEN_PREFIX
  --version <ver>  version to install, e.g. v1.2.0 (default: latest);
                   env SAPIEN_VERSION
  --force          install even if an existing sapien on PATH is already
                   at this version
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)
      [ $# -ge 2 ] || { echo "install.sh: --prefix needs a directory" >&2; exit 1; }
      PREFIX="$2"
      PREFIX_EXPLICIT=1
      shift 2
      ;;
    --version)
      [ $# -ge 2 ] || { echo "install.sh: --version needs a version" >&2; exit 1; }
      VERSION="$2"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "install.sh: unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

say() {
  echo "install.sh: $*"
}

die() {
  echo "install.sh: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found on PATH"
}

# --- downloader: curl, or wget when curl is missing ---------------------------

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL -o "$2" "$1"; }
  final_url() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  final_url() {
    wget -S --spider "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n1 | tr -d '\r'
  }
else
  die "curl or wget is required"
fi

need tar
need uname

# --- detect OS/arch -----------------------------------------------------------

os="$(uname -s)"
case "$os" in
  Darwin) sapien_os="darwin" ;;
  Linux) sapien_os="linux" ;;
  MINGW* | MSYS* | CYGWIN*)
    die "Windows is not supported yet (the daemon uses Unix-only syscalls); run this inside WSL instead"
    ;;
  *) die "unsupported OS: $os (sapien ships for macOS and Linux)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) sapien_arch="amd64" ;;
  arm64 | aarch64) sapien_arch="arm64" ;;
  *) die "unsupported architecture: $arch (sapien ships for amd64 and arm64)" ;;
esac

# A shell running under Rosetta reports x86_64 on an Apple silicon Mac; the
# native arm64 binary is the one to install there.
if [ "$sapien_os" = "darwin" ] && [ "$sapien_arch" = "amd64" ]; then
  if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
    sapien_arch="arm64"
  fi
fi

# --- resolve version ----------------------------------------------------------

if [ -z "$VERSION" ]; then
  # The /releases/latest page redirects to /releases/tag/<tag>; following it
  # avoids the GitHub API's unauthenticated rate limit.
  latest="$(final_url "https://github.com/${REPO}/releases/latest" 2>/dev/null || true)"
  case "$latest" in
    */releases/tag/*) VERSION="${latest##*/}" ;;
  esac
fi
if [ -z "$VERSION" ]; then
  tmp_json="$(mktemp)"
  if fetch "https://api.github.com/repos/${REPO}/releases/latest" "$tmp_json" 2>/dev/null; then
    VERSION="$(grep -m1 '"tag_name"' "$tmp_json" | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  fi
  rm -f "$tmp_json"
fi
[ -n "$VERSION" ] || die "could not determine the latest release; set SAPIEN_VERSION (e.g. v1.2.0)"

case "$VERSION" in
  v*) ;;
  *) VERSION="v$VERSION" ;;
esac

version_num="${VERSION#v}"
archive="sapien_${version_num}_${sapien_os}_${sapien_arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${VERSION}"

# --- existing install --------------------------------------------------------
#
# resolve_path follows a chain of symlinks by hand (rather than relying on
# `readlink -f`, which older BSD/macOS readlink implementations lack)
# until it lands on a real file, so a Homebrew shim (`/opt/homebrew/bin/
# sapien` -> `../Cellar/sapien/1.3.1/bin/sapien`) or a developer's own
# `ln -s ~/sapien/bin/sapien /usr/local/bin/sapien` both resolve to where
# the binary actually lives.
resolve_path() {
  p="$1"
  while [ -L "$p" ]; do
    link="$(readlink "$p")"
    case "$link" in
      /*) p="$link" ;;
      *) p="$(dirname "$p")/$link" ;;
    esac
  done
  # A relative symlink target (Homebrew's own "../Cellar/..." among them)
  # can leave "p" with an unresolved ".." in it; cd+pwd -P (POSIX, unlike
  # realpath(1), which isn't guaranteed present on macOS/BSD) canonicalizes
  # the directory part without needing the file itself to exist yet.
  dir="$(cd "$(dirname "$p")" && pwd -P)"
  printf '%s/%s\n' "$dir" "$(basename "$p")"
}

existing="$(command -v sapien 2>/dev/null || true)"
if [ -n "$existing" ]; then
  existing_version="$("$existing" version --json 2>/dev/null | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p')"

  if [ -n "$existing_version" ] && [ "${existing_version#v}" = "$version_num" ] && [ "$FORCE" -ne 1 ]; then
    say "already up to date (sapien ${existing_version} at $existing)"
    exit 0
  fi

  existing_resolved="$(resolve_path "$existing")"
  case "$existing_resolved" in
    */Cellar/* | */homebrew/*)
      say "sapien is installed via Homebrew ($existing_resolved)"
      say "run this instead: brew upgrade gs-sinha/tap/sapien"
      exit 0
      ;;
  esac

  if [ -L "$existing" ] && [ "$PREFIX_EXPLICIT" -ne 1 ]; then
    die "$existing is a symlink to $existing_resolved (looks like a dev build); pass --prefix <dir> to install somewhere else, or remove/replace it yourself first"
  fi

  if [ "$PREFIX_EXPLICIT" -ne 1 ]; then
    # Upgrade in place: land in the same directory the existing sapien
    # already lives in, rather than this script's own default (which,
    # picked independently, could silently produce a second install that
    # shadows or is shadowed by the first depending on PATH order).
    PREFIX="$(dirname "$existing_resolved")"
  fi
fi

say "installing sapien ${VERSION} (${sapien_os}/${sapien_arch})"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

fetch "$base_url/$archive" "$workdir/$archive" \
  || die "download failed: $base_url/$archive"
fetch "$base_url/checksums.txt" "$workdir/checksums.txt" \
  || die "download failed: $base_url/checksums.txt"

# --- verify checksum ----------------------------------------------------------

grep " ${archive}\$" "$workdir/checksums.txt" > "$workdir/checksums.match" \
  || die "no checksum entry for ${archive}"

if command -v sha256sum >/dev/null 2>&1; then
  ( cd "$workdir" && sha256sum -c checksums.match >/dev/null ) \
    || die "checksum verification failed"
elif command -v shasum >/dev/null 2>&1; then
  ( cd "$workdir" && shasum -a 256 -c checksums.match >/dev/null ) \
    || die "checksum verification failed"
else
  echo "install.sh: warning: no sha256sum/shasum found, skipping checksum verification" >&2
fi

# --- extract ------------------------------------------------------------------

mkdir -p "$workdir/extracted"
tar -xzf "$workdir/$archive" -C "$workdir/extracted"
[ -f "$workdir/extracted/sapien" ] || die "extracted archive has no sapien binary"

# --- choose install prefix ----------------------------------------------------

if [ -z "$PREFIX" ]; then
  if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
    PREFIX="/usr/local/bin"
  else
    PREFIX="$HOME/.local/bin"
  fi
fi

mkdir -p "$PREFIX" || die "cannot create $PREFIX; pass --prefix <dir>"
install -m 755 "$workdir/extracted/sapien" "$PREFIX/sapien" 2>/dev/null \
  || cp "$workdir/extracted/sapien" "$PREFIX/sapien" \
  || die "cannot write $PREFIX/sapien; pass --prefix <dir> or re-run with sudo"
chmod 755 "$PREFIX/sapien"

say "installed $PREFIX/sapien"
"$PREFIX/sapien" version || true

case ":$PATH:" in
  *":$PREFIX:"*)
    found="$(command -v sapien 2>/dev/null || true)"
    if [ -n "$found" ] && [ "$found" != "$PREFIX/sapien" ]; then
      echo "install.sh: warning: $found comes first on PATH and shadows $PREFIX/sapien" >&2
    fi
    ;;
  *)
    echo "install.sh: warning: $PREFIX is not on PATH; add this to your shell profile:" >&2
    echo "  export PATH=\"$PREFIX:\$PATH\"" >&2
    ;;
esac

# --- restart a running daemon, if any ------------------------------------------
#
# An upgrade that leaves the old build still serving would be invisible:
# the daemon keeps answering on the same port with the previous version
# until something notices. `sapien daemon status --json` and `daemon
# restart` both resolve a workspace the same way every other sapien
# command does (--workspace, $SAPIEN_WORKSPACE, cwd, or default_workspace);
# from wherever this script happens to run, that commonly finds none at
# all, which status_json reports as anything other than `"running":true`
# and this section quietly skips.
status_json="$("$PREFIX/sapien" daemon status --json 2>/dev/null || true)"
case "$status_json" in
  *'"running":true'* | *'"running": true'*)
    say "restarting the running daemon"
    "$PREFIX/sapien" daemon restart >/dev/null 2>&1 \
      || echo "install.sh: warning: could not restart the running daemon; run \`sapien daemon restart\` yourself" >&2
    ;;
esac

cat <<'EOF'

Next:
  sapien init ~/sapien-workspace                     create a workspace
  cd ~/sapien-workspace
  sapien mcp config --client claude-code --write --allow-mutations
                                                     connect your agent (or codex, cursor, cowork)
  sapien ui                                          open the inspector; turn on semantic search from Settings there

Guide: https://gs-sinha.github.io/sapien/
EOF
