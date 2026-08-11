#!/usr/bin/env bash
# datapin installer — Linux and macOS
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.sh | bash
#
# Override install directory:
#   DATAPIN_INSTALL_DIR=~/.local/bin curl -fsSL ... | bash

set -euo pipefail

REPO="BU-Neuromics/datapin"
BINARY="datapin"

# ---- output helpers ----

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

# ---- detect OS ----

OS="$(uname -s)"
case "$OS" in
  Linux)  OS=linux  ;;
  Darwin) OS=darwin ;;
  *)
    die "Unsupported OS: $OS"$'\n'"Download manually: https://github.com/$REPO/releases"
    ;;
esac

# ---- detect architecture ----

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *)             die "Unsupported architecture: $ARCH" ;;
esac

# ---- choose install directory ----

if [ -n "${DATAPIN_INSTALL_DIR:-}" ]; then
  INSTALL_DIR="$DATAPIN_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
  INSTALL_DIR=/usr/local/bin
else
  INSTALL_DIR="${HOME}/.local/bin"
fi
mkdir -p "$INSTALL_DIR"

# ---- fetch latest version from GitHub API ----

info "Fetching latest release..."
API_RESPONSE="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest")"
VERSION="$(printf '%s' "$API_RESPONSE" | grep '"tag_name"' | sed 's/.*"tag_name": *"\(.*\)".*/\1/')"
[ -n "$VERSION" ] || die "Could not determine latest version from GitHub API."
VER="${VERSION#v}"  # strip leading 'v'

# ---- download archive + checksum file ----

ARCHIVE="${BINARY}_${VER}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download/$VERSION"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "Downloading $BINARY $VERSION ($OS/$ARCH)..."
curl -fsSL --progress-bar "$BASE_URL/$ARCHIVE"      -o "$TMP/$ARCHIVE"
curl -fsSL                "$BASE_URL/checksums.txt"  -o "$TMP/checksums.txt"

# ---- verify SHA-256 checksum ----

info "Verifying checksum..."
(
  cd "$TMP"
  if command -v sha256sum >/dev/null 2>&1; then
    grep "$ARCHIVE" checksums.txt | sha256sum --check --quiet
  elif command -v shasum >/dev/null 2>&1; then
    grep "$ARCHIVE" checksums.txt | shasum -a 256 --check --quiet
  else
    die "No SHA-256 utility found (sha256sum or shasum required)."
  fi
)
ok "Checksum verified."

# ---- extract binary ----

tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
BINARY_PATH="$(find "$TMP" -type f -name "$BINARY" | head -1)"
[ -n "$BINARY_PATH" ] || die "Binary not found in archive."
chmod +x "$BINARY_PATH"

# ---- install ----

info "Installing to $INSTALL_DIR/$BINARY..."
if [ -w "$INSTALL_DIR" ]; then
  mv "$BINARY_PATH" "$INSTALL_DIR/$BINARY"
else
  sudo mv "$BINARY_PATH" "$INSTALL_DIR/$BINARY"
fi
ok "Installed: $("$INSTALL_DIR/$BINARY" --version)"

# ---- PATH reminder ----

if ! printf ':%s:' "$PATH" | grep -q ":$INSTALL_DIR:"; then
  printf '\n\033[1;33mNote:\033[0m %s is not in your PATH.\n' "$INSTALL_DIR"
  printf 'Add the following line to your shell profile (e.g. ~/.bashrc or ~/.zshrc):\n\n'
  printf '  export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
fi
