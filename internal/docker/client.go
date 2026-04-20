package docker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Client is a thin wrapper around the official docker client exposing only what BetterAutoHeal needs.
type Client struct {
	cli *client.Client
}

func New() (*Client, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{cli: c}, nil
}

func (c *Client) Close() error { return c.cli.Close() }

// Ping verifies connectivity to the daemon.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// ListByLabel returns all running containers carrying labelKey=labelValue.
func (c *Client) ListByLabel(ctx context.Context, labelKey, labelValue string) ([]types.Container, error) {
	args := filters.NewArgs()
	args.Add("label", fmt.Sprintf("%s=%s", labelKey, labelValue))
	return c.cli.ContainerList(ctx, container.ListOptions{Filters: args})
}

// Inspect returns full inspect data, including health status.
func (c *Client) Inspect(ctx context.Context, id string) (types.ContainerJSON, error) {
	return c.cli.ContainerInspect(ctx, id)
}

// Restart issues a docker restart with the given stop timeout (seconds).
func (c *Client) Restart(ctx context.Context, id string, stopTimeoutSec int) error {
	t := stopTimeoutSec
	return c.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &t})
}

// TailLogs returns the last `lines` lines of combined stdout+stderr for a container.
// It demultiplexes the docker stream and trims to the requested line count.
func (c *Client) TailLogs(ctx context.Context, id string, lines int) (string, error) {
	rc, err := c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", lines),
		Timestamps: true,
	})
	if err != nil {
		return "", fmt.Errorf("container logs: %w", err)
	}
	defer rc.Close()

	// Inspect to detect TTY; with TTY there is no multiplex framing.
	insp, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", fmt.Errorf("inspect for tty check: %w", err)
	}

	var stdout, stderr strings.Builder
	if insp.Config != nil && insp.Config.Tty {
		if _, err := io.Copy(&stdout, rc); err != nil {
			return "", fmt.Errorf("read tty logs: %w", err)
		}
	} else {
		if _, err := stdcopy.StdCopy(&stdout, &stderr, rc); err != nil {
			return "", fmt.Errorf("demux logs: %w", err)
		}
	}

	combined := stdout.String()
	if s := stderr.String(); s != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += s
	}
	return combined, nil
}
