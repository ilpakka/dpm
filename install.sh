#!/bin/sh
# DPM installer — https://dpm.iskff.fi
# Usage: curl -sL https://dpm.iskff.fi/install.sh | sh
set -e

BASE_URL="https://dpm.iskff.fi/packages"
INSTALL_DIR="$HOME/.local/bin"

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

PLATFORM="${OS}-${ARCH}"
URL="${BASE_URL}/${PLATFORM}/dpm"

echo "dpm installer"
echo "Platform: ${PLATFORM}"
echo "Downloading from: ${URL}"
echo ""

# Create install dir
mkdir -p "$INSTALL_DIR"

# Download dpm + dpm-tui
for bin in dpm dpm-tui; do
  BIN_URL="${BASE_URL}/${PLATFORM}/${bin}"
  echo "Downloading ${bin}..."
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$BIN_URL" -o "$INSTALL_DIR/$bin"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$INSTALL_DIR/$bin" "$BIN_URL"
  else
    echo "Error: curl or wget required"
    exit 1
  fi
  chmod +x "$INSTALL_DIR/$bin"
done

# Check PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "Add to your PATH:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
    echo "Or add that line to ~/.bashrc / ~/.zshrc"
    ;;
esac

echo "Installed dpm + dpm-tui to $INSTALL_DIR/"
echo ""
echo "Run: dpm list --all"
