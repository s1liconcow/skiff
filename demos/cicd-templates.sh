#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SKIFF_BIN="${SKIFF_BIN:-go run ./cmd/skiff}"
OUT="${DEMO_OUT:-demos/out/cicd-templates}"
mkdir -p "$OUT"

skiff() {
  # Allow SKIFF_BIN="go run ./cmd/skiff" for source-tree demos.
  # shellcheck disable=SC2086
  $SKIFF_BIN "$@"
}

echo "+ skiff ci generate github-actions" >&2
skiff ci generate github-actions --service payments-api --out "$OUT/github-actions.yml" --format human

echo "+ skiff ci generate gitlab" >&2
skiff ci generate gitlab --service payments-api --out "$OUT/gitlab.yml" --format human

echo "+ skiff ci generate buildkite" >&2
skiff ci generate buildkite --service payments-api --out "$OUT/buildkite.yml" --format human

echo "+ skiff contract test tests/fixtures/services/minimal.yaml" >&2
skiff contract test tests/fixtures/services/minimal.yaml --format json --trace-id tr_demo_cicd > "$OUT/contract.json"

echo "wrote CI/CD demo artifacts to $OUT" >&2
