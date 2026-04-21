#!/usr/bin/env bash
#
# install.sh — local installer for BetterAutoHeal.
#
# Two modes (default is `integrate`):
#
#   integrate Generate a docker-compose.betterautoheal.yml overlay next to an
#             existing compose file, labelling the selected services so
#             BetterAutoHeal monitors them. Nothing in your original file is touched.
#   demo      Bring up the bundled docker-compose.example.yml (BAH + a sample nginx).
#
# Usage:
#   ./install.sh                                   # interactive; integrate by default
#   ./install.sh --compose-file PATH \
#                [--services s1,s2] \
#                [--project-name "My Stack"]       # same, non-interactive
#   ./install.sh demo [--no-sample]                # bundled example
#   ./install.sh -h
#
# The integrate mode writes two files next to the target compose file:
#   docker-compose.betterautoheal.yml   overlay with BAH service + per-service labels
#   .env                                BAH_* configuration (seeded from .env.example)
# Then it starts the combined stack with:
#   docker compose -f <your-file> -f docker-compose.betterautoheal.yml up -d

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ENV_EXAMPLE="$SCRIPT_DIR/.env.example"
PLACEHOLDER="https://hooks.slack.com/services/REPLACE/WITH/YOUR_WEBHOOK"
IMAGE_TAG="betterautoheal:dev"

log()  { printf "\033[1;34m[install]\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m[install]\033[0m %s\n" "$*" >&2; }
die()  { printf "\033[1;31m[install]\033[0m %s\n" "$*" >&2; exit 1; }

usage() {
  sed -n '/^# install.sh/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

########################################
# Prereq checks
########################################
check_prereqs() {
  command -v docker >/dev/null 2>&1 || die "docker not found in PATH."
  docker compose version >/dev/null 2>&1 || die "'docker compose' plugin not found."
  docker info >/dev/null 2>&1 || die "docker daemon is not reachable."
}

########################################
# .env helpers
########################################
# Seed an .env at $1 from .env.example if missing.
seed_env() {
  local target="$1"
  if [[ ! -f "$target" ]]; then
    [[ -f "$ENV_EXAMPLE" ]] || die ".env.example is missing at $ENV_EXAMPLE"
    log "creating $target from .env.example"
    cp "$ENV_EXAMPLE" "$target"
  fi
}

# Rewrite (or append) KEY=VALUE in $1.
env_set() {
  local file="$1" key="$2" value="$3" tmp
  tmp="$(mktemp)"
  awk -v k="$key" -v v="$value" '
    BEGIN { replaced = 0 }
    $0 ~ "^" k "=" { print k "=" v; replaced = 1; next }
    { print }
    END { if (!replaced) print k "=" v }
  ' "$file" >"$tmp"
  mv "$tmp" "$file"
}

# Read a value from a KEY=... line in an .env file.
env_get() {
  local file="$1" key="$2"
  grep -E "^$key=" "$file" 2>/dev/null | tail -n1 | cut -d= -f2- || true
}

prompt_if_tty() {
  local prompt="$1"
  if [[ -t 0 ]]; then
    local input
    read -rp "$prompt" input
    printf "%s" "$input"
  else
    printf ""
  fi
}

# Ensure the .env at $1 has a real webhook (or disables notifications).
fill_webhook() {
  local env_file="$1"
  local current notify
  notify="$(env_get "$env_file" BAH_NOTIFY)"
  notify="${notify:-true}"
  current="$(env_get "$env_file" BAH_SLACK_WEBHOOK_URL)"

  if [[ "$notify" == "true" && ( -z "$current" || "$current" == "$PLACEHOLDER" ) ]]; then
    local input
    input="$(prompt_if_tty "Slack Incoming Webhook URL (blank to disable notifications): ")"
    if [[ -n "$input" ]]; then
      env_set "$env_file" BAH_SLACK_WEBHOOK_URL "$input"
      log "webhook URL written to $env_file"
    elif [[ -t 0 ]]; then
      env_set "$env_file" BAH_NOTIFY "false"
      warn "notifications disabled in $env_file"
    else
      die "BAH_SLACK_WEBHOOK_URL is still the placeholder. Edit $env_file and re-run."
    fi
  fi
}

# Ensure BAH_PROJECT_NAME is set (only if caller passed one, or we're interactive).
fill_project_name() {
  local env_file="$1" provided="${2-}"
  if [[ -n "$provided" ]]; then
    env_set "$env_file" BAH_PROJECT_NAME "$provided"
    return
  fi
  local existing
  existing="$(env_get "$env_file" BAH_PROJECT_NAME)"
  if [[ -z "$existing" && -t 0 ]]; then
    local input
    input="$(prompt_if_tty "Project name for Slack messages (blank to skip): ")"
    if [[ -n "$input" ]]; then
      env_set "$env_file" BAH_PROJECT_NAME "$input"
    fi
  fi
}

########################################
# Build image if missing
########################################
ensure_image() {
  if docker image inspect "$IMAGE_TAG" >/dev/null 2>&1; then
    log "$IMAGE_TAG already present; skipping build"
  else
    log "building $IMAGE_TAG"
    docker build -t "$IMAGE_TAG" "$SCRIPT_DIR"
  fi
}

########################################
# Demo mode
########################################
run_demo() {
  local no_sample="${1:-0}"
  local compose_file="$SCRIPT_DIR/docker-compose.example.yml"
  local env_file="$SCRIPT_DIR/.env"
  seed_env "$env_file"
  fill_webhook "$env_file"
  fill_project_name "$env_file" "${PROJECT_NAME:-}"

  log "building image"
  docker compose --env-file "$env_file" -f "$compose_file" build

  log "starting stack"
  if [[ "$no_sample" == "1" ]]; then
    docker compose --env-file "$env_file" -f "$compose_file" up -d betterautoheal
  else
    docker compose --env-file "$env_file" -f "$compose_file" up -d
  fi

  cat <<EOF

Stack is up. Useful commands:
  docker compose -f $compose_file logs -f betterautoheal
  docker compose -f $compose_file down
EOF
}

########################################
# Integrate mode
########################################
list_services() {
  local compose_file="$1"
  docker compose -f "$compose_file" config --services 2>/dev/null | sort
}

# Write the overlay file at $1 for target compose $2, with services list in $3 (space-separated).
write_overlay() {
  local overlay="$1" target_dir; target_dir="$(cd -- "$(dirname -- "$2")" && pwd)"
  shift 2
  local services=("$@")

  {
    printf "# Generated by install.sh — do not hand-edit.\n"
    printf "# Overlay for BetterAutoHeal. Use with:\n"
    printf "#   docker compose -f <your-file> -f %s up -d\n" "$(basename "$overlay")"
    printf "\nservices:\n"
    for svc in "${services[@]}"; do
      cat <<EOF
  $svc:
    labels:
      - betterautoheal.enable=true
      - betterautoheal.mode=compose
      - betterautoheal.log_lines=200

EOF
    done
    cat <<EOF
  betterautoheal:
    image: $IMAGE_TAG
    container_name: betterautoheal
    restart: unless-stopped
    env_file: .env
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - $target_dir:$target_dir:ro
EOF
  } >"$overlay"
}

run_integrate() {
  local target="${COMPOSE_FILE:-}"
  if [[ -z "$target" && -t 0 ]]; then
    target="$(prompt_if_tty "Path to your existing docker-compose.yml: ")"
  fi
  [[ -n "$target" ]] || die "--compose-file is required in integrate mode"
  [[ -f "$target" ]] || die "compose file not found: $target"
  target="$(cd -- "$(dirname -- "$target")" && pwd)/$(basename -- "$target")"
  local target_dir; target_dir="$(dirname -- "$target")"

  local available
  mapfile -t available < <(list_services "$target")
  [[ ${#available[@]} -gt 0 ]] || die "no services found in $target (is the file valid?)"

  local chosen_raw="${SERVICES:-}"
  if [[ -z "$chosen_raw" && -t 0 ]]; then
    printf "Services found in %s:\n" "$target"
    printf "  - %s\n" "${available[@]}"
    chosen_raw="$(prompt_if_tty "Comma-separated services to monitor (blank = all): ")"
  fi

  local chosen=()
  if [[ -z "$chosen_raw" ]]; then
    chosen=("${available[@]}")
  else
    IFS=',' read -ra chosen <<<"${chosen_raw// /}"
    # Validate each chosen name exists.
    for s in "${chosen[@]}"; do
      local found=0
      for a in "${available[@]}"; do
        [[ "$s" == "$a" ]] && { found=1; break; }
      done
      [[ "$found" == 1 ]] || die "service not found in $target: $s"
    done
  fi

  ensure_image

  local overlay="$target_dir/docker-compose.betterautoheal.yml"
  if [[ -f "$overlay" ]]; then
    warn "overwriting existing $overlay"
  fi
  log "writing overlay $overlay (services: ${chosen[*]})"
  write_overlay "$overlay" "$target" "${chosen[@]}"

  local env_file="$target_dir/.env"
  seed_env "$env_file"
  fill_webhook "$env_file"
  fill_project_name "$env_file" "${PROJECT_NAME:-}"

  log "starting stack"
  (
    cd -- "$target_dir"
    docker compose -f "$(basename -- "$target")" -f "$(basename -- "$overlay")" up -d
  )

  log "done. BetterAutoHeal is now watching: ${chosen[*]}"
  cat <<EOF

Reminders:
  * Each monitored service needs its own healthcheck; BetterAutoHeal can only act
    on containers Docker marks as unhealthy.
  * To tear down just the BAH service without touching your app:
      docker compose -f $(basename "$target") -f $(basename "$overlay") stop betterautoheal
  * To remove the BAH labels, delete $overlay and run:
      docker compose -f $(basename "$target") up -d --remove-orphans
EOF
}

########################################
# CLI parsing
########################################
MODE=""
NO_SAMPLE=0
COMPOSE_FILE=""
SERVICES=""
PROJECT_NAME=""

if [[ $# -eq 0 && -t 0 ]]; then
  MODE="$(prompt_if_tty "Mode — [integrate] existing compose file (default), [demo] bundled example: ")"
  MODE="${MODE:-integrate}"
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)          usage ;;
    demo|integrate)     MODE="$1"; shift ;;
    --no-sample)        NO_SAMPLE=1; shift ;;
    --compose-file)     COMPOSE_FILE="$2"; shift 2 ;;
    --services)         SERVICES="$2"; shift 2 ;;
    --project-name)     PROJECT_NAME="$2"; shift 2 ;;
    *) die "unknown arg: $1 (try --help)" ;;
  esac
done

export COMPOSE_FILE SERVICES PROJECT_NAME
check_prereqs

case "${MODE:-integrate}" in
  demo)      run_demo "$NO_SAMPLE" ;;
  integrate) run_integrate ;;
  *)         die "unknown mode: $MODE (use integrate or demo)" ;;
esac
