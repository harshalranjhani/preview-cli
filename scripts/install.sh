#!/bin/sh
# Install the preview binary on Linux.
#   curl -fsSL https://raw.githubusercontent.com/harshalranjhani/preview-cli/main/scripts/install.sh | sh
set -eu

repo="${PREVIEW_REPO:-harshalranjhani/preview-cli}"
version="${PREVIEW_VERSION:-latest}"
dest="${PREVIEW_INSTALL_DIR:-/usr/local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

if [ "$os" != "linux" ]; then
  echo "The release binary is built for Linux. On this machine, build from source:" >&2
  echo "  go build -o preview ./cmd/preview" >&2
  exit 1
fi

if [ "$version" = "latest" ]; then
  url="https://github.com/${repo}/releases/latest/download/preview_${os}_${arch}"
else
  url="https://github.com/${repo}/releases/download/${version}/preview_${os}_${arch}"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
echo "Downloading ${url}"
curl -fsSL "$url" -o "$tmp"
chmod 755 "$tmp"

mkdir -p "$dest"
if [ -w "$dest" ]; then
  mv "$tmp" "${dest}/preview"
else
  sudo mv "$tmp" "${dest}/preview"
fi
trap - EXIT

echo "Installed ${dest}/preview"
echo "Next, on the server: sudo preview server init"
