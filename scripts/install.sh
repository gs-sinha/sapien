#!/bin/sh
# Install the sapien CLI from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/growsimplee/sapien/main/scripts/install.sh | sh
#
# Env vars:
#   SAPIEN_VERSION   version to install, e.g. "v0.3.0" (default: latest release)
#   SAPIEN_PREFIX    install prefix (default: /usr/local/bin, or ~/.local/bin
#                    if /usr/local/bin isn't writable); same as --prefix
#
# Flags:
#   --prefix <dir>   install into <dir> instead of the default
#   --version <ver>  same as SAPIEN_VERSION
#
# POSIX sh only: no bashisms, so this also runs under dash/ash (Alpine, CI
# containers).
set -eu

REPO="growsimplee/sapien"
VERSION="${SAPIEN_VERSION:-}"
PREFIX="${SAPIEN_PREFIX:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)
      PREFIX="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    *)
      echo "install.sh: unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

die() {
  echo "install.sh: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found on PATH"
}

need curl

# --- detect OS/arch --------------------------------------------------------

os="$(uname -s)"
case "$os" in
  Darwin) sapien_os="darwin" ;;
  Linux) sapien_os="linux" ;;
  MINGW* | MSYS* | CYGWIN*) sapien_os="windows" ;;
  *) die "unsupported OS: $os" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) sapien_arch="amd64" ;;
  arm64 | aarch64) sapien_arch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac

ext="tar.gz"
[ "$sapien_os" = "windows" ] && ext="zip"

# --- resolve version --------------------------------------------------------

if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  [ -n "$VERSION" ] || die "could not determine the latest release; set SAPIEN_VERSION"
fi

version_num="${VERSION#v}"
archive="sapien_${version_num}_${sapien_os}_${sapien_arch}.${ext}"
base_url="https://github.com/${REPO}/releases/download/${VERSION}"

echo "install.sh: installing sapien ${VERSION} (${sapien_os}/${sapien_arch})"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

curl -fsSL -o "$workdir/$archive" "$base_url/$archive" \
  || die "download failed: $base_url/$archive"
curl -fsSL -o "$workdir/checksums.txt" "$base_url/checksums.txt" \
  || die "download failed: $base_url/checksums.txt"

# --- verify checksum ---------------------------------------------------------

( cd "$workdir" \
  && grep " ${archive}\$" checksums.txt > checksums.match \
  || die "no checksum entry for ${archive}" )

if command -v sha256sum >/dev/null 2>&1; then
  ( cd "$workdir" && sha256sum -c checksums.match ) \
    || die "checksum verification failed"
elif command -v shasum >/dev/null 2>&1; then
  ( cd "$workdir" && shasum -a 256 -c checksums.match ) \
    || die "checksum verification failed"
else
  echo "install.sh: warning: no sha256sum/shasum found, skipping checksum verification" >&2
fi

# --- extract ------------------------------------------------------------------

if [ "$ext" = "zip" ]; then
  need unzip
  unzip -q "$workdir/$archive" -d "$workdir/extracted"
else
  mkdir -p "$workdir/extracted"
  tar -xzf "$workdir/$archive" -C "$workdir/extracted"
fi

binary="sapien"
[ "$sapien_os" = "windows" ] && binary="sapien.exe"
[ -f "$workdir/extracted/$binary" ] || die "extracted archive has no $binary"

# --- choose install prefix ----------------------------------------------------

if [ -z "$PREFIX" ]; then
  if [ -w "/usr/local/bin" ] 2>/dev/null; then
    PREFIX="/usr/local/bin"
  else
    PREFIX="$HOME/.local/bin"
  fi
fi

mkdir -p "$PREFIX"
install -m 755 "$workdir/extracted/$binary" "$PREFIX/$binary" 2>/dev/null \
  || cp "$workdir/extracted/$binary" "$PREFIX/$binary"
chmod 755 "$PREFIX/$binary"

echo "install.sh: installed $PREFIX/$binary"

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) echo "install.sh: warning: $PREFIX is not on PATH; add it to your shell profile" >&2 ;;
esac

"$PREFIX/$binary" version || true
