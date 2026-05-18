#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
report_dir="${SKIFF_E2E_REPORT_DIR:-$repo_root/.skiff-demo-reports/apple-container}"
persist="${SKIFF_APPLE_CONTAINER_PERSIST:-${SKIFF_APPLE_CONTAINER_KEEPALIVE:-}}"

cat <<EOF
Skiff Apple Container workload demo

This launches the optional Apple Container/RustFS/Caddy e2e path. It pulls OCI
images, starts local Linux VMs through Apple Container, writes S3-compatible
Skiff object state to RustFS, rolls Caddy to a second signed release, starts a
local skiffd against that state, and runs a rolling canary saga.

Reports will be written to:
  $report_dir

EOF

if [[ "$persist" == "1" ]]; then
  cat <<'EOF'
Persistent mode is enabled. RustFS, Caddy, and skiffd will be left running so
you can use the generated Skiff contexts and TUI after this command exits.

EOF
fi

if ! command -v container >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Apple Container CLI was not found.

Install/start Apple Container first, or run the fast fake-provider demo instead:
  demos/quickstart-fake.sh
EOF
  exit 1
fi

missing_bins=()
for bin in skiff skiffd; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    missing_bins+=("$bin")
  fi
done
if (( ${#missing_bins[@]} > 0 )); then
  cat >&2 <<EOF
Skiff binaries were not found on PATH: ${missing_bins[*]}

Install Skiff first, then re-run this demo:
  SKIFF_INSTALL_VERSION=v0.1.0 SKIFF_INSTALL_DIR="\$HOME/.local/bin" "$repo_root/scripts/install.sh"
  export PATH="\$HOME/.local/bin:\$PATH"
EOF
  exit 1
fi

mkdir -p "$report_dir"
export SKIFF_E2E_REPORT_DIR="$report_dir"
make -C "$repo_root" e2e-apple-container

latest_env="$(ls -t "$report_dir"/*.env 2>/dev/null || true)"
latest_env="${latest_env%%$'\n'*}"
latest_config="$(ls -t "$report_dir"/*.skiffconfig 2>/dev/null || true)"
latest_config="${latest_config%%$'\n'*}"

if [[ -n "$latest_env" && -n "$latest_config" ]]; then
  cat <<EOF

Apple Container context artifacts:
  env:    $latest_env
  config: $latest_config

The demo used these contexts during the run:
  source "$latest_env"
  skiff config get-contexts
  SKIFF_CONTEXT=local-apple-vms skiff status caddy-web
  SKIFF_CONTEXT=local-apple-skiffd skiff tui caddy-web --read-only

EOF
  if [[ "$persist" == "1" ]]; then
    cat <<EOF
RustFS, Caddy, and skiffd are still running. Stop them with:
  make demo-apple-down

EOF
  else
    cat <<'EOF'
This smoke demo is cleanup-safe: the Apple containers and in-process skiffd are
stopped when the test exits. Use `make demo-apple-context` for a live context
that stays up.
EOF
  fi
fi
