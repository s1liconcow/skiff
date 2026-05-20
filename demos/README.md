# Skiff Demos

These scripts are meant for a user who wants to touch Skiff without cloud
credentials first, then optionally run the local Apple Container workload path.

## Fast Local Operator Demo

```bash
demos/quickstart-fake.sh --reset
```

This builds `bin/skiff` if needed, writes a local `.skiffconfig`, validates
`examples/service/http-hello`, plans provider-visible primitives, deploys a
signed base release, runs a two-stage canary release through the fake provider,
prints human status/logs/metrics/doctor/events/ops output, and renders a
read-only TUI frame.

Open the interactive TUI over the generated state:

```bash
demos/quickstart-fake.sh --tui
```

The local demo writes durable object state under `.skiff-demo-state` by default.
It writes config contexts to `.skiffconfig` by default.
It does not create cloud resources or run a real workload process; the fake
provider lets the CLI, saga, status, and TUI flows run safely on any machine.
Because the workload is simulated, some diagnostic output is synthetic rather
than live target health.

## Demo Smoke Test

```bash
demos/test-local-demo.sh
```

This runs the fast demo against an isolated temporary object-state directory and
exits non-zero if any command fails.

## Real Local Workload Demo

On Apple silicon with Apple Container available:

```bash
SKIFF_INSTALL_VERSION=v0.1.0 SKIFF_INSTALL_DIR="$HOME/.local/bin" scripts/install.sh
export PATH="$HOME/.local/bin:$PATH"
skiff version
skiffd version
container --version
```

```bash
make demo-apple-context
```

This wraps the Apple Container e2e path in persistent mode. It starts RustFS,
launches Caddy in a local Linux VM, verifies signed OCI release/runtime
manifests, starts a local `skiffd` against the RustFS state, and writes
GREEN/RED/BLUE quickstart specs you can deploy by hand. It is slower and pulls
OCI images, but it shows the runner/workload path rather than only the fake
provider.

The run writes a filled-in context and environment file under
`.skiff-demo-reports/apple-container/`, then uses those contexts for the CLI
checks:

The generated Apple contexts include a `keychain://...` release signing key
reference. The demo creates or reuses that OS-keychain signer automatically; set
`SKIFF_APPLE_DEMO_KEYCHAIN_SIGNING=0` only if you are debugging the older
ephemeral local signer path. macOS may not prompt when the login keychain is
already unlocked; the generated `.env` exports the signing key ref plus the
Keychain service/account so you can verify the item without printing the
secret.

```bash
source .skiff-demo-reports/apple-container/<run>.env
security find-generic-password -s "$SKIFF_APPLE_RELEASE_SIGNING_KEYCHAIN_SERVICE" -a "$SKIFF_APPLE_RELEASE_SIGNING_KEYCHAIN_ACCOUNT" >/dev/null && echo "keychain signer found"
skiff config get-contexts
SKIFF_CONTEXT=local-apple-vms skiff status caddy-web
skiff deploy "$SKIFF_APPLE_GREEN_SPEC" --canary --canary-bake 0s --canary-metric request_count --canary-threshold 1
skiff deploy "$SKIFF_APPLE_RED_SPEC" --canary --canary-bake 0s --canary-metric request_count --canary-threshold 1
skiff deploy "$SKIFF_APPLE_BLUE_SPEC" --canary --canary-bake 0s --canary-metric request_count --canary-threshold 1
SKIFF_CONTEXT=local-apple-skiffd skiff tui caddy-web --read-only
```

The target leaves RustFS, Caddy, and `skiffd` running so the generated contexts
remain live after the command exits. Stop them with:

```bash
make demo-apple-down
```

To remove every Skiff Apple Container demo/e2e container and RustFS volume that
matches the generated Skiff names, use:

```bash
make clean-apple-containers
```

For a cleanup-safe smoke run that stops the Apple containers and in-process
`skiffd` before it exits, use:

```bash
make demo-apple-container
```

## Postgres HA Package Demo On Apple Silicon

```bash
make demo-apple-postgres-ha
```

This runs the actual `skiff.dev/postgres-ha` package on Apple Silicon through
the live StatefulGroup package harness. It starts RustFS for object state,
applies the `postgres-ha` Apple StatefulGroup scenarios, locks
`packages/postgres-ha`, builds and executes `cmd/postgres-ha-plugin`, runs a
successful `primary-switchover-update`, and verifies the unsafe replica-lag path
blocks before member mutation. It does not start a standalone Postgres container
as a substitute for the package.
