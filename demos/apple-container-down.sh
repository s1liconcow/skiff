#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
report_dir="${SKIFF_E2E_REPORT_DIR:-$repo_root/.skiff-demo-reports/apple-container}"
env_file="${1:-}"

if [[ -z "$env_file" ]]; then
  shopt -s nullglob
  env_files=("$report_dir"/*.env)
  shopt -u nullglob
  if ((${#env_files[@]} > 0)); then
    env_file="$(ls -t "${env_files[@]}")"
    env_file="${env_file%%$'\n'*}"
  fi
fi

if [[ -z "$env_file" || ! -f "$env_file" ]]; then
  cat >&2 <<EOF
No Apple Container demo env file found.

Pass one explicitly, or run the persistent demo first:
  make demo-apple-context
EOF
  exit 1
fi

# shellcheck source=/dev/null
source "$env_file"

stop_skiffd() {
  local pid="${SKIFF_APPLE_SKIFFD_PID:-}"
  if [[ -z "$pid" ]]; then
    return
  fi
  if [[ ! "$pid" =~ ^[0-9]+$ ]]; then
    echo "Skipping invalid skiffd PID from env: $pid" >&2
    return
  fi
  if ! kill -0 "$pid" 2>/dev/null; then
    return
  fi
  kill "$pid" 2>/dev/null || true
  for _ in {1..20}; do
    if ! kill -0 "$pid" 2>/dev/null; then
      return
    fi
    sleep 0.1
  done
  kill -KILL "$pid" 2>/dev/null || true
}

stop_container() {
  local name="$1"
  if [[ -z "$name" ]]; then
    return
  fi
  container stop --time 2 "$name" >/dev/null 2>&1 || true
  container delete --force "$name" >/dev/null 2>&1 || true
}

delete_volume() {
  local name="$1"
  if [[ -z "$name" ]]; then
    return
  fi
  container volume delete "$name" >/dev/null 2>&1 || true
}

echo "Stopping Apple Container demo from $env_file"
stop_skiffd

if command -v container >/dev/null 2>&1; then
  stop_container "${SKIFF_APPLE_CADDY_CONTAINER:-}"
  stop_container "${SKIFF_APPLE_RUSTFS_CONTAINER:-}"
  delete_volume "${SKIFF_APPLE_RUSTFS_VOLUME:-}"
else
  echo "Apple Container CLI was not found; only skiffd cleanup was attempted." >&2
fi

echo "Apple Container demo cleanup complete."
