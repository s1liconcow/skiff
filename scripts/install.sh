#!/usr/bin/env bash
#
# Skiff installer
#
# One-liner install with cache buster:
#   curl -fsSL "https://raw.githubusercontent.com/s1liconcow/skiff/main/scripts/install.sh?$(date +%s)" | bash
#
# Install a specific release:
#   curl -fsSL https://raw.githubusercontent.com/s1liconcow/skiff/main/scripts/install.sh | \
#     SKIFF_INSTALL_VERSION=0.2 SKIFF_INSTALL_DIR="$HOME/.local/bin" bash
#
# Local or airgapped install:
#   SKIFF_INSTALL_VERSION=0.2 SKIFF_INSTALL_BASE_URL="file://$PWD/dist" scripts/install.sh
#   scripts/install.sh --offline dist/skiff_0.2_linux_amd64.tar.gz --no-verify

if [ -z "${BASH_VERSION:-}" ]; then
  if command -v bash >/dev/null 2>&1 && [ -r "$0" ]; then
    exec bash "$0" "$@"
  fi
  echo "Skiff installer requires bash. Use: curl -fsSL .../scripts/install.sh | bash" >&2
  exit 2
fi

set -euo pipefail
shopt -s lastpipe 2>/dev/null || true
umask 022

PROJECT="Skiff"
OWNER="s1liconcow"
REPO="skiff"
REPO_URL="https://github.com/${OWNER}/${REPO}.git"
INSTALLER_FALLBACK_VERSION="0.2"
BINARIES=(skiff skiffd skiff-runner skiff-worker)

VERSION="${SKIFF_INSTALL_VERSION:-}"
INSTALL_DIR="${SKIFF_INSTALL_DIR:-$HOME/.local/bin}"
BASE_URL="${SKIFF_INSTALL_BASE_URL:-}"
PUBLIC_KEY="${SKIFF_INSTALL_PUBLIC_KEY:-}"
OS_OVERRIDE="${SKIFF_INSTALL_OS:-}"
ARCH_OVERRIDE="${SKIFF_INSTALL_ARCH:-}"
EXPECTED_CHECKSUM="${SKIFF_INSTALL_CHECKSUM:-}"
CHECKSUMS_FILE="${SKIFF_INSTALL_CHECKSUMS:-}"

QUIET="${SKIFF_INSTALL_QUIET:-0}"
NO_GUM="${SKIFF_INSTALL_NO_GUM:-0}"
FORCE="${SKIFF_INSTALL_FORCE:-0}"
VERIFY_ARTIFACTS="${SKIFF_INSTALL_VERIFY:-1}"
RUN_SELF_TEST="${SKIFF_INSTALL_SELF_TEST:-1}"
FROM_SOURCE="${SKIFF_INSTALL_FROM_SOURCE:-0}"
EASY_MODE="${SKIFF_INSTALL_EASY_MODE:-0}"
INSTALL_COMPLETIONS="${SKIFF_INSTALL_COMPLETIONS:-1}"
CONFIGURE_AGENTS="${SKIFF_INSTALL_CONFIGURE_AGENTS:-1}"
SYSTEM_INSTALL=0
OFFLINE_TARBALL="${SKIFF_INSTALL_OFFLINE_TARBALL:-}"

OS=""
ARCH=""
ASSET=""
TMP=""
LOCK_DIR=""
HAS_GUM=0
PROXY_ARGS=()
INSTALLED_FROM="release archive"
SKIFF_VERSION_OUTPUT=""
COMPLETION_STATUS="skipped"
CODEX_STATUS="skipped"
CLAUDE_STATUS="skipped"
CODEX_BACKUP=""
CLAUDE_BACKUP=""
DOWNLOADED_ARCHIVE=""

usage() {
  cat <<'EOF'
Skiff installer

Usage:
  install.sh [flags]
  curl -fsSL https://raw.githubusercontent.com/s1liconcow/skiff/main/scripts/install.sh | bash

Flags:
  --version <tag>        Install a specific release tag (env: SKIFF_INSTALL_VERSION)
  --install-dir <dir>    Install binaries to dir (env: SKIFF_INSTALL_DIR; default: ~/.local/bin)
  --dest <dir>           Alias for --install-dir
  --base-url <url>       Release asset base URL (env: SKIFF_INSTALL_BASE_URL)
  --system               Install to /usr/local/bin
  --offline <tarball>    Install from a local tarball without network downloads
  --from-source          Build from source instead of downloading release archives
  --force                Reinstall even if the requested version is already present
  --easy-mode            Add install dir to shell rc files when missing from PATH
  --no-completions       Skip shell completion installation
  --no-agents            Skip Codex/Claude skill installation
  --verify               Run post-install self-test (default)
  --no-self-test         Skip post-install self-test
  --no-verify            Skip checksum and signature verification
  --public-key <key>     Verify archive signature with cosign or minisign
  --checksum <sha256>    Expected SHA256 for offline/custom archives
  --checksums <file>     Local checksums.txt for offline/custom archives
  --quiet                Suppress non-error output
  --no-gum               Disable gum formatting
  --help                 Show this help

Compatibility environment variables:
  SKIFF_INSTALL_VERSION, SKIFF_INSTALL_DIR, SKIFF_INSTALL_BASE_URL,
  SKIFF_INSTALL_OS, SKIFF_INSTALL_ARCH, SKIFF_INSTALL_PUBLIC_KEY,
  SKIFF_INSTALL_CHECKSUM, SKIFF_INSTALL_CHECKSUMS
EOF
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version)
        [ "$#" -ge 2 ] || { err "--version requires a value"; exit 2; }
        shift; VERSION="$1" ;;
      --install-dir|--dest|--dir)
        [ "$#" -ge 2 ] || { err "$1 requires a value"; exit 2; }
        shift; INSTALL_DIR="$1" ;;
      --base-url)
        [ "$#" -ge 2 ] || { err "--base-url requires a value"; exit 2; }
        shift; BASE_URL="$1" ;;
      --system)
        SYSTEM_INSTALL=1; INSTALL_DIR="/usr/local/bin" ;;
      --offline)
        [ "$#" -ge 2 ] || { err "--offline requires a tarball path"; exit 2; }
        shift; OFFLINE_TARBALL="$1" ;;
      --from-source)
        FROM_SOURCE=1 ;;
      --force)
        FORCE=1 ;;
      --easy-mode)
        EASY_MODE=1 ;;
      --no-completions)
        INSTALL_COMPLETIONS=0 ;;
      --no-agents|--no-configure)
        CONFIGURE_AGENTS=0 ;;
      --verify)
        RUN_SELF_TEST=1 ;;
      --no-self-test)
        RUN_SELF_TEST=0 ;;
      --no-verify)
        VERIFY_ARTIFACTS=0 ;;
      --public-key)
        [ "$#" -ge 2 ] || { err "--public-key requires a value"; exit 2; }
        shift; PUBLIC_KEY="$1" ;;
      --checksum)
        [ "$#" -ge 2 ] || { err "--checksum requires a value"; exit 2; }
        shift; EXPECTED_CHECKSUM="$1" ;;
      --checksums)
        [ "$#" -ge 2 ] || { err "--checksums requires a value"; exit 2; }
        shift; CHECKSUMS_FILE="$1" ;;
      --quiet)
        QUIET=1 ;;
      --no-gum)
        NO_GUM=1 ;;
      --help|-h)
        usage; exit 0 ;;
      *)
        err "Unknown flag: $1"
        usage >&2
        exit 2 ;;
    esac
    shift
  done
}

detect_gum() {
  if command -v gum >/dev/null 2>&1 && [ -t 1 ]; then
    HAS_GUM=1
  fi
}

ansi() {
  local code="$1"; shift
  printf '\033[%sm%s\033[0m' "$code" "$*"
}

info() {
  [ "$QUIET" = "1" ] && return 0
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ]; then
    gum style --foreground 39 "-> $*"
  else
    printf '%b %s\n' "$(ansi '0;34' '->')" "$*"
  fi
}

ok() {
  [ "$QUIET" = "1" ] && return 0
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ]; then
    gum style --foreground 42 "✓ $*"
  else
    printf '%b %s\n' "$(ansi '0;32' '✓')" "$*"
  fi
}

warn() {
  [ "$QUIET" = "1" ] && return 0
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ]; then
    gum style --foreground 214 "warning: $*"
  else
    printf '%b %s\n' "$(ansi '1;33' 'warning:')" "$*"
  fi
}

err() {
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ]; then
    gum style --foreground 196 "error: $*" >&2
  else
    printf '%b %s\n' "$(ansi '0;31' 'error:')" "$*" >&2
  fi
}

strip_ansi() {
  local esc
  esc=$(printf '\033')
  sed "s/${esc}\\[[0-9;]*m//g"
}

draw_box() {
  [ "$QUIET" = "1" ] && return 0
  local color="$1"; shift
  local lines=("$@")
  local max_width=0 line stripped len padding pad border i
  for line in "${lines[@]}"; do
    stripped=$(printf '%b' "$line" | strip_ansi)
    len=${#stripped}
    [ "$len" -gt "$max_width" ] && max_width=$len
  done
  border=""
  for ((i=0; i<max_width+4; i++)); do border+="═"; done
  printf '\033[%sm╔%s╗\033[0m\n' "$color" "$border"
  for line in "${lines[@]}"; do
    stripped=$(printf '%b' "$line" | strip_ansi)
    padding=$((max_width - ${#stripped}))
    pad=""
    for ((i=0; i<padding; i++)); do pad+=" "; done
    printf '\033[%sm║\033[0m  %b%s  \033[%sm║\033[0m\n' "$color" "$line" "$pad" "$color"
  done
  printf '\033[%sm╚%s╝\033[0m\n' "$color" "$border"
}

banner() {
  [ "$QUIET" = "1" ] && return 0
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ]; then
    gum style \
      --border normal --border-foreground 39 \
      --padding "0 1" --margin "1 0" \
      "$(gum style --foreground 42 --bold 'Skiff installer')" \
      "$(gum style --foreground 245 'Kubernetes-class operational leverage without Kubernetes-class cost')"
  else
    draw_box "34" \
      "$(ansi '1;32' 'Skiff installer')" \
      "$(ansi '0;90' 'Kubernetes-class operational leverage without Kubernetes-class cost')"
  fi
}

run_with_spinner() {
  local title="$1"; shift
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ] && [ "$QUIET" != "1" ]; then
    gum spin --spinner dot --title "$title" -- "$@"
  else
    info "$title"
    "$@"
  fi
}

cleanup() {
  if [ -n "${TMP:-}" ] && [ -d "$TMP" ]; then
    rm -rf "$TMP"
  fi
  if [ -n "${LOCK_DIR:-}" ] && [ -d "$LOCK_DIR" ]; then
    rmdir "$LOCK_DIR" 2>/dev/null || true
  fi
}

setup_tmp_and_lock() {
  TMP=$(mktemp -d "${TMPDIR:-/tmp}/skiff-install.XXXXXX")
  local lock_root="${TMPDIR:-/tmp}/skiff-install.lock"
  if mkdir "$lock_root" 2>/dev/null; then
    LOCK_DIR="$lock_root"
    printf '%s\n' "$$" > "$LOCK_DIR/pid"
    return 0
  fi
  if [ -f "$lock_root/pid" ]; then
    local pid
    pid=$(cat "$lock_root/pid" 2>/dev/null || true)
    if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then
      warn "Removing stale install lock from PID $pid"
      rm -rf "$lock_root"
      mkdir "$lock_root"
      LOCK_DIR="$lock_root"
      printf '%s\n' "$$" > "$LOCK_DIR/pid"
      return 0
    fi
  fi
  err "Another Skiff install appears to be running. Lock: $lock_root"
  exit 1
}

setup_proxy() {
  PROXY_ARGS=()
  if [ -n "${HTTPS_PROXY:-}" ]; then
    PROXY_ARGS=(--proxy "$HTTPS_PROXY")
    info "Using HTTPS proxy: $HTTPS_PROXY"
  elif [ -n "${HTTP_PROXY:-}" ]; then
    PROXY_ARGS=(--proxy "$HTTP_PROXY")
    info "Using HTTP proxy: $HTTP_PROXY"
  fi
}

fetch() {
  local url="$1"
  local dest="$2"
  case "$url" in
    file://*) cp "${url#file://}" "$dest" ;;
    http://*|https://*) curl -fsSL --connect-timeout 10 --retry 2 --retry-delay 1 "${PROXY_ARGS[@]}" "$url" -o "$dest" ;;
    *) cp "$url" "$dest" ;;
  esac
}

detect_platform() {
  OS="${OS_OVERRIDE:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
  ARCH="${ARCH_OVERRIDE:-$(uname -m)}"
  case "$OS" in
    linux|darwin) ;;
    *)
      warn "No prebuilt archive for OS $OS; falling back to source"
      FROM_SOURCE=1 ;;
  esac
  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
      warn "No prebuilt archive for architecture $ARCH; falling back to source"
      FROM_SOURCE=1 ;;
  esac
  if [ "$OS" = "linux" ] && grep -qi microsoft /proc/version 2>/dev/null; then
    warn "WSL detected; continuing with linux/$ARCH"
  fi
}

check_disk_space() {
  local parent
  parent=$(dirname "$INSTALL_DIR")
  mkdir -p "$parent"
  local available
  available=$(df -Pk "$parent" 2>/dev/null | awk 'NR==2 {print $4}')
  if [ -n "$available" ] && [ "$available" -lt 10240 ]; then
    err "Less than 10 MB available near $INSTALL_DIR"
    return 1
  fi
}

check_write_permissions() {
  mkdir -p "$INSTALL_DIR"
  local probe="$INSTALL_DIR/.skiff-install-write-test.$$"
  if ! : > "$probe" 2>/dev/null; then
    err "Cannot write to $INSTALL_DIR"
    if [ "$SYSTEM_INSTALL" = "1" ]; then
      err "Try running with sudo, or omit --system to install under ~/.local/bin"
    fi
    return 1
  fi
  rm -f "$probe"
}

preflight_checks() {
  info "Running preflight checks"
  command -v tar >/dev/null 2>&1 || { err "tar is required"; return 1; }
  if [ "$FROM_SOURCE" = "1" ]; then
    command -v go >/dev/null 2>&1 || { err "go is required for --from-source"; return 1; }
  elif [ -z "$OFFLINE_TARBALL" ]; then
    command -v curl >/dev/null 2>&1 || { err "curl is required"; return 1; }
  fi
  check_disk_space
  check_write_permissions
}

resolve_version() {
  if [ -n "$VERSION" ] && [ "$VERSION" != "latest" ]; then
    return 0
  fi
  if [ -n "$BASE_URL" ] && [[ "$BASE_URL" != http*github.com*latest* ]]; then
    VERSION="${VERSION:-$INSTALLER_FALLBACK_VERSION}"
    warn "No version provided with custom SKIFF_INSTALL_BASE_URL; using $VERSION"
    return 0
  fi
  local api_url="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"
  local resolved=""
  resolved=$(curl -fsSL --connect-timeout 5 "${PROXY_ARGS[@]}" "$api_url" 2>/dev/null | awk -F\" '/"tag_name":/ {print $4; exit}' || true)
  if [ -z "$resolved" ]; then
    resolved=$(curl -fsSL -o /dev/null -w '%{url_effective}' "${PROXY_ARGS[@]}" "https://github.com/${OWNER}/${REPO}/releases/latest" 2>/dev/null | sed -E 's|.*/tag/||' || true)
  fi
  VERSION="${resolved:-$INSTALLER_FALLBACK_VERSION}"
}

asset_name() {
  printf 'skiff_%s_%s_%s.tar.gz' "$VERSION" "$OS" "$ARCH"
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    return 127
  fi
}

checksum_from_file() {
  local file="$1"
  local name="$2"
  awk -v f="$name" '$0 ~ f {print $1; exit}' "$file"
}

verify_checksum() {
  local file="$1"
  local expected="$2"
  if [ "$VERIFY_ARTIFACTS" != "1" ]; then
    warn "Skipping checksum verification because --no-verify was set"
    return 0
  fi
  if [ -z "$expected" ]; then
    err "No checksum available for $(basename "$file")"
    err "Provide --checksum/--checksums or rerun with --no-verify only if you trust the archive"
    return 1
  fi
  local actual
  if ! actual=$(sha256_file "$file"); then
    err "No SHA256 tool found; install sha256sum or shasum"
    return 1
  fi
  if [ "$actual" != "$expected" ]; then
    err "Checksum mismatch for $(basename "$file")"
    err "Expected: $expected"
    err "Actual:   $actual"
    return 1
  fi
  ok "SHA256 checksum verified"
}

verify_signature() {
  local file="$1"
  local sig_file="$2"
  if [ "$VERIFY_ARTIFACTS" != "1" ]; then
    return 0
  fi
  if [ -z "$PUBLIC_KEY" ]; then
    if command -v cosign >/dev/null 2>&1 && [ -f "${file}.bundle" ]; then
      cosign verify-blob --bundle "${file}.bundle" \
        --certificate-identity-regexp "^https://github.com/${OWNER}/${REPO}/.github/workflows/.*$" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        "$file" >/dev/null
      ok "Sigstore bundle verified"
    else
      warn "No public key or Sigstore bundle configured; checksum verification is the authenticity floor"
    fi
    return 0
  fi
  if [ ! -f "$sig_file" ]; then
    warn "Signature file not found; skipping signature verification"
    return 0
  fi
  if command -v cosign >/dev/null 2>&1; then
    cosign verify-blob --key "$PUBLIC_KEY" --signature "$sig_file" "$file" >/dev/null
    ok "cosign signature verified"
  elif command -v minisign >/dev/null 2>&1; then
    minisign -Vm "$file" -P "$PUBLIC_KEY" -x "$sig_file" >/dev/null
    ok "minisign signature verified"
  else
    err "Signature verification requested but neither cosign nor minisign is installed"
    return 1
  fi
}

download_release_archive() {
  ASSET=$(asset_name)
  local base="${BASE_URL:-https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}}"
  local archive="$TMP/$ASSET"
  local checksums="$TMP/checksums.txt"
  local sig="$TMP/$ASSET.sig"

  run_with_spinner "Downloading $ASSET" fetch "$base/$ASSET" "$archive" || return 1
  run_with_spinner "Downloading checksums.txt" fetch "$base/checksums.txt" "$checksums" || return 1

  local expected
  expected=$(checksum_from_file "$checksums" "$ASSET")
  verify_checksum "$archive" "$expected" || return 1

  if [ -n "$PUBLIC_KEY" ]; then
    if ! fetch "$base/$ASSET.sig" "$sig" 2>/dev/null; then
      err "Signature verification was requested, but $base/$ASSET.sig is not available"
      return 1
    fi
    verify_signature "$archive" "$sig" || return 1
  fi
  DOWNLOADED_ARCHIVE="$archive"
}

offline_checksum_for() {
  local file="$1"
  local name
  name=$(basename "$file")
  if [ -n "$EXPECTED_CHECKSUM" ]; then
    printf '%s\n' "$EXPECTED_CHECKSUM"
    return 0
  fi
  if [ -n "$CHECKSUMS_FILE" ] && [ -f "$CHECKSUMS_FILE" ]; then
    checksum_from_file "$CHECKSUMS_FILE" "$name"
    return 0
  fi
  if [ -f "$(dirname "$file")/checksums.txt" ]; then
    checksum_from_file "$(dirname "$file")/checksums.txt" "$name"
    return 0
  fi
  printf ''
}

install_from_archive() {
  local archive="$1"
  local extract="$TMP/extract"
  mkdir -p "$extract"
  tar -xzf "$archive" -C "$extract"
  local bin src
  for bin in "${BINARIES[@]}"; do
    src=$(find "$extract" -type f -name "$bin" -perm -111 | head -n 1)
    if [ -z "$src" ]; then
      warn "$bin not found in archive; skipping"
      continue
    fi
    install -m 0755 "$src" "$INSTALL_DIR/$bin"
    ok "Installed $bin to $INSTALL_DIR/$bin"
  done
}

build_from_source() {
  INSTALLED_FROM="source build"
  command -v go >/dev/null 2>&1 || { err "go is required to build from source"; return 1; }
  local src="$TMP/src"
  if [ -f "go.mod" ] && grep -q "module github.com/s1liconcow/skiff" go.mod; then
    src="$PWD"
  else
    local clone_args=(clone --depth 1)
    if [ -n "$VERSION" ] && [ "$VERSION" != "latest" ]; then
      clone_args+=(--branch "$VERSION")
    fi
    clone_args+=("$REPO_URL" "$src")
    run_with_spinner "Cloning Skiff source" git "${clone_args[@]}"
  fi
  local ldflags="-X github.com/s1liconcow/skiff/internal/buildinfo.Version=${VERSION:-dev} -X github.com/s1liconcow/skiff/internal/buildinfo.Commit=source -X github.com/s1liconcow/skiff/internal/buildinfo.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local bin
  for bin in "${BINARIES[@]}"; do
    run_with_spinner "Building $bin from source" bash -c '
      set -euo pipefail
      cd "$1"
      GOCACHE="$2" GOMODCACHE="$3" go build -trimpath -ldflags "$4" -o "$5" "./cmd/$6"
    ' _ "$src" "${GOCACHE:-$TMP/go-build}" "${GOMODCACHE:-$TMP/gomod}" "$ldflags" "$TMP/$bin" "$bin"
    install -m 0755 "$TMP/$bin" "$INSTALL_DIR/$bin"
    ok "Installed $bin to $INSTALL_DIR/$bin"
  done
}

installed_version_matches() {
  [ "$FORCE" = "1" ] && return 1
  [ -n "$VERSION" ] || return 1
  [ -x "$INSTALL_DIR/skiff" ] || return 1
  local out
  out=$("$INSTALL_DIR/skiff" version 2>/dev/null || true)
  [[ "$out" == *"$VERSION"* ]]
}

install_binaries() {
  if installed_version_matches; then
    ok "Skiff $VERSION is already installed in $INSTALL_DIR"
    info "Use --force to reinstall binaries; shell integration will still be refreshed"
    return 0
  fi
  if [ "$FROM_SOURCE" = "1" ]; then
    build_from_source
    return 0
  fi
  if [ -n "$OFFLINE_TARBALL" ]; then
    [ -f "$OFFLINE_TARBALL" ] || { err "Offline tarball not found: $OFFLINE_TARBALL"; return 1; }
    ASSET=$(basename "$OFFLINE_TARBALL")
    verify_checksum "$OFFLINE_TARBALL" "$(offline_checksum_for "$OFFLINE_TARBALL")"
    install_from_archive "$OFFLINE_TARBALL"
    INSTALLED_FROM="offline archive"
    return 0
  fi
  if download_release_archive; then
    install_from_archive "$DOWNLOADED_ARCHIVE"
    return 0
  fi
  warn "Prebuilt archive download failed; falling back to source build"
  build_from_source
}

install_completions() {
  if [ "$INSTALL_COMPLETIONS" != "1" ]; then
    COMPLETION_STATUS="skipped"
    return 0
  fi
  [ -x "$INSTALL_DIR/skiff" ] || { COMPLETION_STATUS="failed"; return 0; }
  local installed=0 shell_name target
  for shell_name in bash zsh fish; do
    case "$shell_name" in
      bash) target="${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/skiff" ;;
      zsh) target="${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions/_skiff" ;;
      fish) target="${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions/skiff.fish" ;;
    esac
    mkdir -p "$(dirname "$target")"
    if "$INSTALL_DIR/skiff" completion "$shell_name" > "$target" 2>/dev/null; then
      installed=$((installed + 1))
    fi
  done
  if [ "$installed" -gt 0 ]; then
    COMPLETION_STATUS="installed"
    ok "Installed shell completions"
  else
    COMPLETION_STATUS="failed"
    warn "Could not install shell completions"
  fi
}

maybe_add_path() {
  case ":$PATH:" in
    *:"$INSTALL_DIR":*) return 0 ;;
  esac
  if [ "$EASY_MODE" = "1" ]; then
    local rc changed=0 line
    line="export PATH=\"$INSTALL_DIR:\$PATH\""
    for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
      if [ -e "$rc" ] && [ -w "$rc" ] && ! grep -Fq "$line" "$rc" 2>/dev/null; then
        printf '\n# Added by Skiff installer\n%s\n' "$line" >> "$rc"
        changed=1
      fi
    done
    [ "$changed" = "1" ] && ok "Added $INSTALL_DIR to shell startup files"
  else
    warn "$INSTALL_DIR is not on PATH. Add it with: export PATH=\"$INSTALL_DIR:\$PATH\""
  fi
}

write_skill() {
  local dest="$1"
  local status_var="$2"
  local backup_var="$3"
  local skill="$dest/SKILL.md"
  mkdir -p "$dest"
  if [ -f "$skill" ] && grep -q "Skiff" "$skill" 2>/dev/null; then
    printf -v "$status_var" "already"
    return 0
  fi
  if [ -f "$skill" ]; then
    local backup="${skill}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$skill" "$backup"
    printf -v "$backup_var" "%s" "$backup"
  fi
  cat > "$skill" <<'EOF'
---
name: skiff
description: Use when operating Skiff, editing Skiff specs, deploying services, debugging rollouts, or using direct object-state recovery mode.
---

# Skiff

Skiff's durable source of truth is object storage. `skiffd` is a rebuildable facade, and the CLI must keep direct recovery mode working.

Use these defaults:
- Prefer `skiff --direct --state <uri> ... --format json` when recovering.
- Preserve create-only release, operation, saga, event, and audit objects.
- Use compare-and-swap control documents instead of separate lock files.
- Include trace IDs, operation IDs, facts, recommended commands, risk, and reversibility for agent-facing output.
- Do not store plaintext secrets in object state or events.

Common commands:
```bash
skiff validate skiff.yaml
skiff plan skiff.yaml --provider aws --region us-west-2
skiff deploy skiff.yaml --canary --format json
skiff status payments-api --fresh --format json
skiff doctor payments-api --fresh --format json
skiff --direct --state s3://skiff-state-prod rollback payments-api --to previous-stable --format json
```
EOF
  printf -v "$status_var" "installed"
}

install_agent_skills() {
  if [ "$CONFIGURE_AGENTS" != "1" ]; then
    CODEX_STATUS="skipped"
    CLAUDE_STATUS="skipped"
    return 0
  fi
  info "Scanning for coding-agent skill directories"
  if [ -d "$HOME/.codex" ] || command -v codex >/dev/null 2>&1; then
    write_skill "$HOME/.codex/skills/skiff" CODEX_STATUS CODEX_BACKUP || CODEX_STATUS="failed"
  else
    CODEX_STATUS="skipped"
  fi
  if [ -d "$HOME/.claude" ] || command -v claude >/dev/null 2>&1; then
    write_skill "$HOME/.claude/skills/skiff" CLAUDE_STATUS CLAUDE_BACKUP || CLAUDE_STATUS="failed"
  else
    CLAUDE_STATUS="skipped"
  fi
}

self_test() {
  if [ "$RUN_SELF_TEST" != "1" ]; then
    return 0
  fi
  SKIFF_VERSION_OUTPUT=$("$INSTALL_DIR/skiff" version 2>/dev/null || true)
  if [ -z "$SKIFF_VERSION_OUTPUT" ]; then
    err "Installed skiff did not pass version self-test"
    return 1
  fi
  ok "Self-test passed: $SKIFF_VERSION_OUTPUT"
}

summary_line_for_agent() {
  local name="$1" status="$2" backup="$3"
  case "$status" in
    installed) printf '%s: installed Skiff skill' "$name" ;;
    already) printf '%s: already configured' "$name" ;;
    failed) printf '%s: configuration failed' "$name" ;;
    skipped) printf '%s: skipped' "$name" ;;
    *) printf '%s: %s' "$name" "$status" ;;
  esac
  [ -n "$backup" ] && printf ' (backup: %s)' "$backup"
  printf '\n'
}

final_summary() {
  local lines=()
  lines+=("Installed from: $INSTALLED_FROM")
  lines+=("Binaries: ${BINARIES[*]} in $INSTALL_DIR")
  lines+=("Completions: $COMPLETION_STATUS")
  lines+=("$(summary_line_for_agent "Codex" "$CODEX_STATUS" "$CODEX_BACKUP")")
  lines+=("$(summary_line_for_agent "Claude" "$CLAUDE_STATUS" "$CLAUDE_BACKUP")")
  lines+=("Uninstall: rm -f $INSTALL_DIR/skiff $INSTALL_DIR/skiffd $INSTALL_DIR/skiff-runner $INSTALL_DIR/skiff-worker")
  lines+=("Next: export PATH=\"$INSTALL_DIR:\$PATH\"")
  if [ "$HAS_GUM" = "1" ] && [ "$NO_GUM" != "1" ] && [ "$QUIET" != "1" ]; then
    {
      gum style --foreground 42 --bold "Skiff install complete"
      echo
      printf '%s\n' "${lines[@]}"
    } | gum style --border normal --border-foreground 42 --padding "1 2"
  else
    draw_box "32" "$(ansi '1;32' 'Skiff install complete')" "${lines[@]}"
  fi
}

main() {
  trap cleanup EXIT INT TERM
  parse_args "$@"
  detect_gum
  banner
  setup_tmp_and_lock
  setup_proxy
  detect_platform
  if [ -z "$OFFLINE_TARBALL" ]; then
    run_with_spinner "Resolving release version" resolve_version
  fi
  ASSET=$(asset_name)
  preflight_checks
  install_binaries
  install_completions
  maybe_add_path
  install_agent_skills
  self_test
  final_summary
}

main "$@"
