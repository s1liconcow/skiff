# Codex Apple Container Sandbox

Use `scripts/codex-apple-sandbox.sh` to start a shell inside an Apple
Container-backed Linux VM with only the intended host state mounted. This is the
outer safety boundary for running Codex with
`--dangerously-bypass-approvals-and-sandbox`.

The tool does not start Codex or any worker loop. It only launches a shell.

```bash
make codex-apple-sandbox
```

Inside the shell, the repository is mounted at `/workspace/skiff` and Codex
state is mounted at `/root/.codex`:

```bash
codex --dangerously-bypass-approvals-and-sandbox -C /workspace/skiff \
  -p "/goal burn down all remaining beads"
```

## What It Exposes

- The Skiff git repository is mounted writable at `/workspace/skiff`.
- `${CODEX_HOME:-$HOME/.codex}` is mounted writable at `/root/.codex` so Codex
  auth, config, sessions, and logs are available.
- `~/.gitconfig` is copied into a short-lived temp directory and mounted
  read-only when it exists. `GIT_CONFIG_GLOBAL` points Git at that staged file.
- The host SSH agent is forwarded by default with Apple Container `--ssh`.
- `/root/.ssh` is a guest-only temp directory. If `~/.ssh/known_hosts` exists,
  it is copied into that directory first; SSH uses
  `StrictHostKeyChecking=accept-new`.
- Nested virtualization is enabled by default with `--virtualization`, allowing
  container runtimes inside the guest when the image supports them.
- Outbound networking uses the container runtime default network, which is
  suitable for GitHub pushes when credentials are available.

GitHub CLI credentials are not mounted by default because they expose bearer
tokens. Use `--mount-gh` when HTTPS `gh` auth is needed:

```bash
scripts/codex-apple-sandbox.sh --mount-gh
```

## Useful Options

```bash
scripts/codex-apple-sandbox.sh --dry-run
scripts/codex-apple-sandbox.sh --image ghcr.io/openai/codex-universal:latest
scripts/codex-apple-sandbox.sh --no-virtualization
scripts/codex-apple-sandbox.sh --no-ssh-agent
scripts/codex-apple-sandbox.sh -- --version
```

Override the image with `CODEX_SANDBOX_IMAGE` if you need a custom toolchain or
a preinstalled nested container runtime.

## Playwright Browser Tests

Use `--playwright` when the mounted workspace needs to run Playwright browser
tests inside the Linux sandbox:

```bash
make codex-apple-sandbox-playwright
```

Or run a one-shot browser-test command:

```bash
scripts/codex-apple-sandbox.sh --playwright --memory 4G -- \
  bash -lc 'cd /workspace/skiff && npm ci && npx playwright install --with-deps chromium && npx playwright test'
```

`--playwright` enables the pieces Chromium usually needs in a container:

- `/dev/shm` is mounted as tmpfs.
- An init process is enabled so browser child processes are reaped.
- `CI=1`, `PLAYWRIGHT_BROWSERS_PATH=/ms-playwright`, and
  `PLAYWRIGHT_SKIP_BROWSER_GC=1` are set.
- A host cache is mounted at `/ms-playwright` so Linux browser downloads can be
  reused across sandbox runs. Override it with `CODEX_PLAYWRIGHT_CACHE` or
  `--playwright-cache`.

Run the dev server inside the same sandbox when possible, then point Playwright
at `127.0.0.1`. If you need to inspect a service from the host, publish the
port explicitly:

```bash
scripts/codex-apple-sandbox.sh --playwright --publish 127.0.0.1:3000:3000
```

The Playwright mode does not add extra Linux capabilities by default. If a
specific browser setup requires a capability, pass it explicitly with
`--cap-add`; keep that opt-in because it widens the sandbox.
