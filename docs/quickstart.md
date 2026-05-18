# Quickstart

This quickstart deploys the `http-hello` service with local object state and the
fake provider. It shows the normal operator loop without requiring AWS
credentials. On Apple silicon, it also shows how to run the local VM-backed
RustFS/Caddy path through Apple Container.

## Run The Scripted Demo

The fastest path is the scripted local demo:

```bash
make demo-local
```

It builds `bin/skiff` if needed, writes a local `.skiffconfig`, validates and
plans the example service, publishes signed release objects, runs a rolling
canary saga, prints human-readable status/logs/metrics/doctor/events/ops
output, and renders a read-only TUI frame.
The fake-provider path writes real object state but does not create cloud
resources or run a workload process, so diagnostic health is synthetic.

To run the same flow as a smoke test with isolated temporary state:

```bash
make demo-test
```

On Apple silicon, you can also launch the optional RustFS/Caddy workload demo:

```bash
make demo-apple-context
```

That path pulls OCI images, starts local Linux VMs through Apple Container, and
writes a filled-in `.skiffconfig` plus a sourceable `.env` file for the run. It
leaves RustFS, Caddy, and `skiffd` running so you can inspect the live local
state and open the TUI after the command exits. Use the fake-provider demo first
if you only want to learn the operator loop.

## Create A Local Skiff Config

Skiff config files can carry multiple named contexts, similar to kubeconfig.
The quickstart uses one direct fake-provider context and one AWS planning
context over the same local object-state directory:

```bash
STATE_URI="file://$PWD/.skiff-demo-state"

cat > .skiffconfig <<EOF
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: local-fake
contexts:
  - name: local-fake
    context:
      mode: direct
      env: prod
      provider: fake
      region: local
      state: "$STATE_URI"
  - name: local-aws-plan
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: "$STATE_URI"
EOF

export SKIFF_CONFIG="$PWD/.skiffconfig"
unset SKIFF_CONTEXT

skiff config get-contexts
skiff config use-context local-fake
skiff config show
```

Select a context for one command without changing the file:

```bash
SKIFF_CONTEXT=local-aws-plan skiff config show
```

## Run Actual Local VMs On Apple Silicon

The fake-provider context writes real Skiff state but does not start a workload
process. To exercise the VM-local runner/workload path on an Apple silicon Mac,
use the Apple Container demo:

```bash
container --version
make demo-apple-context
```

Prerequisites:

- Apple silicon Mac
- Apple Container installed and running, with the `container` CLI on `PATH`
- Network access to pull the RustFS and Caddy OCI images

The demo starts RustFS as an S3-compatible object store and Caddy as the
workload in local Linux VMs. It publishes signed release/runtime manifests,
boots the runner lifecycle, rolls Caddy to a second release, starts a local
`skiffd` against the same object state, and runs a rolling canary saga.

The helper creates temporary ports and bucket names, writes the resolved values
under `.skiff-demo-reports/apple-container/`, and uses those contexts for its
Skiff CLI checks. At the end of the run it prints the exact files:

```bash
source .skiff-demo-reports/apple-container/<run>.env
skiff config get-contexts
SKIFF_CONTEXT=local-apple-vms skiff status caddy-web
SKIFF_CONTEXT=local-apple-skiffd skiff tui caddy-web --read-only
make demo-apple-down
```

The generated config has this shape, with the RustFS bucket and `skiffd` port
already filled in:

```yaml
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: local-apple-vms
contexts:
  - name: local-apple-vms
    context:
      mode: direct
      env: prod
      provider: apple-container
      region: local
      state: "s3://<rustfs-bucket>"
  - name: local-apple-skiffd-server
    context:
      mode: skiffd
      env: prod
      provider: apple-container
      region: local
      state: "s3://<rustfs-bucket>"
  - name: local-apple-skiffd
    context:
      mode: api
      env: prod
      provider: apple-container
      region: local
      state: "s3://<rustfs-bucket>"
      apiURL: "http://127.0.0.1:<skiffd-port>"
```

The generated `.env` file contains the RustFS endpoint details for the same
run:

```bash
export AWS_ACCESS_KEY_ID=skiffe2eaccess
export AWS_SECRET_ACCESS_KEY=skiffe2esecret
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION=us-east-1
export SKIFF_AWS_ENDPOINT="http://127.0.0.1:<rustfs-port>"
export SKIFF_AWS_S3_PATH_STYLE=true
export SKIFF_CONFIG=".skiff-demo-reports/apple-container/<run>.skiffconfig"
export SKIFF_CONTEXT=local-apple-vms
export SKIFF_APPLE_RUSTFS_CONTAINER="<rustfs-container>"
export SKIFF_APPLE_RUSTFS_VOLUME="<rustfs-volume>"
export SKIFF_APPLE_CADDY_CONTAINER="<caddy-container>"
export SKIFF_APPLE_SKIFFD_URL="http://127.0.0.1:<skiffd-port>"
export SKIFF_APPLE_SKIFFD_PID="<skiffd-pid>"
export SKIFF_APPLE_SKIFFD_LOG=".skiff-demo-reports/apple-container/<run>-skiffd.log"
```

With that environment sourced, `skiff logs caddy-web` reads the recorded Caddy
Apple Container logs instead of the fake-provider placeholder.

`make demo-apple-context` is the persistent path. Use `make demo-apple-down` to
stop the `skiffd` process, Caddy VM, RustFS VM, and RustFS volume recorded in
the latest generated `.env` file. Use `make clean-apple-containers` to remove
all Skiff-named Apple Container demo/e2e containers and RustFS volumes. If you
want a cleanup-safe smoke run instead, use `make demo-apple-container`; it stops
the Apple containers and in-process `skiffd` when the command exits.

## Start From A Spec

```bash
skiff validate examples/service/http-hello/skiff.yaml
```

Expected result: `Service prod/http-hello valid`.

The spec defines one service, one OCI artifact pinned by digest, private ingress,
logs, metrics, and two replicas. Skiff will compile it into provider-visible
primitives: IAM role/profile, security groups, launch template, Auto Scaling
Group, target group, log group, metrics, release manifest, runtime manifest, and
service control state.

Stateful examples are available for deliberate self-managed state:

```bash
skiff validate examples/stateful/jetstream/skiff.yaml --format json
skiff validate examples/stateful/single-member/skiff.yaml --format json
skiff stateful plan examples/stateful/jetstream/skiff.yaml --provider fake --region local --format json
```

Read [Stateful Group](recipes/stateful-group.md) before using these examples in
production. Managed state remains the default recommendation for ordinary
databases, queues, and caches.

## Plan Cloud Primitives

```bash
SKIFF_CONTEXT=local-aws-plan skiff plan examples/service/http-hello/skiff.yaml
```

Expected result: a human-readable AWS resource plan. Planning is non-mutating.
It explains cloud primitives but does not write object state.

## Deploy With Local Object State

```bash
skiff deploy examples/service/http-hello/skiff.yaml
```

Expected result: `deploy op_... succeeded` and `release: rel_...`.
Skiff constructs release and operation IDs when you do not provide them. The
fake provider uses an ephemeral local signing key when no signing seed is
provided. Real providers still require an explicit signing key source so runners
can verify releases against trusted public keys.

State written before memory/provider effects:

- `services/http-hello/releases/<release>/release.json`
- `services/http-hello/releases/<release>/runtime-manifest.json`
- `services/http-hello/operations/<operation>/intent.json`
- `services/http-hello/operations/<operation>/control.json`
- `audit/<yyyy-mm-dd>/<id>.json`

## Run A Rolling Canary

After the base deploy has written provider resource records, run a staged canary
saga:

```bash
skiff deploy examples/service/http-hello/skiff.yaml \
  --canary \
  --canary-bake 0s \
  --canary-metric request_count \
  --canary-threshold 1
```

Expected result: a `canary saga ... status=succeeded` line plus generated
`release: rel_...` and `operation: op_...` lines. The canary saga is
compensatable; compensation is not the same as
rollback unless it restores the prior service state.

## Inspect Status, Logs, And Doctor

```bash
skiff status http-hello
skiff logs http-hello
skiff doctor http-hello
```

Expected result: status shows `http-hello`, logs include fake workload messages,
and doctor returns structured findings for the synthetic fake-provider path.

If diagnosis fails, recover through direct mode first:

```bash
skiff events --scope service --service http-hello
skiff ops list --all --service http-hello
```

## Open The TUI

Render one frame:

```bash
skiff tui http-hello --once --read-only
```

Open the interactive TUI:

```bash
skiff tui http-hello --read-only
```

## Roll Back

After a second release, roll back to the previous stable release:

```bash
skiff rollback http-hello \
  --to <base-release>
```

Use the `release: rel_...` value printed by the first deploy as
`<base-release>`. Skiff generates the rollback operation and saga IDs unless you
override them.

Expected result: rollback writes a new operation intent, updates desired release
through CAS, records saga/audit events, and reports success.

## Golden Demos

Run the local fake-provider demo:

```bash
demos/quickstart-fake.sh --reset
```

Run its smoke test:

```bash
demos/test-local-demo.sh
```

Launch the Apple Container/Caddy workload path when available:

```bash
make demo-apple-context
```
