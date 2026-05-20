# Package Operations

Package operations are explicit, typed operational graphs. They are not hidden
controllers and they do not reconcile forever in the background.

A package can export operation profiles such as:

- `primary-switchover-update`
- `raft-group-rolling-update`
- `partition-quorum-rolling-update`
- `slot-aware-failover-update`
- `shard-allocation-rolling-update`

Each profile renders a saga graph with typed package steps. Skiff writes the
operation intent/control documents first, then writes saga intent, graph,
control, events, and audit records. Every provider operation ID and package step
result is durable before Skiff waits on it.

## Normal Operator Path

Use `skiff ops` first:

```bash
skiff ops plan payments-db primary-switchover-update --param release_id=rel_01J... --param candidate=payments-db-1 --format json
skiff ops run payments-db primary-switchover-update --param release_id=rel_01J... --param candidate=payments-db-1 --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff ops inspect op_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff ops resume op_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

`skiff ops` is the normal operation surface because it preserves the operator's
intent, risk, target, trace ID, and package provenance alongside the saga graph.

## Advanced Recovery Path

Use `skiff saga` when you need to inspect or recover the graph directly:

```bash
skiff saga inspect saga_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff saga resume saga_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff saga approve saga_01J... --step approve-switchover --reason "approved maintenance window" --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Do not start normal package work through `skiff saga start`. Start with
`skiff ops run` so the operation document remains the durable user-facing
record.

## Risk Boundaries

Package steps must declare risk and reversibility. Verification steps are
usually low-risk and reversible. Topology-changing steps, failovers, volume
movement, restore, and primary election are medium or high risk and normally
need approval in production.

Compensation is not automatically rollback. A compensation step can reduce harm
or restore a previous topology only when the package can prove that behavior.
