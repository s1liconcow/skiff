# Single-Member StatefulGroup Example

This example is a deliberately small StatefulGroup for recovery tutorials. It
uses one NATS JetStream member, one encrypted durable volume, one stable DNS
name, and the same digest-pinned NATS OCI image as the clustered JetStream
example.

Validate and inspect the local fake-provider journey:

```bash
skiff validate examples/stateful/single-member/skiff.yaml --format json
skiff stateful plan examples/stateful/single-member/skiff.yaml --provider fake --region local --format json
skiff stateful apply examples/stateful/single-member/skiff.yaml --direct --state file://$PWD/.skiff-state --provider fake --region local --format json
skiff stateful status ledger-stream --direct --state file://$PWD/.skiff-state --provider fake --region local --format json
```

Use managed state for ordinary relational databases, queues, and caches. Choose
this shape only when the workload process owns durable local state and the
operator needs explicit member identity, fencing, backup, restore, and
replacement sagas.
