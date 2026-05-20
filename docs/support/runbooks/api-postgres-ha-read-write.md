# Runbook: API plus Postgres HA Read/Write And Stateful Operation

Use this runbook to deploy the orders API example, provision Postgres through
the installable `skiff.dev/postgres-ha` package, verify database reads/writes,
and run the package-exposed `primary-switchover-update` stateful operation.

The happy path is convention-first:

- Skiff configuration comes from one selected context.
- Package lock/cache paths use the defaults: `skiff.lock.json` and
  `.skiff/packages/cache`.
- The stack spec is `orders-api-postgres-ha.skiff.yaml`.
- The environment is `prod`.
- The package-created StatefulGroup is `orders-db`.
- The API `DATABASE_URL` points at the `orders-db` package secret reference.

The API and the operation use the same `postgres-ha` package dependency:

- the stack declares `db` as `skiff.dev/postgres-ha` in `mode: self-managed`
- Skiff compiles that dependency into the `orders-db` StatefulGroup
- the API receives the package-exported connection URL as `DATABASE_URL`
- `primary-switchover-update` targets the same `orders-db` StatefulGroup

Do not pair the API with a managed database while running the operation against
a separate StatefulGroup. That proves the operation path, but it does not prove
the API is using the database that the package operates.

The read/write service is
[`examples/stacks/api-multiregion-database`](../../../examples/stacks/api-multiregion-database)
because it exposes JSON-RPC methods that actually touch Postgres.

## Prerequisites

- `go`, `skiff`, `jq`, and `curl` are available.
- `IMAGE_REF` points at an orders API OCI image the selected provider can pull.
- `POSTGRES_HA_IMAGE_REF` points at a real self-managed `postgres-ha` OCI image
  that exposes the package admin API on port `8008`.
- `secret://stateful/orders-db/connection-url` resolves to the connection URL
  exported by the `orders-db` `postgres-ha` package instance. Do not put
  plaintext database credentials in the stack spec, generated manifests, logs,
  or shell history.
- For AWS, bootstrap the environment first and use the state bucket and region
  from that context.
- For Apple Silicon, Apple Container must be installed and object state must
  point at an S3-compatible endpoint such as local RustFS.

## Configure Skiff

Create a runbook-local config once. Edit the AWS `region` and `state` values if
bootstrap chose different names.

```bash
cat > .skiff-postgres-ha.config <<'EOF'
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: postgres-ha-aws
contexts:
  - name: postgres-ha-aws
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: s3://skiff-state-prod
  - name: postgres-ha-apple
    context:
      mode: direct
      env: prod
      provider: apple-container
      region: local
      state: s3://skiff-apple-postgres-ha
EOF

export SKIFF_CONFIG=$PWD/.skiff-postgres-ha.config
```

Choose one provider profile.

AWS:

```bash
export SKIFF_CONTEXT=postgres-ha-aws
export IMAGE_REF=123456789012.dkr.ecr.us-west-2.amazonaws.com/orders-rpc@sha256:...
export POSTGRES_HA_IMAGE_REF=123456789012.dkr.ecr.us-west-2.amazonaws.com/postgres-ha@sha256:...
export API_URL=https://orders-api.example.com
```

Apple Silicon:

```bash
export SKIFF_CONTEXT=postgres-ha-apple
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION=us-east-1
export AWS_ACCESS_KEY_ID=skiffdev
export AWS_SECRET_ACCESS_KEY=skiffdevsecret
export SKIFF_AWS_ENDPOINT=http://127.0.0.1:9000
export SKIFF_AWS_S3_PATH_STYLE=true
export IMAGE_REF=localhost/orders-rpc:apple
export POSTGRES_HA_IMAGE_REF=localhost/postgres-ha:apple
export API_URL=http://127.0.0.1:8080
```

Confirm Skiff sees the intended context:

```bash
skiff config show
```

## Install The Postgres HA Package

Install the package plugin and lock the package from this checkout:

```bash
go install ./cmd/postgres-ha-plugin
skiff pkg add skiff.dev/postgres-ha --registry-dir packages
skiff pkg verify postgres-ha --conformance
```

If the package is already locked, refresh it instead:

```bash
skiff pkg update postgres-ha --registry-dir packages
```

## Write The Stack Spec

This single stack spec deploys the orders API and declares the self-managed
`postgres-ha` package dependency. Skiff injects the same package-exported
connection URL into the service as `DATABASE_URL`; the package operation later
targets that same compiled `orders-db` StatefulGroup.

```bash
cat > orders-api-postgres-ha.skiff.yaml <<EOF
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: prod
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
          type: internal-http
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: self-managed
        endpoint: secret://stateful/orders-db/connection-url
        replicas: 3
        maxReplicaLagBytes: 65536
        volume:
          size: 100Gi
          mountPath: /data
          encrypted: true
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
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
EOF
```

## Validate And Preview

```bash
skiff validate orders-api-postgres-ha.skiff.yaml
skiff plan orders-api-postgres-ha.skiff.yaml
skiff ops list orders-db
```

The plan should show visible provider primitives for the API and the
self-managed Postgres HA StatefulGroup generated from the package dependency.
The operation list should include `primary-switchover-update` with
`postgres-ha` package provenance.

## Deploy

```bash
skiff deploy orders-api-postgres-ha.skiff.yaml
```

Record the deploy operation ID, API release ID, `orders-db` StatefulGroup
resource IDs, and provider resource IDs from the command output. If deployment
is interrupted, resume from object state rather than starting a second mutating
operation.

## Verify Service And Database Health

Use direct mode from the selected context to confirm service and stateful target
health:

```bash
skiff status orders-api
skiff doctor orders-api
skiff stateful status orders-db
```

For an internal load balancer, run the HTTP requests from inside the provider
network or through an approved network path.

```bash
curl -fsS "$API_URL/readyz"
```

The readiness endpoint pings the same `orders-db` Postgres HA package instance
and creates the `orders` table if it is missing.

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

Successful output proves the deployed API has a working `DATABASE_URL` binding
to the self-managed `postgres-ha` package instance, can initialize schema, can
insert a row, can query rows back, and can count rows from Postgres.

## Run The Postgres HA Stateful Operation

Plan and run the package operation against the same `orders-db` package target:

```bash
release_id=rel_orders_db_$(date +%Y%m%d%H%M%S)

skiff ops plan orders-db primary-switchover-update \
  --param release_id="$release_id" \
  --param candidate=member-1

skiff ops run orders-db primary-switchover-update \
  --param release_id="$release_id" \
  --param candidate=member-1
```

Inspect the durable operation and saga objects using the IDs printed by
`skiff ops run`:

```bash
skiff ops inspect <operation-id> --service orders-db
skiff saga inspect <saga-id>
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
skiff logs orders-api --since 20m
skiff ops events orders-api
skiff ops events orders-db
```

Common checks:

- `DATABASE_URL` should be a secret reference in the runtime manifest, not a
  plaintext password in object state.
- The API target group should use `/readyz`, not `/healthz`, so traffic is sent
  only after the database is reachable.
- The API image and `postgres-ha` image should be pullable by the selected
  provider.
- The StatefulGroup in the deploy graph should be `orders-db`, the same target
  used by `skiff ops run`.
- The stateful operation should fail before mutation if the candidate is not
  caught up.

## Roll Back The API Release

If the application release is bad and a previous stable release exists:

```bash
skiff rollback orders-api --approval-id <approval-id>
```

This rolls back the API release. It does not rewind database writes. Use an
explicit database restore operation when data must move.

## Evidence To Save

- selected Skiff context
- API and `postgres-ha` image refs
- single API stack spec with the self-managed `postgres-ha` package dependency
- `skiff.lock.json` package entry for `postgres-ha`
- API deploy operation ID and release ID
- `orders-db` StatefulGroup provider resource IDs
- provider resource IDs
- `curl /readyz` output
- `orders.add` response
- `orders.list` response showing the inserted order
- `curl /metrics` output showing the order count
- operation and saga IDs from `skiff ops run`
- `postgres-ha` package digest from the operation plan
- `primary-switchover-update` plan/run/inspect output
