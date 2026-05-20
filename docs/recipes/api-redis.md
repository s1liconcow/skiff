# API plus Redis

Use `skiff.dev/redis-ha` for Sentinel-style primary/replica Redis. Use
`skiff.dev/redis-cluster` when slot ownership and cluster resharding matter.

## Redis HA

```bash
skiff pkg add skiff.dev/redis-ha --lockfile skiff.lock.json
```

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: storefront
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/storefront-api@sha256:...
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: cache
      uses: skiff.dev/redis-ha
      version: "1.0.0"
      config:
        mode: stateful
        replicas: 3
        volume:
          size: 20Gi
        artifact:
          type: oci
          ref: registry.example.com/redis-ha@sha256:...
        runtime:
          command: ["/usr/local/bin/redis-ha"]
          ports:
            redis: 6379
            sentinel: 26379
            health: 8080
          health:
            path: /healthz
            port: 8080
  bindings:
    - from: api
      to: cache
      as: REDIS_URL
```

```bash
skiff ops run cache primary-switchover-update --param release_id=rel_01J... --param candidate=cache-1 --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

## Redis Cluster

```bash
skiff pkg add skiff.dev/redis-cluster --lockfile skiff.lock.json
```

Change the dependency to:

```yaml
    - name: cache
      uses: skiff.dev/redis-cluster
      version: "1.0.0"
      config:
        mode: stateful
        replicas: 6
        volume:
          size: 50Gi
```

Run slot-aware operations through `skiff ops`:

```bash
skiff ops run cache-cluster slot-aware-failover-update --param release_id=rel_01J... --param slot_selector={} --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
