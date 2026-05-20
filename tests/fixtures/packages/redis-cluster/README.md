# redis-cluster

`skiff.dev/redis-cluster` models slot-aware Redis Cluster operations. Normal
updates require complete slot coverage and eligible replicas; `TAKEOVER` style
break-glass paths remain separate from the normal profile.

```bash
skiff pkg add file://tests/fixtures/packages/redis-cluster
skiff ops plan cache-cluster slot-aware-failover-update --param release_id=rel_20260520 --param slot_selector={}
skiff stateful inspect cache-cluster
```
