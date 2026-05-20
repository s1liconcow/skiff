# Runbook: API plus Postgres HA Read/Write Smoke

Use this runbook to deploy an API service through Skiff, provision a Postgres
database through the installable `skiff.dev/postgres-ha` package, and verify
that the deployed API can write to and read from that database.

This runbook uses the read/write service in
[`examples/stacks/api-multiregion-database`](../../../examples/stacks/api-multiregion-database)
because it exposes JSON-RPC methods that actually touch Postgres. The simpler
`examples/stacks/api-database` service only checks that `DATABASE_URL` is
present.

The commands below create billable AWS resources. Use an isolated environment
and record the operation IDs before making changes.

## Prerequisites

- A Skiff AWS environment has been bootstrapped.
- The caller can push an OCI image to a registry the workload VMs can pull.
- The caller can deploy with Skiff direct mode against the environment state
  bucket.
- `docker`, `aws`, `go`, `skiff`, `jq`, and `curl` are available.

Set the run variables from the repository root:

```bash
export SKIFF_ENV=prod
export AWS_REGION=us-west-2
export SKIFF_STATE=s3://skiff-state-prod
export TRACE_ID=tr_orders_api_db_postgres_ha
export LOCKFILE=$PWD/skiff.lock.json
export PKG_CACHE=$PWD/.skiff/packages/cache
export SPEC=/tmp/orders-postgres-ha.skiff.yaml
```

If this is a disposable environment and you have not bootstrapped it yet, create
the environment first:

```bash
skiff bootstrap aws \
  --env "$SKIFF_ENV" \
  --region "$AWS_REGION" \
  --network managed \
  --ingress private \
  --out bootstrap
```

For browser or laptop `curl` access, bootstrap with `--ingress public` and a
domain/certificate configuration. Otherwise keep the service internal and run
the smoke requests from a host with network access to the internal load
balancer.

## Build The Read/Write API Image

Build and push the orders RPC image. This example uses ECR; if you already have
a digest-pinned image, set `IMAGE_REF` directly and skip this section.

```bash
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export IMAGE_REPOSITORY=orders-rpc
export IMAGE_TAG=orders-$(date +%Y%m%d%H%M%S)
export IMAGE_REPO="$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$IMAGE_REPOSITORY"

aws ecr describe-repositories --repository-names "$IMAGE_REPOSITORY" >/dev/null 2>&1 || \
  aws ecr create-repository --repository-name "$IMAGE_REPOSITORY" >/dev/null

aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"

docker buildx build \
  --platform linux/amd64 \
  -f examples/stacks/api-multiregion-database/Dockerfile \
  -t "$IMAGE_REPO:$IMAGE_TAG" \
  --push \
  .

export IMAGE_DIGEST=$(aws ecr describe-images \
  --repository-name "$IMAGE_REPOSITORY" \
  --image-ids imageTag="$IMAGE_TAG" \
  --query 'imageDetails[0].imageDigest' \
  --output text)
export IMAGE_REF="$IMAGE_REPO@$IMAGE_DIGEST"
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

## Write The Stack Spec

This spec deploys one API service and one managed Postgres dependency exported
by `skiff.dev/postgres-ha`. Skiff injects the database connection secret into
the service as `DATABASE_URL`.

Set `INGRESS_TYPE=public-http` only when the environment has a public ingress
and usable TLS host configuration. Otherwise leave it internal.

```bash
export INGRESS_TYPE=${INGRESS_TYPE:-internal-http}

cat > "$SPEC" <<EOF
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

## Validate And Preview

```bash
skiff validate "$SPEC" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"

skiff explain "$SPEC" \
  --provider aws \
  --region "$AWS_REGION" \
  --state "$SKIFF_STATE" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE"

skiff deploy "$SPEC" \
  --dry-run \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE" \
  --trace-id "$TRACE_ID"
```

The preview should show visible cloud primitives for the API and database,
including an Auto Scaling Group, launch template, target group, listener when
ingress is enabled, RDS or Aurora resources, Secrets Manager connection secret,
security groups, logs, metrics, release objects, operation objects, and audit
records.

## Deploy

```bash
skiff deploy "$SPEC" \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --lockfile "$LOCKFILE" \
  --cache "$PKG_CACHE" \
  --trace-id "$TRACE_ID"
```

Record the operation ID, release ID, and provider resource IDs from the deploy
output. If deploy is interrupted, resume from object state rather than starting
a second mutating operation:

```bash
skiff ops resume <op-id> \
  --service orders-api \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --trace-id "$TRACE_ID"
```

## Verify Service And Database Health

Use direct mode to confirm the service, runner, and provider resources are
healthy:

```bash
skiff status orders-api \
  --fresh \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --trace-id "$TRACE_ID"

skiff doctor orders-api \
  --fresh \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --trace-id "$TRACE_ID"
```

Set the API URL from the deploy/status output or from the load balancer/host
created for the service:

```bash
export API_URL=https://orders-api.example.com
```

For an internal load balancer, run these requests from inside the VPC or through
an approved network path.

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

## Demonstrate Postgres HA Package Operations

The read/write deployment above uses `skiff.dev/postgres-ha` in managed mode for
the database dependency. To demo the package's self-managed stateful operation
profile on Apple Silicon, use the dedicated package gate:
[`postgres-ha-apple-silicon.md`](postgres-ha-apple-silicon.md).

That runbook installs the same package from `packages/postgres-ha`, builds and
executes `cmd/postgres-ha-plugin`, runs `primary-switchover-update` against a
live three-member Apple StatefulGroup, verifies direct-mode and local `skiffd`
surfaces, and proves the unsafe replica-lag scenario blocks before member
mutation. It does not start a standalone Postgres container as a substitute for
the package.

## Failure Triage

If readiness or read/write verification fails, collect state before mutating:

```bash
skiff logs orders-api \
  --since 20m \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --trace-id "$TRACE_ID"

skiff ops events orders-api \
  --direct \
  --state "$SKIFF_STATE" \
  --trace-id "$TRACE_ID"
```

Common checks:

- `DATABASE_URL` should be a secret reference in the runtime manifest, not a
  plaintext password in object state.
- The service security group must be allowed to reach the database security
  group on port 5432.
- The API target group should use `/readyz`, not `/healthz`, so traffic is sent
  only after the database is reachable.
- The image ref should be digest-pinned and pullable by the runner IAM role.

## Roll Back The API Release

If the application release is bad and a previous stable release exists:

```bash
skiff rollback orders-api \
  --to previous-stable \
  --direct \
  --state "$SKIFF_STATE" \
  --env "$SKIFF_ENV" \
  --provider aws \
  --region "$AWS_REGION" \
  --approval-id <approval-id> \
  --trace-id "$TRACE_ID"
```

This rolls back the API release. It does not rewind database writes. Use an
explicit database restore operation when data must move.

## Evidence To Save

- stack spec path and image digest
- `skiff.lock.json` package entry for `postgres-ha`
- deploy operation ID and release ID
- RDS/Aurora provider IDs and secret reference
- `curl /readyz` output
- `orders.add` response
- `orders.list` response showing the inserted order
- `curl /metrics` output showing the order count
