# Stateful JetStream Example

This example models a production NATS JetStream cluster for order and payment
events. It is deliberately a `StatefulGroup`, not a managed database recipe:
the service process owns durable local state, so Skiff makes the identity,
volumes, fencing, recovery saga, and ordered updates explicit.

Use it to inspect the stateful spec surface:

```bash
skiff validate examples/stateful/jetstream/skiff.yaml
```

The important pieces are:

- three named members pinned to distinct zones with stable DNS names
- one encrypted durable volume mounted at `/var/lib/nats` per member
- a recipe config that describes ports, health, metrics, streams, snapshots,
  and member replacement behavior
- `requireFencing: true`, because replacing a stateful member must fence the
  old instance before reattaching its volume
- `update.strategy: ordered`, so stateful rollout is explicit and serial

Managed services should still be the default for ordinary databases. This
example is for workloads where the service itself is the stateful system.
