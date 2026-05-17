#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
report_dir="${SKIFF_E2E_REPORT_DIR:-$repo_root/.skiff-demo-reports/apple-container}"

cat <<EOF
Skiff Apple Container workload demo

This launches the optional Apple Container/RustFS/Caddy e2e path. It pulls OCI
images, starts local Linux VMs through Apple Container, writes S3-compatible
Skiff object state to RustFS, rolls Caddy to a second signed release, starts a
local skiffd against that state, and runs a rolling canary saga.

Reports will be written to:
  $report_dir

EOF

if ! command -v container >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Apple Container CLI was not found.

Install/start Apple Container first, or run the fast fake-provider demo instead:
  demos/quickstart-fake.sh
EOF
  exit 1
fi

mkdir -p "$report_dir"
export SKIFF_E2E_REPORT_DIR="$report_dir"
exec make -C "$repo_root" e2e-apple-container
