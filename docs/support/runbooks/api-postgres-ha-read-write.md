# Runbook: API plus Postgres HA Read/Write And Stateful Operation

Use this runbook to deploy the orders API example, provision Postgres through
the installable `skiff.dev/postgres-ha` package, verify database reads/writes,
and run the package-exposed `primary-switchover-update` stateful operation.

This is one runbook for AWS and Apple Silicon. Pick a provider profile, then run
the common commands unchanged. The operation target is a `postgres-ha`
StatefulGroup, and the operation is resolved from `skiff.lock.json`; it is not a
generic StatefulGroup compatibility alias.

The read/write service is
[`examples/stacks/api-multiregion-database`](../../../examples/stacks/api-multiregion-database)
because it exposes JSON-RPC methods that actually touch Postgres.

## Prerequisites

- `go`, `skiff`, `jq`, and `curl` are available.
- `IMAGE_REF` points at an orders API OCI image the selected provider can pull.
- `POSTGRES_HA_IMAGE_REF` points at a real self-managed `postgres-ha` OCI image
  that exposes the package admin API on port `8008`.
- For AWS, bootstrap the environment first and use an isolated state bucket.
- For Apple Silicon, Apple Container must be installed and object state must
  point at an S3-compatible endpoint such as local RustFS.

## Choose Provider Profile

AWS:

```bash
export SKIFF_ENV=prod
export SKIFF_PROVIDER=aws
export SKIFF_REGION=${AWS_REGION:-us-west-2}
export AWS_REGION=$SKIFF_REGION
export AWS_DEFAULT_REGION=$SKIFF_REGION
export SKIFF_STATE=s3://skiff-state-prod
export INGRESS_TYPE=${INGRESS_TYPE:-internal-http}
export IMAGE_REF=123456789012.dkr.ecr.$SKIFF_REGION.amazonaws.com/orders-rpc@sha256:...
export POSTGRES_HA_IMAGE_REF=123456789012.dkr.ecr.$SKIFF_REGION.amazonaws.com/postgres-ha@sha256:...
export API_URL=https://orders-api.example.com
```

Apple Silicon:

```bash
export SKIFF_ENV=prod
export SKIFF_PROVIDER=apple-container
export SKIFF_REGION=local
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION=us-east-1
export AWS_ACCESS_KEY_ID=skiffdev
export AWS_SECRET_ACCESS_KEY=skiffdevsecret
export SKIFF_AWS_ENDPOINT=http://127.0.0.1:9000
export SKIFF_AWS_S3_PATH_STYLE=true
export SKIFF_STATE=s3://skiff-apple-postgres-ha
export INGRESS_TYPE=${INGRESS_TYPE:-internal-http}
export IMAGE_REF=localhost/orders-rpc:apple
export POSTGRES_HA_IMAGE_REF=localhost/postgres-ha:apple
export API_URL=http://127.0.0.1:8080
```

Common variables:

```bash
export TRACE_ID=tr_orders_api_db_postgres_ha
export LOCKFILE=$PWD/skiff.lock.json
export PKG_CACHE=$PWD/.skiff/packages/cache
export API_SPEC=/tmp/orders-api-postgres-ha.skiff.yaml
export POSTGRES_HA_SPEC=/tmp/orders-postgres-ha-stateful.skiff.yaml
export POSTGRES_HA_TARGET=orders-db
export POSTGRES_HA_OPERATION=primary-switchover-update
export POSTGRES_HA_APPLY_OP_ID=op_orders_db_apply
export POSTGRES_HA_OP_ID=op_orders_db_primary_switchover
export POSTGRES_HA_SAGA_ID=saga_orders_db_primary_switchover
export POSTGRES_HA_RELEASE_ID=rel_orders_db_$(date +%Y%m%d%H%M%S)
export POSTGRES_HA_CANDIDATE=1
```

## Install The Postgres HA Package

Install the package plugin and lock the package from this checkout:

```bash
go install ./cmd/postgres-ha-plugin

skiff pkg add skiff.dev/postgres-ha \
  --registry-dir packages \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"

skiff pkg verify postgres-ha \
  --conformance \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"
```

## Write The API Stack Spec

This spec deploys the orders API and a managed Postgres dependency exported by
`skiff.dev/postgres-ha`. Skiff injects the connection secret into the service as
`DATABASE_URL`.

```bash
cat > "$API_SPEC" <<EOF
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: ${SKIFF_ENV}
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: ${IMAGE_REF}
      runtime:
        port: 8080
        env:
          SKIFF_STACK: orders
          PORT: "8080"
        health:
          path: /readyz
      scale:
        min: 2
        max: 4
      network:
        ingress:
          type: ${INGRESS_TYPE}
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: managed
        engine: postgres
        version: "16"
        size: small
        storage:
          sizeGB: 20
          type: gp3
          encrypted: true
        backups:
          enabled: true
          retentionDays: 7
        network:
          private: true
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
EOF
```

## Write The Postgres HA Stateful Spec

This spec creates the stateful target for the package operation. Use a real
`postgres-ha` image for `POSTGRES_HA_IMAGE_REF`; do not substitute a test
semantics harness.

```bash
cat > "$POSTGRES_HA_SPEC" <<EOF
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: ${POSTGRES_HA_TARGET}
  env: ${SKIFF_ENV}
stateful:
  replicas: 3
  members:
    - ordinal: 0
      dnsName: ${POSTGRES_HA_TARGET}-0.local
    - ordinal: 1
      dnsName: ${POSTGRES_HA_TARGET}-1.local
    - ordinal: 2
      dnsName: ${POSTGRES_HA_TARGET}-2.local
  volume:
    size: 100Gi
    mountPath: /data
    encrypted: true
  recipe:
    name: postgres-ha
    config:
      artifact:
        type: oci
        ref: ${POSTGRES_HA_IMAGE_REF}
      runtime:
        command:
          - /usr/local/bin/postgres-ha
        ports:
          postgres: 5432
          admin: 8008
        health:
          path: /healthz
          port: 8008
  update:
    strategy: ordered
EOF
```

## Validate And Preview

```bash
skiff validate "$API_SPEC" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"

skiff explain "$API_SPEC" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --state "$SKIFF_STATE" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"

skiff stateful plan "$POSTGRES_HA_SPEC" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --state "$SKIFF_STATE"

skiff ops list "$POSTGRES_HA_TARGET" \
  --target-kind StatefulGroup \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"
```

The preview should show visible provider primitives for the API, managed
database, and stateful Postgres HA target. The operation list should include
`primary-switchover-update` with `postgres-ha` package provenance.

## Deploy

```bash
skiff deploy "$API_SPEC" \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE" \
  --trace-id "$TRACE_ID"

skiff stateful apply "$POSTGRES_HA_SPEC" \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --operation-id "$POSTGRES_HA_APPLY_OP_ID" \
  --trace-id "$TRACE_ID"
```

Record the deploy operation ID, API release ID, StatefulGroup apply operation
ID, and provider resource IDs from the command output. If deployment is
interrupted, resume from object state rather than starting a second mutating
operation.

## Verify Service And Database Health

Use direct mode to confirm service and stateful target health:

```bash
skiff status orders-api \
  --fresh \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --trace-id "$TRACE_ID"

skiff doctor orders-api \
  --fresh \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --trace-id "$TRACE_ID"

skiff stateful status "$POSTGRES_HA_TARGET" \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --trace-id "$TRACE_ID"
```

For an internal load balancer, run the HTTP requests from inside the provider
network or through an approved network path.

```bash
curl -fsS "$API_URL/readyz"
```

The readiness endpoint pings Postgres and creates the `orders` table if it is
missing.

## Prove Database Writes And Reads

Write an order:

```bash
curl -fsS "$API_URL/rpc" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"add-1","method":"orders.add","params":{"customer":"acme","sku":"sku-123","quantity":2}}' | jq .
```

Read it back:

```bash
curl -fsS "$API_URL/rpc" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"list-1","method":"orders.list","params":{"limit":20}}' | jq .
```

Check the database-backed metric:

```bash
curl -fsS "$API_URL/metrics"
```

Successful output proves the deployed API has a working `DATABASE_URL` binding,
can initialize schema, can insert a row, can query rows back, and can count rows
from Postgres.

## Run The Postgres HA Stateful Operation

Plan the package operation without writing object state:

```bash
skiff ops plan "$POSTGRES_HA_TARGET" "$POSTGRES_HA_OPERATION" \
  --target-kind StatefulGroup \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE" \
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
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --target-kind StatefulGroup \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE" \
  --operation-id "$POSTGRES_HA_OP_ID" \
  --saga-id "$POSTGRES_HA_SAGA_ID" \
  --param release_id="$POSTGRES_HA_RELEASE_ID" \
  --param "candidate=\"$POSTGRES_HA_CANDIDATE\"" \
  --param return_primary=true \
  --yes \
  --trace-id "$TRACE_ID"
```

Inspect the durable operation and saga objects:

```bash
skiff ops inspect "$POSTGRES_HA_OP_ID" \
  --service "$POSTGRES_HA_TARGET" \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --trace-id "$TRACE_ID"

skiff saga inspect "$POSTGRES_HA_SAGA_ID" \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --trace-id "$TRACE_ID"
```

Expected result: Skiff writes immutable operation intent, saga intent, saga
graph, operation events, saga events, and audit records under object state. The
rendered graph identifies the `postgres-ha` package digest, classifies the
switchover as high risk and partially reversible, verifies the candidate's
replica lag before mutation, moves the primary only after the safety gates pass,
and verifies the final topology.

## Failure Triage

Collect state before mutating:

```bash
skiff logs orders-api \
  --since 20m \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --trace-id "$TRACE_ID"

skiff ops events orders-api \
  --direct \
  --state "$SKIFF_STATE" \
  --trace-id "$TRACE_ID"

skiff ops events "$POSTGRES_HA_TARGET" \
  --direct \
  --state "$SKIFF_STATE" \
  --trace-id "$TRACE_ID"
```

Common checks:

- `DATABASE_URL` should be a secret reference in the runtime manifest, not a
  plaintext password in object state.
- The API target group should use `/readyz`, not `/healthz`, so traffic is sent
  only after the database is reachable.
- The API image and `postgres-ha` image should be pullable by the selected
  provider.
- The stateful operation should fail before mutation if the candidate is not
  caught up.

## Roll Back The API Release

If the application release is bad and a previous stable release exists:

```bash
skiff rollback orders-api \
  --to previous-stable \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider "$SKIFF_PROVIDER" \
  --region "$SKIFF_REGION" \
  --approval-id <approval-id> \
  --trace-id "$TRACE_ID"
```

This rolls back the API release. It does not rewind database writes. Use an
explicit database restore operation when data must move.

## Evidence To Save

- provider profile values
- API stack spec and Postgres HA StatefulGroup spec
- `skiff.lock.json` package entry for `postgres-ha`
- API deploy operation ID and release ID
- StatefulGroup apply operation ID
- provider resource IDs
- `curl /readyz` output
- `orders.add` response
- `orders.list` response showing the inserted order
- `curl /metrics` output showing the order count
- `POSTGRES_HA_OP_ID` and `POSTGRES_HA_SAGA_ID`
- `postgres-ha` package digest from the operation plan
- `primary-switchover-update` plan/run/inspect output
