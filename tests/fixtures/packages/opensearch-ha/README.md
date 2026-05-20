# opensearch-ha

`skiff.dev/opensearch-ha` models shard-aware OpenSearch operations with stable
node identity, persistent volumes, explicit allocation toggles, flush/recovery
steps, and red-health blocking by default.

```bash
skiff pkg add file://tests/fixtures/packages/opensearch-ha
skiff ops plan search shard-allocation-rolling-update --param release_id=rel_20260520 --param shard_selector={}
skiff stateful inspect search
```
