# kafka

`skiff.dev/kafka` models a self-managed KRaft Kafka cluster with stable broker
identity, persistent volumes, replication factor 3, `min.insync.replicas` 2, and
unclean leader election disabled by default.

```bash
skiff pkg add file://tests/fixtures/packages/kafka
skiff ops plan orders-kafka partition-quorum-rolling-update --param release_id=rel_20260520 --param partition_selector={}
skiff stateful inspect orders-kafka
```
