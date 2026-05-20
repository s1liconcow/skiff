# mysql-ha

`skiff.dev/mysql-ha` supports managed MySQL and self-managed InnoDB
Cluster-style single-primary operation. The package exports a MySQL Router
application binding and keeps emergency failover separate from planned primary
changes.

```bash
skiff pkg add file://tests/fixtures/packages/mysql-ha
skiff ops plan orders-mysql primary-switchover-update --param release_id=rel_20260520 --param candidate=orders-mysql-1
skiff stateful inspect orders-mysql
```
