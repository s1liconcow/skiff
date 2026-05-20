# postgres-ha

`skiff.dev/postgres-ha` is the first-party HA Postgres package fixture. It
supports a provider-managed mode for AWS RDS/Aurora-style databases and a
self-managed mode for one-VM-per-member Postgres clusters with Patroni-style
primary/replica semantics.

Configuration fields:

- `mode`: `managed` or `self-managed`
- `version`: Postgres major version
- `size`: provider or VM sizing hint
- `storage`: encrypted storage settings
- `backups`: backup retention and window
- `replicas`: self-managed member count
- `maxReplicaLagBytes`: planned switchover lag budget
- `synchronous`: whether provider-native synchronous replication is required

Managed mode uses provider-native HA, failover, backup, restore, credential
rotation, and topology inspection APIs. Self-managed mode exports
`primary-switchover-update` plus typed Postgres package steps that distinguish a
planned switchover from an emergency failover.

```bash
skiff pkg add file://tests/fixtures/packages/postgres-ha
skiff ops plan payments-db primary-switchover-update --param release_id=rel_20260520 --param candidate=payments-db-1
skiff stateful inspect payments-db
skiff doctor payments-api
skiff status payments-api
```
