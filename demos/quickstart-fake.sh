#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: demos/quickstart-fake.sh [options]

Run a local Skiff operator demo with file-backed object state and the fake
provider. This creates durable Skiff state, publishes signed releases, runs a
rolling canary saga, prints status/diagnostics, and renders the TUI.

Options:
  --state-dir DIR  Object-state directory. Default: ./.skiff-demo-state
  --config FILE    Skiff context config. Default: ./.skiffconfig
  --spec FILE      Service spec. Default: examples/service/http-hello/skiff.yaml
  --skiff PATH     Skiff binary to use. Default: ./bin/skiff, built if missing
  --no-build       Do not build ./bin/skiff if no binary is available
  --reset          Delete the state directory before running
  --no-tui         Skip the TUI render
  --tui            Launch the interactive TUI at the end
  -h, --help       Show this help

Examples:
  demos/quickstart-fake.sh --reset
  demos/quickstart-fake.sh --tui
EOF
}

repo_root() {
  git rev-parse --show-toplevel 2>/dev/null || pwd -P
}

quote_cmd() {
  local first=1
  local arg
  for arg in "$@"; do
    if [[ "$first" -eq 0 ]]; then
      printf ' '
    fi
    printf '%q' "$arg"
    first=0
  done
}

print_skiff_cmd() {
  if [[ "$use_skiff_shell" -eq 1 ]]; then
    printf '%s' "$skiff_cmd_string"
  else
    quote_cmd "${skiff_cmd[@]}"
  fi
}

run_skiff() {
  printf '\n$ '
  print_skiff_cmd
  local arg
  for arg in "$@"; do
    printf ' '
    printf '%q' "$arg"
  done
  printf '\n'
  if [[ "$use_skiff_shell" -eq 1 ]]; then
    # Preserve the historical SKIFF_BIN="go run ./cmd/skiff" demo hook.
    # shellcheck disable=SC2086
    $skiff_cmd_string "$@"
  else
    "${skiff_cmd[@]}" "$@"
  fi
}

run_skiff_with_context() {
  local context="$1"
  shift
  printf '\n$ SKIFF_CONTEXT=%q ' "$context"
  print_skiff_cmd
  local arg
  for arg in "$@"; do
    printf ' '
    printf '%q' "$arg"
  done
  printf '\n'
  if [[ "$use_skiff_shell" -eq 1 ]]; then
    # Preserve the historical SKIFF_BIN="go run ./cmd/skiff" demo hook.
    # shellcheck disable=SC2086
    SKIFF_CONTEXT="$context" $skiff_cmd_string "$@"
  else
    SKIFF_CONTEXT="$context" "${skiff_cmd[@]}" "$@"
  fi
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

root="$(repo_root)"
state_dir="${SKIFF_DEMO_STATE_DIR:-$root/.skiff-demo-state}"
config_path="$root/.skiffconfig"
config_path_set=0
spec_path="$root/examples/service/http-hello/skiff.yaml"
skiff_bin="${SKIFF_BIN:-}"
skiff_cmd_string=""
use_skiff_shell=0
build_if_missing=1
reset_state=0
render_tui=1
interactive_tui=0
demo_out="${DEMO_OUT:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --state-dir)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      state_dir="$2"
      shift 2
      ;;
    --config)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      config_path="$2"
      config_path_set=1
      shift 2
      ;;
    --spec)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      spec_path="$2"
      shift 2
      ;;
    --skiff)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      skiff_bin="$2"
      shift 2
      ;;
    --no-build)
      build_if_missing=0
      shift
      ;;
    --reset)
      reset_state=1
      shift
      ;;
    --no-tui)
      render_tui=0
      interactive_tui=0
      shift
      ;;
    --tui)
      render_tui=0
      interactive_tui=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown option: %s\n\n' "$1" >&2
      usage
      exit 1
      ;;
  esac
done

case "$state_dir" in
  /*) ;;
  *) state_dir="$root/$state_dir" ;;
esac

case "$spec_path" in
  /*) ;;
  *) spec_path="$root/$spec_path" ;;
esac

if [[ -n "$demo_out" ]]; then
  case "$demo_out" in
    /*) ;;
    *) demo_out="$root/$demo_out" ;;
  esac
  mkdir -p "$demo_out"
  printf 'Skiff quickstart fake-provider demo\n' > "$demo_out/summary.txt"
  if [[ "$config_path_set" -eq 0 ]]; then
    config_path="$demo_out/.skiffconfig"
  fi
fi

case "$config_path" in
  /*) ;;
  *) config_path="$root/$config_path" ;;
esac

if [[ -n "$skiff_bin" ]]; then
  skiff_cmd_string="$skiff_bin"
  use_skiff_shell=1
elif [[ -x "$root/bin/skiff" ]]; then
  skiff_cmd=("$root/bin/skiff")
elif [[ "$build_if_missing" -eq 1 ]]; then
  require_command go
  mkdir -p "$root/bin"
  printf 'Building ./bin/skiff for the demo...\n' >&2
  (
    cd "$root"
    GOCACHE="${GOCACHE:-$root/.cache/go-build}" \
    GOMODCACHE="${GOMODCACHE:-$root/.cache/gomod}" \
      go build -o "$root/bin/skiff" ./cmd/skiff
  )
  skiff_cmd=("$root/bin/skiff")
else
  printf 'No Skiff binary found. Run `make build` or pass --skiff PATH.\n' >&2
  exit 1
fi

if [[ ! -f "$spec_path" ]]; then
  printf 'Spec file not found: %s\n' "$spec_path" >&2
  exit 1
fi

if [[ "$reset_state" -eq 1 ]]; then
  if [[ -z "$state_dir" || "$state_dir" == "/" ]]; then
    printf 'Refusing to reset unsafe state dir: %s\n' "$state_dir" >&2
    exit 1
  fi
  rm -rf "$state_dir"
fi
mkdir -p "$state_dir"

service="http-hello"
env_name="prod"
region="local"
state_uri="file://$state_dir"
context_name="local-fake"
plan_context_name="local-aws-plan"

mkdir -p "$(dirname "$config_path")"
cat > "$config_path" <<EOF
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: $context_name
contexts:
  - name: $context_name
    context:
      mode: direct
      env: $env_name
      provider: fake
      region: $region
      state: "$state_uri"
  - name: $plan_context_name
    context:
      mode: direct
      env: $env_name
      provider: aws
      region: us-west-2
      state: "$state_uri"
EOF

printf 'Skiff local demo\n'
printf '  service: %s\n' "$service"
printf '  state:   %s\n' "$state_uri"
printf '  config:  %s\n' "$config_path"
printf '  context: %s\n' "$context_name"
printf '\nThis demo uses the fake provider: it writes real Skiff object state but creates no cloud resources.\n'

export SKIFF_CONFIG="$config_path"
unset SKIFF_CONTEXT

printf '\n$ export SKIFF_CONFIG=%q\n' "$SKIFF_CONFIG"
printf '$ unset SKIFF_CONTEXT\n'

run_skiff config get-contexts
run_skiff config show
run_skiff validate "$spec_path"

run_skiff_with_context "$plan_context_name" plan "$spec_path"

run_skiff deploy "$spec_path"

run_skiff deploy "$spec_path" \
  --canary \
  --canary-bake 0s \
  --canary-metric request_count \
  --canary-threshold 1

run_skiff status "$service"
run_skiff logs "$service"
run_skiff metrics "$service"
run_skiff doctor "$service"
run_skiff ops events "$service"
run_skiff ops list --all --service "$service"

if [[ "$render_tui" -eq 1 ]]; then
  run_skiff tui "$service" --once --read-only
fi

cat <<EOF

Demo complete.

State directory:
  $state_dir

Skiff config:
  $config_path

Open the interactive TUI:
  SKIFF_CONFIG="$config_path" $(print_skiff_cmd) tui $service --read-only

Inspect object state:
  find "$state_dir" -type f | sort
EOF

if [[ -n "$demo_out" ]]; then
  cat >> "$demo_out/summary.txt" <<EOF
service=$service
state=$state_uri
config=$config_path
context=$context_name
EOF
fi

if [[ "$interactive_tui" -eq 1 ]]; then
  run_skiff tui "$service" --read-only
fi
