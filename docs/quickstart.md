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

The fake-provider context writes real Skiff state but does not start a workload.
On an Apple silicon Mac, use the Apple Container path to run a real local Caddy
workload VM, RustFS for S3-compatible object state, and a local `skiffd`.

```bash
SKIFF_INSTALL_VERSION=v0.1.0 SKIFF_INSTALL_DIR="$HOME/.local/bin" scripts/install.sh
export PATH="$HOME/.local/bin:$PATH"
skiff version
skiffd version
```

Prerequisites:

- Apple silicon Mac
- Skiff installed with `scripts/install.sh`, with `skiff` and `skiffd` available on `PATH`
- Apple Container installed and running, with the `container` CLI on `PATH`
- Network access to pull the RustFS and Caddy OCI images

Check Apple Container:

```bash
container --version
```

Start the demo environment:

```bash
make demo-apple-context
```

The command prints the generated `.env` path. Source that exact file:

```bash
source .skiff-demo-reports/apple-container/<run>.env
```

The sourced environment selects the direct Apple Container context and exports
the generated quickstart release specs:

- `$SKIFF_APPLE_GREEN_SPEC`
- `$SKIFF_APPLE_RED_SPEC`
- `$SKIFF_APPLE_BLUE_SPEC`
- `$SKIFF_APPLE_CADDY_URL`

Now walk the system one command at a time.

```bash
skiff config get-contexts
```

```bash
skiff status caddy-web
```

```bash
skiff logs caddy-web --limit 20
```

```bash
skiff ops events caddy-web --limit 20
```

Create a unique suffix for the releases you are about to publish:

```bash
export SKIFF_DEMO_RUN="$(date +%Y%m%d%H%M%S)"
```

Publish and canary the GREEN release. This writes signed release objects,
updates service control through a saga, restarts the local Caddy VM, and passes
`/healthz`.

```bash
skiff deploy "$SKIFF_APPLE_GREEN_SPEC" --canary --canary-stages 50,100 --canary-bake 0s --canary-metric request_count --canary-threshold 1 --release-id "rel_green_$SKIFF_DEMO_RUN" --operation-id "op_green_$SKIFF_DEMO_RUN"
```

```bash
curl "$SKIFF_APPLE_CADDY_URL"
```

Expected result: the page says `GREEN`.

Try the RED release. The RED artifact intentionally omits `/healthz`, so the
canary fails target health and compensates back to the previous stable GREEN
release.

```bash
skiff deploy "$SKIFF_APPLE_RED_SPEC" --canary --canary-stages 50,100 --canary-bake 0s --canary-metric request_count --canary-threshold 1 --release-id "rel_red_$SKIFF_DEMO_RUN" --operation-id "op_red_$SKIFF_DEMO_RUN"
```

Expected result: `canary saga ... status=failed` and `next: inspect_failure`.

```bash
curl "$SKIFF_APPLE_CADDY_URL"
```

Expected result: the page still says `GREEN`.

Inspect what happened:

```bash
skiff status caddy-web
```

```bash
skiff ops events caddy-web --limit 30
```

```bash
skiff ops list --all --service caddy-web
```

Now publish and canary the BLUE release. This one has a healthy `/healthz`, so
the canary succeeds and BLUE becomes stable.

```bash
skiff deploy "$SKIFF_APPLE_BLUE_SPEC" --canary --canary-stages 50,100 --canary-bake 0s --canary-metric request_count --canary-threshold 1 --release-id "rel_blue_$SKIFF_DEMO_RUN" --operation-id "op_blue_$SKIFF_DEMO_RUN"
```

```bash
curl "$SKIFF_APPLE_CADDY_URL"
```

Expected result: the page says `BLUE`.

Use direct mode for recovery-style inspection:

```bash
skiff status caddy-web --fresh
```

```bash
skiff logs caddy-web --limit 20
```

```bash
skiff ops events caddy-web --limit 30
```

Use the local `skiffd` API context for the normal facade:

```bash
SKIFF_CONTEXT=local-apple-skiffd skiff status caddy-web --fresh
```

```bash
SKIFF_CONTEXT=local-apple-skiffd skiff tui caddy-web --read-only
```

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
skiff ops events http-hello
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
