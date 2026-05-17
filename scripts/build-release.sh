#!/usr/bin/env sh
set -eu

VERSION="${VERSION:-${1:-dev}}"
OUT_DIR="${OUT_DIR:-dist}"
PKG="github.com/s1liconcow/skiff"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BINS="${BINS:-skiff skiffd skiff-runner skiff-worker}"
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"

mkdir -p "$OUT_DIR"
: > "$OUT_DIR/checksums.txt"

for platform in $PLATFORMS; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"
  work="$OUT_DIR/work/skiff_${VERSION}_${GOOS}_${GOARCH}"
  rm -rf "$work"
  mkdir -p "$work"

  for bin in $BINS; do
    case "$bin" in
      skiff) cmd="./cmd/skiff" ;;
      skiffd) cmd="./cmd/skiffd" ;;
      skiff-runner) cmd="./cmd/skiff-runner" ;;
      skiff-worker) cmd="./cmd/skiff-worker" ;;
      *) echo "unknown binary $bin" >&2; exit 1 ;;
    esac
    echo "building $bin $GOOS/$GOARCH"
    GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X ${PKG}/internal/buildinfo.Version=${VERSION} -X ${PKG}/internal/buildinfo.Commit=${COMMIT} -X ${PKG}/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o "$work/$bin" "$cmd"
  done

  cp LICENSE "$work/LICENSE" 2>/dev/null || true
  tarball="$OUT_DIR/skiff_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
  tar -C "$work" -czf "$tarball" .
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$tarball" >> "$OUT_DIR/checksums.txt"
  else
    shasum -a 256 "$tarball" >> "$OUT_DIR/checksums.txt"
  fi
done

rm -rf "$OUT_DIR/work"
echo "wrote $OUT_DIR/checksums.txt"
