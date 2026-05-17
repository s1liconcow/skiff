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
- `~/.gitconfig` is mounted read-only when it exists.
- The host SSH agent is forwarded by default with Apple Container `--ssh`.
- `/root/.ssh` is a guest-only tmpfs; `~/.ssh/known_hosts` is mounted read-only
  when present, and SSH uses `StrictHostKeyChecking=accept-new`.
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
