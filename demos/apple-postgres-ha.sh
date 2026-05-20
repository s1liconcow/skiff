#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
run_id="${SKIFF_DEMO_RUN_ID:-postgres-ha-$(date +%Y%m%d%H%M%S)}"
if [[ -n "${SKIFF_E2E_REPORT_DIR:-}" ]]; then
  report_dir="$SKIFF_E2E_REPORT_DIR"
else
  report_dir="$repo_root/.skiff-demo-reports/apple-postgres-ha/$run_id"
fi
opsem_image="${SKIFF_E2E_OPSEM_IMAGE:-localhost/skiff-opsem:e2e}"
filter="${SKIFF_OPSEM_PROFILE_FILTER:-postgres-primary}"

cat <<EOF
Skiff postgres-ha Apple Silicon package demo

This demo uses the actual installable package in packages/postgres-ha. It does
not start a standalone Postgres container as a substitute.

The demo will:
  - start RustFS for S3-compatible Skiff object state
  - apply live Apple StatefulGroups for the filtered postgres-ha scenarios
  - lock packages/postgres-ha into skiff.lock.json files under the report dir
  - build and execute cmd/postgres-ha-plugin through the package host
  - run primary-switchover-update successfully
  - run the unsafe replica-lag scenario and verify it blocks before mutation
  - verify direct-mode and local skiffd operation/saga inspect surfaces

Reports will be written to:
  $report_dir

EOF

if ! command -v container >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Apple Container CLI was not found. Install/start Apple Container first.
EOF
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  cat >&2 <<'EOF'
go was not found on PATH.
EOF
  exit 1
fi

if ! container image list 2>/dev/null | grep -Eq 'localhost/skiff-opsem[[:space:]]+e2e'; then
  cat >&2 <<EOF
Required local image not found: $opsem_image

Build the semantics workload image first:
  container build -t localhost/skiff-opsem:e2e -f tests/fixtures/opsem/Dockerfile .

Then rerun:
  make demo-apple-postgres-ha
EOF
  exit 1
fi

mkdir -p "$report_dir"

if compgen -G "$report_dir/*-skiff.lock.json" >/dev/null; then
  cat >&2 <<EOF
Report directory already contains package lockfiles:
  $report_dir

Use a fresh report directory for each demo run, for example:
  SKIFF_E2E_REPORT_DIR="$repo_root/.skiff-demo-reports/apple-postgres-ha/postgres-ha-\$(date +%Y%m%d%H%M%S)" make demo-apple-postgres-ha
EOF
  exit 1
fi

SKIFF_APPLE_STATEFUL_PACKAGES_E2E=1 \
SKIFF_OPSEM_PROFILE_FILTER="$filter" \
SKIFF_E2E_OPSEM_IMAGE="$opsem_image" \
SKIFF_E2E_REPORT_DIR="$report_dir" \
GOCACHE="$repo_root/.cache/go-build" \
GOMODCACHE="$repo_root/.cache/gomod" \
go test ./tests/e2e -run TestOpsemAppleOperationProfilesE2E -count=1 -v

report="$report_dir/testopsemappleoperationprofilese2e.json"
cat <<EOF

postgres-ha Apple demo complete.

Report:
  $report

EOF

if command -v jq >/dev/null 2>&1 && [[ -f "$report" ]]; then
  cat <<'EOF'
Postgres HA package evidence:
EOF
  jq '.facts[] | select((.message // "") | test("postgres-ha|postgres-primary|replica-lag-too-high|direct status"))' "$report"
fi
