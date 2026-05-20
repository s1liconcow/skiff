# nats-jetstream

`skiff.dev/nats-jetstream` models a self-managed JetStream cluster with stable
member identity, persistent JetStream storage, route/client/monitor ports, and
RAFT-aware operations.

```bash
skiff pkg add file://tests/fixtures/packages/nats-jetstream
skiff ops plan orders-stream raft-group-rolling-update --param release_id=rel_20260520 --param group_selector={}
skiff stateful inspect orders-stream
```
