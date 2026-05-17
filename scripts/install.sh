#!/usr/bin/env sh
set -eu

VERSION="${SKIFF_INSTALL_VERSION:-dev}"
INSTALL_DIR="${SKIFF_INSTALL_DIR:-/usr/local/bin}"
OS="${SKIFF_INSTALL_OS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
ARCH="${SKIFF_INSTALL_ARCH:-$(uname -m)}"
BASE_URL="${SKIFF_INSTALL_BASE_URL:-https://github.com/s1liconcow/skiff/releases/download/${VERSION}}"

case "$OS" in
  darwin|linux) ;;
  *) echo "unsupported OS: $OS" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

asset="skiff_${VERSION}_${OS}_${ARCH}.tar.gz"
tmp="${TMPDIR:-/tmp}/skiff-install.$$"
mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() {
  url="$1"
  dest="$2"
  case "$url" in
    file://*) cp "${url#file://}" "$dest" ;;
    http://*|https://*) curl -fsSL "$url" -o "$dest" ;;
    *) cp "$url" "$dest" ;;
  esac
}

fetch "$BASE_URL/$asset" "$tmp/$asset"
fetch "$BASE_URL/checksums.txt" "$tmp/checksums.txt"

expected="$(awk -v f="$asset" '$0 ~ f {print $1; exit}' "$tmp/checksums.txt")"
if [ -z "$expected" ]; then
  echo "checksum for $asset not found" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
if [ "$actual" != "$expected" ]; then
  echo "checksum mismatch for $asset" >&2
  exit 1
fi

if [ -n "${SKIFF_INSTALL_PUBLIC_KEY:-}" ]; then
  fetch "$BASE_URL/$asset.sig" "$tmp/$asset.sig"
  if command -v cosign >/dev/null 2>&1; then
    cosign verify-blob --key "$SKIFF_INSTALL_PUBLIC_KEY" --signature "$tmp/$asset.sig" "$tmp/$asset"
  elif command -v minisign >/dev/null 2>&1; then
    minisign -Vm "$tmp/$asset" -P "$SKIFF_INSTALL_PUBLIC_KEY" -x "$tmp/$asset.sig"
  else
    echo "signature verification requested but cosign/minisign is not installed" >&2
    exit 1
  fi
fi

mkdir -p "$tmp/extract"
tar -C "$tmp/extract" -xzf "$tmp/$asset"
mkdir -p "$INSTALL_DIR"
for bin in skiff skiffd skiff-runner skiff-worker; do
  if [ -f "$tmp/extract/$bin" ]; then
    install -m 0755 "$tmp/extract/$bin" "$INSTALL_DIR/$bin"
  fi
done

"$INSTALL_DIR/skiff" version >/dev/null
echo "installed Skiff $VERSION to $INSTALL_DIR"
