# Runbook: Apple Silicon Postgres HA Package Demo

Use this on an Apple Silicon Mac to demo the actual installable
`skiff.dev/postgres-ha` package without AWS credentials.

This path does not start a standalone Postgres container as a substitute. It
locks the first-party package from `packages/postgres-ha`, builds and executes
`cmd/postgres-ha-plugin` through Skiff's package host, applies a live
three-member Apple StatefulGroup, runs `primary-switchover-update`, verifies
direct-mode and local `skiffd` surfaces, and proves the unsafe replica-lag
scenario blocks before member mutation.

The Apple provider is a local validation provider. It is useful for proving
Skiff object-state, runner, StatefulGroup, package operation, and direct-mode
behavior without AWS credentials. It is not the production path for managed RDS
or Aurora.

Today the self-managed Apple package demo uses `skiff-opsem primary-replica` as
the package's live semantics workload. That workload exposes the admin endpoints
the `postgres-ha` package plugin uses for planned primary movement, replica
catch-up, failback, direct-mode inspection, and unsafe-lag blocking.

## Prerequisites

- Apple Silicon Mac.
- Apple Container installed and running, with `container` on `PATH`.
- `go` is available.
- `jq` is optional but useful for reading the report.
- Network access to pull `rustfs/rustfs` and any missing builder images.

Confirm the basics:

```bash
uname -m
container --version
skiff version
go version
```

Expected architecture is `arm64`.

Set shared variables from the repository root:

```bash
export APPLE_RUN_ID=postgres-ha-apple-$(date +%Y%m%d%H%M%S)
export SKIFF_E2E_REPORT_DIR=$PWD/.skiff-demo-reports/apple-postgres-ha
export SKIFF_E2E_OPSEM_IMAGE=localhost/skiff-opsem:e2e
```

## Build Or Confirm The Opsem Image

The Postgres HA stateful operation uses `skiff-opsem primary-replica` as the
local Apple semantics harness. If the image is already present, keep using it:

```bash
container image list | rg 'localhost/skiff-opsem\s+e2e'
```

If it is missing, build it:

```bash
container build \
  -t localhost/skiff-opsem:e2e \
  -f tests/fixtures/opsem/Dockerfile \
  .
```

## Run The Apple Stateful Package Gate

Run the focused demo target:

```bash
make demo-apple-postgres-ha
```

Equivalent explicit command:

```bash
SKIFF_APPLE_STATEFUL_PACKAGES_E2E=1 \
SKIFF_OPSEM_PROFILE_FILTER=postgres-primary \
SKIFF_E2E_OPSEM_IMAGE="$SKIFF_E2E_OPSEM_IMAGE" \
SKIFF_E2E_REPORT_DIR="$SKIFF_E2E_REPORT_DIR" \
GOCACHE=$PWD/.cache/go-build \
GOMODCACHE=$PWD/.cache/gomod \
go test ./tests/e2e -run TestOpsemAppleOperationProfilesE2E -count=1 -v
```

This starts RustFS for S3-compatible Skiff object state, applies live Apple
StatefulGroups for the `postgres-ha` success and unsafe scenarios, locks the
actual `packages/postgres-ha` package, runs `primary-switchover-update`,
verifies direct-mode and local `skiffd` surfaces, and checks the unsafe
replica-lag block.

The report path is:

```bash
export APPLE_REPORT="$SKIFF_E2E_REPORT_DIR/testopsemappleoperationprofilese2e.json"
```

Inspect the Postgres HA evidence:

```bash
jq '.facts[] | select((.message // "") | test("postgres-ha|postgres-primary"))' "$APPLE_REPORT"
```

Expected facts include:

- the first-party `postgres-ha` package was locked from `packages/postgres-ha`
- `package=postgres-ha ... mode=self-managed opsem_mode=primary-replica`
- `postgres-primary` verified direct status, doctor, ops events/watch/inspect,
  and saga inspect
- `postgres-primary-unsafe` blocked `replica-lag-too-high` before member
  mutation

## Direct Operation Commands

To inspect the command shape outside the all-package gate, use the operation
profile directly against the live `primary-replica` StatefulGroup created by the
demo:

```bash
skiff ops plan postgres-primary primary-switchover-update \
  --target-kind StatefulGroup \
  --lockfile "$SKIFF_E2E_REPORT_DIR/op-opsem-primary-skiff.lock.json" \
  --cache "$SKIFF_E2E_REPORT_DIR/package-cache/op_opsem_primary" \
  --operation-id op_opsem_primary \
  --saga-id saga_opsem_primary \
  --param release_id=rel_primary \
  --param 'candidate="1"' \
  --param return_primary=true \
  --format json
```

The corresponding mutating command is:

```bash
skiff ops run postgres-primary primary-switchover-update \
  --direct \
  --state <rustfs-state-uri> \
  --env prod \
  --provider apple-container \
  --region local \
  --target-kind StatefulGroup \
  --lockfile "$SKIFF_E2E_REPORT_DIR/op-opsem-primary-skiff.lock.json" \
  --cache "$SKIFF_E2E_REPORT_DIR/package-cache/op_opsem_primary" \
  --operation-id op_opsem_primary \
  --saga-id saga_opsem_primary \
  --param release_id=rel_primary \
  --param 'candidate="1"' \
  --param return_primary=true \
  --yes \
  --format json
```

Use the `<rustfs-state-uri>` from the Apple report or e2e output. The all-package
gate is the recommended repeatable path because it creates isolated names,
captures report evidence, and cleans up Apple containers and volumes by default.

## Cleanup

If a failed Apple e2e leaves resources behind, inspect the report and then use:

```bash
make clean-apple-containers
```

## Evidence To Save

- Apple e2e report JSON path
- Postgres HA package digest in the report
- operation ID and saga ID for `primary-switchover-update`
- unsafe-lag blocked scenario fact
