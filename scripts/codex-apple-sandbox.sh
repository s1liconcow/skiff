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
  --env KEY=VALUE        Pass an additional environment variable. Repeatable.
  --publish SPEC         Publish a port to the host. Repeatable.
                         Format: [host-ip:]host-port:container-port[/protocol]
  --memory SIZE          Container memory limit, such as 4G.
  --cpus N               Number of CPUs to allocate.
  --playwright           Enable browser-test defaults for Playwright:
                         /dev/shm tmpfs, init process, Linux browser cache,
                         CI=1, and PLAYWRIGHT_BROWSERS_PATH=/ms-playwright.
  --playwright-cache DIR Host directory for Linux Playwright browser downloads.
                         Default: ${CODEX_PLAYWRIGHT_CACHE:-$HOME/.cache/skiff-codex-playwright}
  --cap-add CAP          Add a Linux capability. Repeatable. Not needed for
                         trusted headless Playwright's default Chromium path.
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

Browser tests:
  scripts/codex-apple-sandbox.sh --playwright --memory 4G -- \
    bash -lc 'cd /workspace/skiff && npm ci && npx playwright install --with-deps chromium && npx playwright test'
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

stage_file_dir() {
  local source="$1"
  local name="$2"
  local mode="$3"
  local dir

  dir="$(mktemp -d "${TMPDIR:-/tmp}/skiff-codex-sandbox.XXXXXX")"
  temp_dirs+=("$dir")
  cp "$source" "$dir/$name"
  chmod "$mode" "$dir/$name"
  staged_file_dir="$dir"
}

image="${CODEX_SANDBOX_IMAGE:-ghcr.io/openai/codex-universal:latest}"
name="${CODEX_SANDBOX_NAME:-skiff-codex-sandbox-$(timestamp)}"
repo_dir="$(repo_root)"
codex_home="${CODEX_HOME:-$HOME/.codex}"
container_workdir="/workspace/skiff"
container_codex_home="/root/.codex"
container_playwright_cache="/ms-playwright"
shell_path="/bin/bash"
mount_gh=0
mount_gitconfig=1
forward_ssh=1
enable_virtualization=1
enable_playwright=0
dry_run=0
custom_command=()
extra_env=()
publish_specs=()
cap_adds=()
memory_limit=""
cpu_limit=""
playwright_cache="${CODEX_PLAYWRIGHT_CACHE:-$HOME/.cache/skiff-codex-playwright}"
temp_dirs=()
gitconfig_mount_dir=""
ssh_mount_dir=""
staged_file_dir=""

cleanup_temp_dirs() {
  local dir

  for dir in "${temp_dirs[@]:-}"; do
    if [[ -n "$dir" && -d "$dir" ]]; then
      rm -rf "$dir"
    fi
  done
}

trap cleanup_temp_dirs EXIT INT TERM

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
    --env)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      extra_env+=("$2")
      shift 2
      ;;
    --publish)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      publish_specs+=("$2")
      shift 2
      ;;
    --memory)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      memory_limit="$2"
      shift 2
      ;;
    --cpus)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      cpu_limit="$2"
      shift 2
      ;;
    --playwright)
      enable_playwright=1
      shift
      ;;
    --playwright-cache)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      playwright_cache="$2"
      shift 2
      ;;
    --cap-add)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      cap_adds+=("$2")
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

if [[ "$enable_playwright" -eq 1 && "$dry_run" -eq 0 ]]; then
  mkdir -p "$playwright_cache"
fi

if [[ "$mount_gitconfig" -eq 1 && -f "$HOME/.gitconfig" ]]; then
  stage_file_dir "$HOME/.gitconfig" ".gitconfig" 0444
  gitconfig_mount_dir="$staged_file_dir"
fi

if [[ "$forward_ssh" -eq 1 && -f "$HOME/.ssh/known_hosts" ]]; then
  ssh_mount_dir="$(mktemp -d "${TMPDIR:-/tmp}/skiff-codex-sandbox-ssh.XXXXXX")"
  temp_dirs+=("$ssh_mount_dir")
  cp "$HOME/.ssh/known_hosts" "$ssh_mount_dir/known_hosts"
  chmod 0700 "$ssh_mount_dir"
  chmod 0600 "$ssh_mount_dir/known_hosts"
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

if [[ -n "$memory_limit" ]]; then
  cmd+=(--memory "$memory_limit")
fi

if [[ -n "$cpu_limit" ]]; then
  cmd+=(--cpus "$cpu_limit")
fi

if [[ "${#extra_env[@]}" -gt 0 ]]; then
  for env_value in "${extra_env[@]}"; do
    cmd+=(--env "$env_value")
  done
fi

if [[ "${#publish_specs[@]}" -gt 0 ]]; then
  for spec in "${publish_specs[@]}"; do
    cmd+=(--publish "$spec")
  done
fi

if [[ "${#cap_adds[@]}" -gt 0 ]]; then
  for cap in "${cap_adds[@]}"; do
    cmd+=(--cap-add "$cap")
  done
fi

if [[ "$enable_virtualization" -eq 1 ]]; then
  cmd+=(--virtualization)
fi

if [[ "$enable_playwright" -eq 1 ]]; then
  cmd+=(
    --init
    --tmpfs /dev/shm
    --env "CI=1"
    --env "PLAYWRIGHT_BROWSERS_PATH=$container_playwright_cache"
    --env "PLAYWRIGHT_SKIP_BROWSER_GC=1"
    --mount "type=bind,source=$playwright_cache,target=$container_playwright_cache"
  )
fi

if [[ "$forward_ssh" -eq 1 ]]; then
  cmd+=(
    --ssh
    --env "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new"
  )
  if [[ -n "$ssh_mount_dir" ]]; then
    cmd+=(--mount "type=bind,source=$ssh_mount_dir,target=/root/.ssh")
  else
    cmd+=(--tmpfs /root/.ssh)
  fi
fi

if [[ -n "$gitconfig_mount_dir" ]]; then
  cmd+=(
    --env "GIT_CONFIG_GLOBAL=/root/.skiff-host-gitconfig/.gitconfig"
    --mount "type=bind,source=$gitconfig_mount_dir,target=/root/.skiff-host-gitconfig,readonly"
  )
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
if [[ "$enable_playwright" -eq 1 ]]; then
  printf 'Playwright browser cache is mounted at %s; /dev/shm tmpfs enabled\n' \
    "$container_playwright_cache" >&2
fi
"${cmd[@]}"
