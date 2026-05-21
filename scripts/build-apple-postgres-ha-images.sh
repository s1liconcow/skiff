#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
postgres_image="${SKIFF_APPLE_POSTGRES_HA_IMAGE:-localhost/postgres-ha:apple}"
orders_image="${SKIFF_APPLE_ORDERS_API_IMAGE:-localhost/orders-rpc:apple}"
build_orders="${SKIFF_APPLE_BUILD_ORDERS_API:-0}"

if ! command -v container >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Apple Container CLI was not found. Install/start Apple Container first.
EOF
  exit 1
fi

cd "$repo_root"

echo "building $postgres_image from examples/stateful/postgres-ha/Dockerfile"
container build -t "$postgres_image" -f examples/stateful/postgres-ha/Dockerfile .

if [[ "$build_orders" == "1" ]]; then
  echo "building $orders_image from examples/stacks/api-multiregion-database/Dockerfile"
  container build -t "$orders_image" -f examples/stacks/api-multiregion-database/Dockerfile .
fi

cat <<EOF

Built Apple demo images:
  postgres-ha: $postgres_image
$(if [[ "$build_orders" == "1" ]]; then printf '  orders-api:  %s\n' "$orders_image"; fi)

EOF
