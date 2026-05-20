# elasticsearch-ha

`skiff.dev/elasticsearch-ha` shares the search shard-safety contract with
OpenSearch while remaining a separate package. It exposes explicit allocation,
flush, restart, recovery, and health gates.

```bash
skiff pkg add file://tests/fixtures/packages/elasticsearch-ha
skiff ops plan es shard-allocation-rolling-update --param release_id=rel_20260520 --param shard_selector={}
skiff stateful inspect es
```
