#!/usr/bin/env bash
#
# install.sh — one-shot local installer for BetterAutoHeal.
#
# What it does:
#   1. Verifies docker + docker compose are available.
#   2. Creates a .env from .env.example if none exists, and makes sure
#      BAH_SLACK_WEBHOOK_URL is filled in (prompts if still the placeholder).
#   3. Builds the image.
#   4. Starts the example stack in the background via docker compose.
#
# Usage:
#   ./install.sh              # interactive
#   ./install.sh --no-sample  # only bring up the autoheal service, not the sample nginx
#   ./install.sh -h           # help

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

COMPOSE_FILE="docker-compose.example.yml"
ENV_FILE=".env"
ENV_EXAMPLE=".env.example"
PLACEHOLDER="https://hooks.slack.com/services/REPLACE/WITH/YOUR_WEBHOOK"

log()  { printf "\033[1;34m[install]\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m[install]\033[0m %s\n" "$*" >&2; }
die()  { printf "\033[1;31m[install]\033[0m %s\n" "$*" >&2; exit 1; }

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

SERVICES=()
for arg in "$@"; do
  case "$arg" in
    -h|--help) usage ;;
    --no-sample) SERVICES=(betterautoheal) ;;
    *) die "unknown flag: $arg (try --help)" ;;
  esac
done

# 1. Prereqs.
command -v docker >/dev/null 2>&1 || die "docker not found in PATH. Install Docker first: https://docs.docker.com/get-docker/"
if ! docker compose version >/dev/null 2>&1; then
  die "`docker compose` plugin not found. Install Docker Compose v2: https://docs.docker.com/compose/install/"
fi
docker info >/dev/null 2>&1 || die "docker daemon is not reachable. Start Docker and re-run."

# 2. .env.
if [[ ! -f "$ENV_FILE" ]]; then
  if [[ ! -f "$ENV_EXAMPLE" ]]; then
    die "$ENV_EXAMPLE is missing — cannot bootstrap $ENV_FILE"
  fi
  log "creating $ENV_FILE from $ENV_EXAMPLE"
  cp "$ENV_EXAMPLE" "$ENV_FILE"
fi

# Validate webhook unless notifications are disabled.
NOTIFY="$(grep -E '^BAH_NOTIFY=' "$ENV_FILE" | tail -n1 | cut -d= -f2- || true)"
NOTIFY="${NOTIFY:-true}"
CURRENT_WEBHOOK="$(grep -E '^BAH_SLACK_WEBHOOK_URL=' "$ENV_FILE" | tail -n1 | cut -d= -f2- || true)"

if [[ "$NOTIFY" == "true" && ( -z "$CURRENT_WEBHOOK" || "$CURRENT_WEBHOOK" == "$PLACEHOLDER" ) ]]; then
  if [[ -t 0 ]]; then
    read -rp "Enter your Slack Incoming Webhook URL (or leave blank to disable notifications): " INPUT
    if [[ -n "$INPUT" ]]; then
      # Portable in-place edit: rewrite the matching line.
      tmp="$(mktemp)"
      awk -v url="$INPUT" '
        BEGIN { replaced = 0 }
        /^BAH_SLACK_WEBHOOK_URL=/ { print "BAH_SLACK_WEBHOOK_URL=" url; replaced = 1; next }
        { print }
        END { if (!replaced) print "BAH_SLACK_WEBHOOK_URL=" url }
      ' "$ENV_FILE" >"$tmp"
      mv "$tmp" "$ENV_FILE"
      log "webhook URL written to $ENV_FILE"
    else
      tmp="$(mktemp)"
      awk '
        /^BAH_NOTIFY=/ { print "BAH_NOTIFY=false"; next }
        { print }
      ' "$ENV_FILE" >"$tmp"
      mv "$tmp" "$ENV_FILE"
      warn "no webhook provided; set BAH_NOTIFY=false in $ENV_FILE"
    fi
  else
    die "BAH_SLACK_WEBHOOK_URL is still the placeholder in $ENV_FILE. Edit it and re-run, or pass --no-sample after setting BAH_NOTIFY=false."
  fi
fi

# 3. Build.
log "building image (this may take a minute on first run)"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" build

# 4. Start.
log "starting stack"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d "${SERVICES[@]}"

log "stack is up. Useful commands:"
cat <<EOF

  # follow BetterAutoHeal logs
  docker compose -f $COMPOSE_FILE logs -f betterautoheal

  # stop everything
  docker compose -f $COMPOSE_FILE down

  # trigger a test failure on the sample nginx (healthcheck will flip):
  docker compose -f $COMPOSE_FILE exec flaky nginx -s stop

EOF
