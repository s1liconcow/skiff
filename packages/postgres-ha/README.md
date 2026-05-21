# postgres-ha

`skiff.dev/postgres-ha` is the first-party HA Postgres package. It is an
installable package directory with a command-runtime plugin named
`postgres-ha-plugin`.

Install from a local checkout:

```bash
go install ./cmd/postgres-ha-plugin
skiff pkg add skiff.dev/postgres-ha --registry-dir packages --lockfile skiff.lock.json
skiff pkg verify postgres-ha --conformance --lockfile skiff.lock.json
```

Managed mode compiles to a Skiff `ManagedDatabase` with provider-native HA,
backup, restore, credential rotation, and topology inspection operations exposed
in package metadata:

```yaml
stack:
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      config:
        mode: managed
        engine: postgres
        version: "16"
        size: small
        maxReplicaLagBytes: 1048576
        synchronous: true
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

Self-managed mode compiles to a one-VM-per-member `StatefulGroup` and uses the
package plugin for planned primary switchover safety checks:

```yaml
stack:
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      config:
        mode: self-managed
        version: "16"
        replicas: 3
        maxReplicaLagBytes: 0
        volume:
          size: 100Gi
          mountPath: /data
        artifact:
          type: oci
          ref: localhost/postgres-ha:apple
        runtime:
          ports:
            admin: 8008
            postgres: 5432
          health:
            path: /healthz
            port: 8008
```

The self-managed plugin talks to member admin endpoints exposed by the
first-party Apple demo image in `examples/stateful/postgres-ha`:

- `GET /admin/state`
- `POST /admin/promote`
- `POST /admin/stepdown`
- `POST /admin/catch-up`

Build the local Apple image with:

```bash
make demo-apple-postgres-ha-images
```

By default the plugin uses `http://{target}-{member}:8008`. For local Apple
Silicon validation where members are published on loopback ports, pass the
member URL map as an operation parameter:

```bash
skiff ops run payments-db primary-switchover-update \
  --param release_id=rel_20260520 \
  --param candidate=1 \
  --param member_admin_urls='{"0":"http://127.0.0.1:20000","1":"http://127.0.0.1:20100","2":"http://127.0.0.1:20200"}' \
  --param return_primary=true \
  --yes
```

Planned switchover fails before mutation when the candidate is not a replica,
has failures, or exceeds `maxReplicaLagBytes`.
