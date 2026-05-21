#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
postgres_image="${SKIFF_APPLE_POSTGRES_HA_IMAGE:-localhost/postgres-ha:apple}"

if ! command -v container >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Apple Container CLI was not found. Install/start Apple Container first.
EOF
  exit 1
fi

cd "$repo_root"

echo "building $postgres_image from examples/stateful/postgres-ha/Dockerfile"
container build -t "$postgres_image" -f examples/stateful/postgres-ha/Dockerfile .

cat <<EOF

Built Apple demo images:
  postgres-ha: $postgres_image

EOF
