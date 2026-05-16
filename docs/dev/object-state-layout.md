# Object-State Layout

Skiff durable state lives in object storage. Object keys are part of the operator interface, so business logic must use helpers from `internal/state/paths` instead of constructing keys inline.

## Core Paths

```text
services/<service>/control.json
services/<service>/releases/<release>/release.json
services/<service>/releases/<release>/runtime-manifest.json
services/<service>/operations/<operation>/intent.json
services/<service>/operations/<operation>/control.json
services/<service>/operations/<operation>/events/<event>.json

sagas/<saga>/intent.json
sagas/<saga>/graph.json
sagas/<saga>/control.json
sagas/<saga>/events/<event>.json

resources/by-logical/<kind>/<name>.json
resources/by-provider/<provider>/<kind>/<provider-id>.json

observations/services/<service>/<observation>.json

indexes/services.json
indexes/active-sagas.json
indexes/recent-events.json

audit/<yyyy-mm-dd>/<event>.json
```

Provider IDs are path-escaped by the helper because cloud IDs can contain separators. Service names, environment names, logical resource kinds, and logical resource names are strict lowercase slugs: lowercase letters, digits, and hyphens, starting and ending with a letter or digit. IDs for releases, operations, sagas, and events may contain uppercase letters, digits, hyphens, underscores, dots, colons, `@`, and `+`, but never path separators or whitespace.

## Schemas

Durable structs live in `internal/state/schema`. Each durable object includes:

```json
{
  "schema_version": "skiff.state/v1"
}
```

Control documents include embedded lease fields when they need locking. Skiff does not create separate lock objects. Immutable history objects such as release manifests, operation intents, saga graphs, events, and audit records are create-only.

Service control documents are the service-level CAS object and lease object:

```json
{
  "schema_version": "skiff.state/v1",
  "service": "payments-api",
  "env": "prod",
  "desired_release": "rel_01JNEW",
  "stable_release": "rel_01JOLD",
  "operation": {
    "id": "op_01JDEPLOY",
    "kind": "deploy",
    "state": "rolling_out",
    "step": "canary"
  },
  "lease": {
    "owner": "skiffd/instance-a",
    "token": "lease_01J...",
    "generation": 42,
    "expires_at": "2026-05-16T20:05:00Z"
  },
  "version": 18,
  "updated_at": "2026-05-16T20:04:30Z",
  "updated_by": {
    "id": "alpha-one",
    "type": "agent"
  },
  "trace_id": "tr_01J..."
}
```

## Canonical JSON

Use `internal/state/canonical.Marshal` for durable JSON bodies. It emits a single-line JSON document with HTML escaping disabled and stable struct/map ordering from Go's JSON encoder. Use `canonical.UnmarshalStrict` when reading schema documents so unknown fields are findings instead of silently accepted state.

Timestamps in schema structs are stored as UTC RFC3339 strings. Use `canonical.Time(t)` to remove monotonic clock data and format in UTC.

## Signed Objects

Signed immutable objects carry a top-level `digest` and `signatures` array. The digest is calculated from the canonical unsigned object with `digest` and `signatures` excluded, then signatures are verified against that digest. Release verification also checks schema version, service/env target, production artifact digest pinning, runtime manifest digest, and expiry.

Developer verification commands:

```bash
skiff release verify release.json --runtime-manifest runtime-manifest.json --public-key local-test=<base64-ed25519-public-key>
skiff object verify release.json --public-key local-test=<base64-ed25519-public-key>
```

## Developer Command

Use `skiff state path` to print the expected key:

```bash
skiff state path service --service payments-api
skiff state path release --service payments-api --release rel_01JABC
skiff state path release --service payments-api --release rel_01JABC --doc runtime-manifest
skiff state path operation --service payments-api --operation op_01JABC --doc event --event 01JABCDEF
skiff state path saga --saga saga_01JABC --doc graph
skiff state path resource-provider --provider aws --resource-kind target-group --id 'arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/payments/abc'
skiff state path audit --day 2026-05-16 --event 01JABCDEF
```

For agent use:

```bash
skiff state path service --service payments-api --format json --trace-id tr_01JABC
```
