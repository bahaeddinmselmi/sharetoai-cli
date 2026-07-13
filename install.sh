#!/bin/sh
# Installs the latest sharetoai CLI release for your platform.
# Usage: curl -fsSL https://sharetoai.app/install.sh | sh
set -eu

REPO="bahaeddinmselmi/sharetoai-cli"
BIN_DIR="${SHARETOAI_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s)
arch=$(uname -m)

case "$os" in
  Linux) goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) echo "sharetoai: unsupported OS '$os' — download a release manually from https://github.com/$REPO/releases" >&2; exit 1 ;;
esac

case "$arch" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) echo "sharetoai: unsupported architecture '$arch' — download a release manually from https://github.com/$REPO/releases" >&2; exit 1 ;;
esac

asset="sharetoai-${goos}-${goarch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"

mkdir -p "$BIN_DIR"
tmp=$(mktemp)
echo "Downloading ${asset}..."
curl -fsSL "$url" -o "$tmp"
chmod +x "$tmp"
mv "$tmp" "$BIN_DIR/sharetoai"

echo "Installed to $BIN_DIR/sharetoai"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Add it to your PATH: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
echo "Next: sharetoai login"
