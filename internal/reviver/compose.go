package reviver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/anthonyyaghi/betterautoheal/internal/config"
)

// Compose revives by running `docker compose stop <svc>` then `docker compose start <svc>`
// in the container's compose project working directory. It requires the docker CLI + compose
// plugin to be present in the BetterAutoHeal container, plus the project working dir
// bind-mounted at the same path so the compose plugin can find compose.yaml.
type Compose struct {
	bin string // path to `docker` binary
}

func NewCompose() *Compose {
	bin := "docker"
	if p, err := exec.LookPath("docker"); err == nil {
		bin = p
	}
	return &Compose{bin: bin}
}

func (c *Compose) Mode() config.Mode { return config.ModeCompose }

func (c *Compose) Revive(ctx context.Context, info ContainerInfo) error {
	if info.ComposeProject == "" || info.ComposeService == "" {
		return fmt.Errorf("missing compose labels (project=%q service=%q); cannot use compose mode",
			info.ComposeProject, info.ComposeService)
	}
	if info.ComposeWorkingDir == "" {
		return fmt.Errorf("missing com.docker.compose.project.working_dir label; cannot locate compose file")
	}

	if err := c.run(ctx, info, "stop", info.ComposeService); err != nil {
		return fmt.Errorf("compose stop: %w", err)
	}
	if err := c.run(ctx, info, "start", info.ComposeService); err != nil {
		return fmt.Errorf("compose start: %w", err)
	}
	return nil
}

func (c *Compose) run(ctx context.Context, info ContainerInfo, args ...string) error {
	full := append([]string{"compose", "-p", info.ComposeProject}, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	cmd.Dir = info.ComposeWorkingDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w (stderr: %s)", c.bin, full, err, stderr.String())
	}
	return nil
}
