# API plus OpenSearch

Use the OpenSearch package when shard allocation and quorum-aware updates need
to be visible operational steps.

```bash
skiff pkg add skiff.dev/opensearch-ha --lockfile skiff.lock.json
```

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: search
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/search-api@sha256:...
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: search
      uses: skiff.dev/opensearch-ha
      version: "1.0.0"
      config:
        mode: stateful
        replicas: 3
        volume:
          size: 500Gi
        artifact:
          type: oci
          ref: registry.example.com/opensearch@sha256:...
        runtime:
          command: ["/usr/local/bin/opensearch-node"]
          ports:
            http: 9200
            transport: 9300
            health: 9600
          health:
            path: /healthz
            port: 9600
  bindings:
    - from: api
      to: search
      as: OPENSEARCH_URL
```

```bash
skiff validate skiff.yaml
skiff pkg verify opensearch-ha --lockfile skiff.lock.json --conformance --format json
skiff ops run search shard-allocation-rolling-update --param release_id=rel_01J... --param shard_selector={} --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
