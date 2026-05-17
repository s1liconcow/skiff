# Quickstart

This quickstart deploys the `http-hello` service with local object state and the
fake provider. It shows the normal operator loop without requiring AWS
credentials.

## Start From A Spec

```bash
skiff validate examples/service/http-hello/skiff.yaml --format json
```

Expected result: `ok: true`, `result.name: "http-hello"`, and no diagnostics.

The spec defines one service, one OCI artifact pinned by digest, private ingress,
logs, metrics, and two replicas. Skiff will compile it into provider-visible
primitives: IAM role/profile, security groups, launch template, Auto Scaling
Group, target group, log group, metrics, release manifest, runtime manifest, and
service control state.

## Plan Cloud Primitives

```bash
skiff plan examples/service/http-hello/skiff.yaml \
  --provider aws \
  --region us-west-2 \
  --state file://./.skiff-demo-state \
  --format json
```

Expected result: `ok: true` with a `plan.resources[]` list. Planning is
non-mutating. It explains cloud primitives but does not write object state.

## Deploy With Local Object State

```bash
SEED="$(printf '%032d' 0 | tr '0' D | base64 | tr -d '\n')"

skiff deploy examples/service/http-hello/skiff.yaml \
  --canary \
  --canary-stages 100 \
  --canary-bake 0s \
  --direct \
  --state file://./.skiff-demo-state \
  --env prod \
  --provider fake \
  --region local \
  --release-id rel_demo_01 \
  --operation-id op_demo_01 \
  --key-id demo \
  --signing-seed-base64 "$SEED" \
  --format json
```

Expected result: `ok: true`, a saga ID, operation ID, and `status: "succeeded"`.

State written before memory/provider effects:

- `services/http-hello/releases/rel_demo_01/release.json`
- `services/http-hello/releases/rel_demo_01/runtime-manifest.json`
- `services/http-hello/operations/op_demo_01/intent.json`
- `services/http-hello/operations/op_demo_01/control.json`
- `sagas/<saga>/intent.json`, `graph.json`, `control.json`, and events
- `audit/<yyyy-mm-dd>/<id>.json`

The operation is reversible while the previous stable release still exists. The
canary saga is compensatable; compensation is not the same as rollback unless it
restores the prior service state.

## Inspect Status, Logs, And Doctor

```bash
skiff status http-hello --direct --state file://./.skiff-demo-state --env prod --provider fake --region local --format json
skiff logs http-hello --direct --state file://./.skiff-demo-state --env prod --provider fake --region local --format json
skiff doctor http-hello --direct --state file://./.skiff-demo-state --env prod --provider fake --region local --format json
```

Expected result: status shows `http-hello`, logs include fake workload messages,
and doctor returns no critical findings for the healthy fake-provider path.

If diagnosis fails, recover through direct mode first:

```bash
skiff events --scope service --service http-hello --direct --state file://./.skiff-demo-state --format json
skiff ops list http-hello --direct --state file://./.skiff-demo-state --format json
```

## Roll Back

After a second release, roll back to the previous stable release:

```bash
skiff rollback http-hello \
  --to rel_demo_01 \
  --operation-id op_demo_rollback \
  --saga-id saga_demo_rollback \
  --direct \
  --state file://./.skiff-demo-state \
  --env prod \
  --provider fake \
  --region local \
  --format json
```

Expected result: rollback writes a new operation intent, updates desired release
through CAS, records saga/audit events, and returns `ok: true`.

## Golden Demos

Run the local quickstart script:

```bash
bash demos/quickstart-fake.sh
```

Generate CI templates and contract evidence:

```bash
bash demos/cicd-templates.sh
```
