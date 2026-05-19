# Install Skiff

Skiff release archives contain `skiff`, `skiffd`, `skiff-runner`, and
`skiff-worker` for Linux and macOS on amd64/arm64.

Build local release artifacts:

```bash
scripts/build-release.sh v0.1.0
```

Install from a release artifact directory:

```bash
SKIFF_INSTALL_VERSION=v0.1.0 \
SKIFF_INSTALL_BASE_URL=file://$PWD/dist \
SKIFF_INSTALL_DIR=$HOME/.local/bin \
scripts/install.sh
```

Install from GitHub releases:

```bash
curl -fsSL https://raw.githubusercontent.com/s1liconcow/skiff/main/scripts/install.sh | \
  SKIFF_INSTALL_VERSION=v0.1.0 bash
```

The installer is a Bash script. It defaults to `$HOME/.local/bin`; pass
`--system` or set `SKIFF_INSTALL_DIR=/usr/local/bin` when installing for a VM
image or another system-wide path.

Useful flags:

```bash
scripts/install.sh --version v0.1.0 --install-dir "$HOME/.local/bin"
scripts/install.sh --offline dist/skiff_v0.1.0_linux_amd64.tar.gz --checksums dist/checksums.txt
scripts/install.sh --from-source --force
scripts/install.sh --quiet --no-gum
```

The installer verifies `checksums.txt` before copying binaries. If
`SKIFF_INSTALL_PUBLIC_KEY` is set, it also verifies the archive signature with
`cosign` or `minisign`. `--no-verify` skips artifact verification and should be
used only for trusted local test artifacts.

It also installs shell completions when possible and writes a small Skiff skill
for local Codex or Claude installations. Use `--no-completions` or
`--no-agents` to skip those integrations.

Runner image inputs live in `build/runner-image/`. The base image installs
`skiff-runner`, enables `skiff-runner.service`, and expects direct object-state
runner user-data at `/etc/skiff/runner.json`.

Self-hosted `skiffd` is deployed as a normal Skiff service from
`examples/skiffd/skiff.yaml`. `skiffd` remains a stateless facade; direct mode is
still the recovery path.
