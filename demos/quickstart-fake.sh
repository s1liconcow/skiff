#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SKIFF_BIN="${SKIFF_BIN:-go run ./cmd/skiff}"
OUT="${DEMO_OUT:-demos/out/quickstart-fake}"
SPEC="${SKIFF_SPEC:-examples/service/http-hello/skiff.yaml}"
STATE_DIR="${DEMO_STATE_DIR:-$(mktemp -d)}"
STATE_URI="file://${STATE_DIR}"
SIGNING_SEED="${SKIFF_RELEASE_SIGNING_SEED:-$(printf '%*s' 32 '' | tr ' ' D | base64 | tr -d '\n')}"

if [[ -z "${DEMO_STATE_DIR:-}" ]]; then
  trap 'rm -rf "$STATE_DIR"' EXIT
fi

mkdir -p "$OUT"

skiff() {
  # Allow SKIFF_BIN="go run ./cmd/skiff" for source-tree demos.
  # shellcheck disable=SC2086
  $SKIFF_BIN "$@"
}

run_json() {
  local name="$1"
  shift
  echo "+ skiff $*" >&2
  skiff "$@" > "$OUT/${name}.json"
}

run_json validate validate "$SPEC" --format json --trace-id tr_demo_quickstart
run_json plan plan "$SPEC" --provider aws --region us-west-2 --state "$STATE_URI" --format json --trace-id tr_demo_quickstart

run_json deploy-01 deploy "$SPEC" \
  --direct --state "$STATE_URI" --env prod --provider fake --region local \
  --release-id rel_demo_01 \
  --operation-id op_demo_01 \
  --key-id demo \
  --signing-seed-base64 "$SIGNING_SEED" \
  --format json \
  --trace-id tr_demo_quickstart

run_json deploy-02 deploy "$SPEC" \
  --canary \
  --canary-stages 100 \
  --canary-bake 0s \
  --direct --state "$STATE_URI" --env prod --provider fake --region local \
  --release-id rel_demo_02 \
  --operation-id op_demo_02 \
  --key-id demo \
  --signing-seed-base64 "$SIGNING_SEED" \
  --format json \
  --trace-id tr_demo_quickstart

run_json status status http-hello --direct --state "$STATE_URI" --env prod --provider fake --region local --format json --trace-id tr_demo_quickstart
run_json logs logs http-hello --direct --state "$STATE_URI" --env prod --provider fake --region local --format json --trace-id tr_demo_quickstart
run_json metrics metrics http-hello --direct --state "$STATE_URI" --env prod --provider fake --region local --format json --trace-id tr_demo_quickstart
run_json doctor doctor http-hello --direct --state "$STATE_URI" --env prod --provider fake --region local --fresh --format json --trace-id tr_demo_quickstart

run_json rollback rollback http-hello \
  --to rel_demo_01 \
  --operation-id op_demo_rollback \
  --saga-id saga_demo_rollback \
  --direct --state "$STATE_URI" --env prod --provider fake --region local \
  --format json \
  --trace-id tr_demo_quickstart

run_json events events --scope service --service http-hello --direct --state "$STATE_URI" --env prod --provider fake --region local --format json --trace-id tr_demo_quickstart

echo "wrote demo artifacts to $OUT" >&2
