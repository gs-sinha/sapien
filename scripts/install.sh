#!/bin/sh
# Install the sapien CLI from GitHub Releases.
#
#   curl -fsSL https://gs-sinha.github.io/sapien/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/gs-sinha/sapien/main/scripts/install.sh | sh
#
# It detects the OS (macOS, Linux) and CPU (amd64, arm64; an Apple silicon
# Mac running the shell under Rosetta still gets the arm64 build), downloads
# the matching release archive, verifies it against the release's
# checksums.txt, and installs the binary.
#
# Env vars:
#   SAPIEN_VERSION   version to install, e.g. "v1.2.0" (default: latest release)
#   SAPIEN_PREFIX    install directory (default: /usr/local/bin when writable,
#                    otherwise ~/.local/bin); same as --prefix
#
# Flags:
#   --prefix <dir>   install into <dir> instead of the default
#   --version <ver>  same as SAPIEN_VERSION
#   -h, --help       print this help
#
# POSIX sh only: no bashisms, so this also runs under dash/ash (Alpine, CI
# containers).
set -eu

REPO="gs-sinha/sapien"
VERSION="${SAPIEN_VERSION:-}"
PREFIX="${SAPIEN_PREFIX:-}"

usage() {
  cat <<'EOF'
Install the sapien CLI from GitHub Releases.

Usage: install.sh [--prefix <dir>] [--version <ver>]

  --prefix <dir>   install directory (default: /usr/local/bin when writable,
                   otherwise ~/.local/bin); env SAPIEN_PREFIX
  --version <ver>  version to install, e.g. v1.2.0 (default: latest);
                   env SAPIEN_VERSION
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)
      [ $# -ge 2 ] || { echo "install.sh: --prefix needs a directory" >&2; exit 1; }
      PREFIX="$2"
      shift 2
      ;;
    --version)
      [ $# -ge 2 ] || { echo "install.sh: --version needs a version" >&2; exit 1; }
      VERSION="$2"
      shift 2
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

cat <<'EOF'

Next:
  sapien init ~/sapien-workspace                     create a workspace
  cd ~/sapien-workspace
  ollama pull nomic-embed-text                       recommended: semantic search (add the semantic: block from the guide)
  sapien mcp config --client claude-code --write --allow-mutations
                                                     connect your agent (or codex, cursor, cowork)
  sapien ui                                          open the inspector

Guide: https://gs-sinha.github.io/sapien/
EOF
