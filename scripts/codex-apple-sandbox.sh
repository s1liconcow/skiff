#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: scripts/codex-apple-sandbox.sh [options] [-- command...]

Launch an interactive Apple Container shell with the Skiff repo and Codex home
bind-mounted. The shell is intended as the outer sandbox for running Codex with
--dangerously-bypass-approvals-and-sandbox from inside the container.

Options:
  --image IMAGE          Container image to run.
                         Default: ghcr.io/openai/codex-universal:latest
  --name NAME            Container name. Default: skiff-codex-sandbox-<timestamp>
  --repo DIR             Repository directory to mount. Default: git root.
  --codex-home DIR       Codex home to mount. Default: ${CODEX_HOME:-$HOME/.codex}
  --workdir DIR          Container workdir. Default: /workspace/skiff
  --shell SHELL          Shell to launch when no command is supplied.
                         Default: /bin/bash
  --mount-gh             Bind-mount ~/.config/gh read-only for HTTPS gh auth.
  --no-gitconfig         Do not bind-mount ~/.gitconfig read-only.
  --no-ssh-agent         Do not forward the host SSH agent.
  --no-virtualization    Do not expose nested virtualization.
  --dry-run              Print the container command instead of running it.
  -h, --help             Show this help.

Environment:
  CODEX_SANDBOX_IMAGE    Overrides the default image.
  CODEX_SANDBOX_NAME     Overrides the default container name.
  CODEX_HOME             Host Codex home to mount.

Example:
  scripts/codex-apple-sandbox.sh

Inside the shell:
  codex --dangerously-bypass-approvals-and-sandbox -C /workspace/skiff \
    -p "/goal burn down all remaining beads"
EOF
}

quote_arg() {
  printf '%q' "$1"
}

print_command() {
  local first=1
  local arg

  for arg in "$@"; do
    if [[ "$first" -eq 0 ]]; then
      printf ' '
    fi
    quote_arg "$arg"
    first=0
  done
  printf '\n'
}

repo_root() {
  git rev-parse --show-toplevel 2>/dev/null || pwd -P
}

require_command() {
  local cmd="$1"

  if ! command -v "$cmd" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$cmd" >&2
    exit 1
  fi
}

timestamp() {
  date -u +%Y%m%d%H%M%S
}

image="${CODEX_SANDBOX_IMAGE:-ghcr.io/openai/codex-universal:latest}"
name="${CODEX_SANDBOX_NAME:-skiff-codex-sandbox-$(timestamp)}"
repo_dir="$(repo_root)"
codex_home="${CODEX_HOME:-$HOME/.codex}"
container_workdir="/workspace/skiff"
container_codex_home="/root/.codex"
shell_path="/bin/bash"
mount_gh=0
mount_gitconfig=1
forward_ssh=1
enable_virtualization=1
dry_run=0
custom_command=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --image)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      image="$2"
      shift 2
      ;;
    --name)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      name="$2"
      shift 2
      ;;
    --repo)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      repo_dir="$2"
      shift 2
      ;;
    --codex-home)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      codex_home="$2"
      shift 2
      ;;
    --workdir)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      container_workdir="$2"
      shift 2
      ;;
    --shell)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      shell_path="$2"
      shift 2
      ;;
    --mount-gh)
      mount_gh=1
      shift
      ;;
    --no-gitconfig)
      mount_gitconfig=0
      shift
      ;;
    --no-ssh-agent)
      forward_ssh=0
      shift
      ;;
    --no-virtualization)
      enable_virtualization=0
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      custom_command=("$@")
      break
      ;;
    *)
      printf 'Unknown option: %s\n\n' "$1" >&2
      usage
      exit 1
      ;;
  esac
done

require_command container

if [[ ! -d "$repo_dir/.git" ]]; then
  printf 'Repository directory does not look like a git repo: %s\n' "$repo_dir" >&2
  exit 1
fi

if [[ ! -d "$codex_home" ]]; then
  printf 'Codex home does not exist: %s\n' "$codex_home" >&2
  exit 1
fi

cmd=(
  container run
  --rm
  --interactive
  --tty
  --name "$name"
  --workdir "$container_workdir"
  --env "CODEX_HOME=$container_codex_home"
  --env "SKIFF_CODEX_SANDBOX=apple-container"
  --mount "type=bind,source=$repo_dir,target=$container_workdir"
  --mount "type=bind,source=$codex_home,target=$container_codex_home"
)

if [[ "$enable_virtualization" -eq 1 ]]; then
  cmd+=(--virtualization)
fi

if [[ "$forward_ssh" -eq 1 ]]; then
  cmd+=(
    --ssh
    --tmpfs /root/.ssh
    --env "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new"
  )
fi

if [[ "$mount_gitconfig" -eq 1 && -f "$HOME/.gitconfig" ]]; then
  cmd+=(--mount "type=bind,source=$HOME/.gitconfig,target=/root/.gitconfig,readonly")
fi

if [[ "$forward_ssh" -eq 1 && -f "$HOME/.ssh/known_hosts" ]]; then
  cmd+=(--mount "type=bind,source=$HOME/.ssh/known_hosts,target=/root/.ssh/known_hosts,readonly")
fi

if [[ "$mount_gh" -eq 1 ]]; then
  if [[ ! -d "$HOME/.config/gh" ]]; then
    printf 'GitHub CLI config not found: %s\n' "$HOME/.config/gh" >&2
    exit 1
  fi
  cmd+=(--mount "type=bind,source=$HOME/.config/gh,target=/root/.config/gh,readonly")
fi

if [[ "${#custom_command[@]}" -gt 0 ]]; then
  cmd+=("$image" "${custom_command[@]}")
else
  cmd+=(--entrypoint "$shell_path" "$image" -l)
fi

if [[ "$dry_run" -eq 1 ]]; then
  print_command "${cmd[@]}"
  exit 0
fi

printf 'Launching %s with repo mounted at %s\n' "$name" "$container_workdir" >&2
printf 'Codex home is mounted at %s; nested virtualization: %s; SSH agent: %s\n' \
  "$container_codex_home" "$enable_virtualization" "$forward_ssh" >&2
exec "${cmd[@]}"
