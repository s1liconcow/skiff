# redis-ha

`skiff.dev/redis-ha` models Sentinel-style Redis HA with explicit planned
failover/update operations and separate high-risk emergency modes.

```bash
skiff pkg add file://tests/fixtures/packages/redis-ha
skiff ops plan cache primary-switchover-update --param release_id=rel_20260520 --param candidate=cache-1
skiff stateful inspect cache
```
