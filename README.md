# BetterAutoHeal

Docker container watchdog inspired by [`willfarrell/docker-autoheal`](https://github.com/willfarrell/docker-autoheal). Polls the Docker API for containers reporting an `unhealthy` healthcheck and revives them — with three extras over the original:

1. **Two revive modes**, selectable per container via label:
   - `restart` — `docker restart` (the original behavior)
   - `compose` — `docker compose stop <svc> && docker compose start <svc>` for cleaner lifecycles on compose-managed services
2. **Slack notifications** with a tail of the container's logs *captured before* the revival, so the failing state is preserved even after the container restarts.
3. **Restart-loop detection** — spots the other failure mode (container keeps exiting and Docker's `restart:` policy keeps bringing it back) and alerts you, with the option to auto-stop the looping container.

## Quick start

The bundled `install.sh` has two modes. **Default is `integrate`** — attaching BetterAutoHeal to your existing compose stack. `demo` brings up a bundled example instead.

### Integrate — add BetterAutoHeal to an existing compose file

Point the installer at your own `docker-compose.yml` and pick which services it should watch. It generates a non-destructive overlay (`docker-compose.betterautoheal.yml`) next to your file — your original is not touched.

```bash
./install.sh \
  --compose-file /path/to/your/docker-compose.yml \
  --services homeassistant,grafana \
  --project-name "Home Server"
```

Or run it with no flags and it walks you through it interactively:
- enumerates services from your compose file
- asks which ones to monitor (blank = all)
- asks for a project name used in Slack headers
- asks for the Slack webhook (once; stored in `.env` next to your compose file)

After it runs you'll have two new files next to your compose file:
- `docker-compose.betterautoheal.yml` — overlay with the `betterautoheal` service + per-service labels
- `.env` — the `BAH_*` config (gitignore it!)

From then on, bring your stack up with:
```bash
docker compose -f docker-compose.yml -f docker-compose.betterautoheal.yml up -d
```

**Important**: each monitored service still needs its own `healthcheck:`. BetterAutoHeal only acts on containers Docker reports as `unhealthy`, so an unhealthchecked service is invisible to it.

### Demo — bundled example stack (BAH + a sample nginx)

```bash
./install.sh demo                 # explicit demo mode
./install.sh demo --no-sample     # only the autoheal service, skip the demo nginx
```

Seeds `.env` from `.env.example`, prompts for your Slack webhook and (optional) project name, builds the image, and starts the stack.

### Providing the Slack webhook URL

The webhook URL is a secret, so the example compose file reads it from your shell (`${BAH_SLACK_WEBHOOK_URL:?...}`) and fails fast if it is not set. You have three reasonable options, in order of preference:

1. **Shell env** — `export BAH_SLACK_WEBHOOK_URL=...` before running `docker compose up`. Nothing to commit, secret never lands on disk in the repo.
2. **`.env` file next to the compose file** — create a file named `.env` with `BAH_SLACK_WEBHOOK_URL=https://hooks.slack.com/...`. Docker Compose loads it automatically. **Make sure `.env` is in `.gitignore`** (the repo's `.gitignore` already covers it).
3. **Hard-code it in `docker-compose.yml`** — yes, you can put the literal URL under `services.betterautoheal.environment.BAH_SLACK_WEBHOOK_URL`. It works, but the compose file then contains a secret; only do this on private hosts and never commit that file to a public repo. Prefer options 1 or 2 for anything shared.

For production, Docker secrets or your orchestrator's secret store (Swarm secrets, Kubernetes Secret, AWS SSM, etc.) is the right home for the webhook URL.

## Opting a container in

Add labels to any container you want monitored:

```yaml
labels:
  - betterautoheal.enable=true       # required: opt-in
  - betterautoheal.mode=compose      # optional: restart (default) or compose
  - betterautoheal.log_lines=200     # optional: override BAH_LOG_LINES
  - betterautoheal.loop_action=stop  # optional: recreate (default) | stop | notify | ignore
healthcheck:
  test: ["CMD", "curl", "-f", "http://localhost/health"]
  interval: 10s
  timeout: 3s
  retries: 3
```

Containers without `betterautoheal.enable=true` are ignored.

## Restart-loop detection

Docker's own `restart:` policy silently keeps a crashing container in a loop. BAH watches each opted-in container's `RestartCount` and, if it grows by `BAH_LOOP_THRESHOLD` (default 3) within `BAH_LOOP_WINDOW` (default 2m), it posts a `:recycle: Restart loop: <name>` Slack message with the last N log lines and then acts on the container.

Per-container behavior via `betterautoheal.loop_action`:

| Value | Effect |
|-------|--------|
| `recreate` *(default)* | Slack alert, then `docker compose up -d --force-recreate --no-deps <svc>` — the single-service equivalent of `docker compose down && up -d`. Resets `RestartCount` to 0 and often clears transient bad state. Requires compose labels on the container; falls back to notify-only otherwise. |
| `stop` | Slack alert, then `docker stop` the container. Use when a looping service is worse than a down one. |
| `notify` | Slack alert only; Docker's restart policy keeps retrying. Use for transient failures that are expected to self-heal. |
| `ignore` | No loop detection for this container. Use for services you bounce deliberately. |

Cooldown is enforced per container (`BAH_LOOP_COOLDOWN`, default 15m) so a persistent loop doesn't spam the channel or re-recreate on every failed cycle. After a recreate, BAH forgets the container's history — the freshly created container starts with a clean baseline.

BAH can't *fix* a crash loop permanently — if the bug is deterministic, the recreated container will loop again after the cooldown. The alert is what actually matters: it tells you to go look at the logs and fix the root cause.

## Configuration (env vars)

| Var | Default | Purpose |
|-----|---------|---------|
| `BAH_INTERVAL` | `5s` | Poll interval (Go duration) |
| `BAH_START_PERIOD` | `0s` | Grace period after a container starts before it can be revived |
| `BAH_STOP_TIMEOUT` | `10` | Seconds passed to `docker restart` |
| `BAH_DEFAULT_MODE` | `restart` | `restart` or `compose` when a container has no `betterautoheal.mode` label |
| `BAH_LABEL_FILTER` | `betterautoheal.enable=true` | Docker label filter used to discover monitored containers |
| `BAH_LOG_LINES` | `100` | Lines of container logs to capture before revival |
| `BAH_NOTIFY` | `true` | Master switch for Slack notifications |
| `BAH_SLACK_WEBHOOK_URL` | *(required when `BAH_NOTIFY=true`)* | Slack Incoming Webhook URL |
| `BAH_SLACK_USERNAME` | `BetterAutoHeal` | Display name in Slack |
| `BAH_NOTIFY_COOLDOWN` | `30s` | Minimum gap between detection notifications for the same container (anti-spam) |
| `BAH_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `BAH_PROJECT_NAME` | *(empty)* | Friendly label prefixed onto every Slack header, e.g. `[Home Server] Unhealthy: …` |
| `BAH_LOOP_DETECTION` | `true` | Master switch for restart-loop detection |
| `BAH_LOOP_THRESHOLD` | `3` | Number of restarts within the window to trigger an alert |
| `BAH_LOOP_WINDOW` | `2m` | Time window over which restarts are counted |
| `BAH_LOOP_COOLDOWN` | `15m` | Minimum gap between loop alerts for the same container |

## Compose-mode requirements

For `mode=compose` to work, the BetterAutoHeal container needs:

1. Docker CLI **and** the compose plugin in its image (the provided Dockerfile uses `docker:26-cli`, which has both).
2. The Docker socket mounted: `/var/run/docker.sock:/var/run/docker.sock`.
3. A bind-mount of the **host** compose project working directory at the **same path** inside the BetterAutoHeal container. The compose plugin resolves the project from the `com.docker.compose.project.working_dir` label, so the path inside must match the path outside. The example compose file does this with `${PWD}:${PWD}:ro`.

If a container is set to `mode=compose` but is missing any of the `com.docker.compose.*` labels (i.e. it was not started by docker compose), revival fails and the failure is reported via Slack.

## Slack messages

Two messages per incident:

1. **Detection** — header, container name + short ID, mode, project/service (if any), timestamp, and a `tail`-style code block of the captured logs.
2. **Result** — success (`Revived`) or failure (`Revive failed` with the error).

Slack truncates very large messages, so the log block is capped at ~2500 characters with the **tail** preserved (the most diagnostic part).

## Build

```bash
go build ./...
go test ./...
docker build -t betterautoheal:dev .
```

## Publishing the image

Once you've smoke-tested locally and you want to ship the image to a registry so other hosts can pull it:

### Docker Hub

```bash
# 1. Log in once per machine.
docker login                                   # prompts for your Docker Hub username + PAT

# 2. Tag the image you built. Replace YOURUSER with your Docker Hub namespace.
docker build -t YOURUSER/betterautoheal:0.1.0 -t YOURUSER/betterautoheal:latest .

# 3. Push both tags.
docker push YOURUSER/betterautoheal:0.1.0
docker push YOURUSER/betterautoheal:latest
```

Then point any consumer at the registry image in their compose file:

```yaml
services:
  betterautoheal:
    image: YOURUSER/betterautoheal:0.1.0      # instead of `build: .`
    # ... same environment + volumes as the example
```

### GitHub Container Registry (`ghcr.io`)

```bash
# 1. Create a Personal Access Token with write:packages scope, then:
echo "$GHCR_TOKEN" | docker login ghcr.io -u YOURGHUSER --password-stdin

# 2. Tag with the ghcr.io namespace. Image names must be lowercase.
docker build -t ghcr.io/YOURGHUSER/betterautoheal:0.1.0 .

# 3. Push.
docker push ghcr.io/YOURGHUSER/betterautoheal:0.1.0
```

### Private / self-hosted registry

```bash
docker build -t registry.example.com/tools/betterautoheal:0.1.0 .
docker push registry.example.com/tools/betterautoheal:0.1.0
```

### Tagging conventions

- Pin a specific version (`:0.1.0`) in production compose files so upgrades are explicit.
- Keep a floating `:latest` if you want dev environments to follow `main`.
- For multi-arch images (e.g. building on amd64 to deploy on an arm64 server) use `docker buildx`:

  ```bash
  docker buildx build --platform linux/amd64,linux/arm64 \
    -t YOURUSER/betterautoheal:0.1.0 --push .
  ```

## License

MIT
