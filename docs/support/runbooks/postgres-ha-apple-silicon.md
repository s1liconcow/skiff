# Runbook: Apple Silicon Postgres HA Package Demo

Use this on an Apple Silicon Mac to demo the actual installable
`skiff.dev/postgres-ha` package without AWS credentials.

This path does not start a standalone Postgres container as a substitute. It
locks the first-party package from `packages/postgres-ha`, builds and executes
`cmd/postgres-ha-plugin` through Skiff's package host, applies a live
three-member Apple StatefulGroup, runs the same `primary-switchover-update`
stateful operation profile documented in
[`api-postgres-ha-read-write.md`](api-postgres-ha-read-write.md), verifies
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
- `jq` is available for reading the report.
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
export SKIFF_E2E_REPORT_DIR=$PWD/.skiff-demo-reports/apple-postgres-ha/$APPLE_RUN_ID
export SKIFF_E2E_OPSEM_IMAGE=localhost/skiff-opsem:e2e
export POSTGRES_HA_TARGET=postgres-primary
export POSTGRES_HA_OPERATION=primary-switchover-update
export POSTGRES_HA_OP_ID=op_opsem_primary
export POSTGRES_HA_SAGA_ID=saga_opsem_primary
export POSTGRES_HA_RELEASE_ID=rel_primary
export POSTGRES_HA_CANDIDATE=1
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
actual `packages/postgres-ha` package, runs the package-exposed
`primary-switchover-update` StatefulGroup operation, verifies direct-mode and
local `skiffd` surfaces, and checks the unsafe replica-lag block.

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

## Run The Same Stateful Operation On Apple Silicon

The focused demo target runs the same package operation shape as the AWS runbook,
with the Apple local validation provider replacing AWS. The target is the
`postgres-primary` StatefulGroup created by the demo, and the lockfile/cache are
the package artifacts written under the run report directory:

```bash
export POSTGRES_HA_LOCKFILE=$SKIFF_E2E_REPORT_DIR/op-opsem-primary-skiff.lock.json
export POSTGRES_HA_CACHE=$SKIFF_E2E_REPORT_DIR/package-cache/op_opsem_primary
export APPLE_REPORT=$SKIFF_E2E_REPORT_DIR/testopsemappleoperationprofilese2e.json
export APPLE_STATE_URI=$(jq -r '.state_uri' "$APPLE_REPORT")
```

Confirm the locked `postgres-ha` package exposes the stateful operation:

```bash
skiff ops list "$POSTGRES_HA_TARGET" \
  --target-kind StatefulGroup \
  --lockfile "$POSTGRES_HA_LOCKFILE" \
  --cache "$POSTGRES_HA_CACHE"
```

Plan the package operation without writing object state:

```bash
skiff ops plan "$POSTGRES_HA_TARGET" "$POSTGRES_HA_OPERATION" \
  --target-kind StatefulGroup \
  --lockfile "$POSTGRES_HA_LOCKFILE" \
  --cache "$POSTGRES_HA_CACHE" \
  --operation-id "$POSTGRES_HA_OP_ID" \
  --saga-id "$POSTGRES_HA_SAGA_ID" \
  --param release_id="$POSTGRES_HA_RELEASE_ID" \
  --param "candidate=\"$POSTGRES_HA_CANDIDATE\"" \
  --param return_primary=true
```

Run the operation through direct mode:

```bash
skiff ops run "$POSTGRES_HA_TARGET" "$POSTGRES_HA_OPERATION" \
  --direct \
  --state "$APPLE_STATE_URI" \
  --env prod \
  --provider apple-container \
  --region local \
  --target-kind StatefulGroup \
  --lockfile "$POSTGRES_HA_LOCKFILE" \
  --cache "$POSTGRES_HA_CACHE" \
  --operation-id "$POSTGRES_HA_OP_ID" \
  --saga-id "$POSTGRES_HA_SAGA_ID" \
  --param release_id="$POSTGRES_HA_RELEASE_ID" \
  --param "candidate=\"$POSTGRES_HA_CANDIDATE\"" \
  --param return_primary=true \
  --yes \
  --trace-id tr_opsem_profiles_e2e
```

Inspect the durable operation and saga objects:

```bash
skiff ops inspect "$POSTGRES_HA_OP_ID" \
  --service "$POSTGRES_HA_TARGET" \
  --direct \
  --state "$APPLE_STATE_URI" \
  --env prod \
  --provider apple-container \
  --region local \
  --trace-id tr_opsem_profiles_e2e

skiff saga inspect "$POSTGRES_HA_SAGA_ID" \
  --direct \
  --state "$APPLE_STATE_URI" \
  --env prod \
  --provider apple-container \
  --region local \
  --trace-id tr_opsem_profiles_e2e
```

The all-package gate is the recommended repeatable path because it creates
isolated names, captures report evidence, verifies the direct and local
`skiffd` surfaces, runs the unsafe-lag case, and cleans up Apple containers and
volumes by default.

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
