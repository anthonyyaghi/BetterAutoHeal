# BetterAutoHeal

Docker container watchdog inspired by [`willfarrell/docker-autoheal`](https://github.com/willfarrell/docker-autoheal). Polls the Docker API for containers reporting an `unhealthy` healthcheck and revives them — with two extras over the original:

1. **Two revive modes**, selectable per container via label:
   - `restart` — `docker restart` (the original behavior)
   - `compose` — `docker compose stop <svc> && docker compose start <svc>` for cleaner lifecycles on compose-managed services
2. **Slack notifications** with a tail of the container's logs *captured before* the revival, so the failing state is preserved even after the container restarts.

## Quick start

```bash
export BAH_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/XXX/YYY/ZZZ
docker compose -f docker-compose.example.yml up --build
```

The example stack includes BetterAutoHeal plus a sample `nginx` service with a healthcheck and the opt-in label.

## Opting a container in

Add labels to any container you want monitored:

```yaml
labels:
  - betterautoheal.enable=true       # required: opt-in
  - betterautoheal.mode=compose      # optional: restart (default) or compose
  - betterautoheal.log_lines=200     # optional: override BAH_LOG_LINES
healthcheck:
  test: ["CMD", "curl", "-f", "http://localhost/health"]
  interval: 10s
  timeout: 3s
  retries: 3
```

Containers without `betterautoheal.enable=true` are ignored.

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

## License

MIT
