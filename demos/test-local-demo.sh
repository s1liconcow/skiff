#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
tmp_parent="${TMPDIR:-/tmp}"
state_dir="$(mktemp -d "$tmp_parent/skiff-local-demo.XXXXXX")"
config_path="$(mktemp "$tmp_parent/skiff-local-demo-config.XXXXXX")"

cleanup() {
  rm -rf "$state_dir"
  rm -f "$config_path"
}
trap cleanup EXIT

printf 'Testing local Skiff demo with isolated state: %s\n' "$state_dir"
"$repo_root/demos/quickstart-fake.sh" --state-dir "$state_dir" --config "$config_path" --no-tui --reset "$@"
printf '\nLocal demo smoke test passed.\n'
