#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
report_dir="${SKIFF_E2E_REPORT_DIR:-$repo_root/.skiff-demo-reports/apple-container}"
cleanup_all=0
env_file="${1:-}"

if [[ "${1:-}" == "--all" ]]; then
  cleanup_all=1
  env_file=""
fi

if [[ "$cleanup_all" -eq 0 && -z "$env_file" ]]; then
  shopt -s nullglob
  env_files=("$report_dir"/*.env)
  shopt -u nullglob
  if ((${#env_files[@]} > 0)); then
    env_file="$(ls -t "${env_files[@]}")"
    env_file="${env_file%%$'\n'*}"
  fi
fi

if [[ "$cleanup_all" -eq 0 && ( -z "$env_file" || ! -f "$env_file" ) ]]; then
  cat >&2 <<EOF
No Apple Container demo env file found.

Pass one explicitly, or run the persistent demo first:
  make demo-apple-context
EOF
  exit 1
fi

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
  local command_line
  command_line="$(ps -p "$pid" -o command= 2>/dev/null || ps -p "$pid" -o args= 2>/dev/null || true)"
  if [[ "$command_line" != *skiffd* ]]; then
    echo "Skipping PID from env because it is not skiffd: $pid" >&2
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
  echo "Stopping Apple container: $name"
  container stop --time 2 "$name" >/dev/null 2>&1 || true
  container delete --force "$name" >/dev/null 2>&1 || true
}

delete_volume() {
  local name="$1"
  if [[ -z "$name" ]]; then
    return
  fi
  echo "Deleting Apple container volume: $name"
  container volume delete "$name" >/dev/null 2>&1 || true
}

cleanup_env_file() {
  local path="$1"

  (
    unset SKIFF_APPLE_CADDY_CONTAINER
    unset SKIFF_APPLE_RUSTFS_CONTAINER
    unset SKIFF_APPLE_RUSTFS_VOLUME
    unset SKIFF_APPLE_SKIFFD_PID

    # shellcheck source=/dev/null
    source "$path"

    echo "Stopping Apple Container demo from $path"
    stop_skiffd

    if command -v container >/dev/null 2>&1; then
      stop_container "${SKIFF_APPLE_CADDY_CONTAINER:-}"
      stop_container "${SKIFF_APPLE_RUSTFS_CONTAINER:-}"
      delete_volume "${SKIFF_APPLE_RUSTFS_VOLUME:-}"
    else
      echo "Apple Container CLI was not found; only skiffd cleanup was attempted." >&2
    fi
  )
}

is_skiff_apple_container() {
  case "$1" in
    skiff-e2e-*-caddy|skiff-e2e-*-rustfs)
      return 0
      ;;
  esac
  return 1
}

is_skiff_apple_volume() {
  case "$1" in
    skiff-e2e-*-rustfs-data)
      return 0
      ;;
  esac
  return 1
}

cleanup_discovered_skiff_resources() {
  local names name volumes volume

  if ! command -v container >/dev/null 2>&1; then
    echo "Apple Container CLI was not found; skipped container discovery." >&2
    return
  fi

  if names="$(container list --all --quiet 2>/dev/null)"; then
    while IFS= read -r name; do
      if [[ -n "$name" ]] && is_skiff_apple_container "$name"; then
        stop_container "$name"
      fi
    done <<< "$names"
  else
    echo "Could not list Apple containers; is Apple Container running?" >&2
  fi

  if volumes="$(container volume list --quiet 2>/dev/null)"; then
    while IFS= read -r volume; do
      if [[ -n "$volume" ]] && is_skiff_apple_volume "$volume"; then
        delete_volume "$volume"
      fi
    done <<< "$volumes"
  else
    echo "Could not list Apple container volumes; is Apple Container running?" >&2
  fi
}

if [[ "$cleanup_all" -eq 1 ]]; then
  shopt -s nullglob
  env_files=("$report_dir"/*.env)
  shopt -u nullglob

  if ((${#env_files[@]} > 0)); then
    for env_file in "${env_files[@]}"; do
      cleanup_env_file "$env_file"
    done
  fi

  cleanup_discovered_skiff_resources
else
  cleanup_env_file "$env_file"
fi

echo "Apple Container demo cleanup complete."
