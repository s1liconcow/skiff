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
        runtime:
          command: ["/usr/local/bin/postgres-ha"]
          ports:
            postgres: 5432
            health: 8008
          health:
            path: /healthz
            port: 8008
```

The self-managed plugin talks to member admin endpoints compatible with
`skiff-opsem primary-replica`:

- `GET /admin/state`
- `POST /admin/promote`
- `POST /admin/stepdown`
- `POST /admin/catch-up`

By default the plugin uses `http://{target}-{member}:8008`. For local or Apple
Silicon validation, provide explicit endpoints:

```bash
export SKIFF_POSTGRES_HA_MEMBER_ADMIN_URLS='{"0":"http://127.0.0.1:18080","1":"http://127.0.0.1:18180","2":"http://127.0.0.1:18280"}'
skiff ops run payments-db primary-switchover-update --param release_id=rel_20260520 --param candidate=1 --param return_primary=true --yes
```

Planned switchover fails before mutation when the candidate is not a replica,
has failures, or exceeds `maxReplicaLagBytes`.
