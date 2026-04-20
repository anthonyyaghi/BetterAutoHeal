package reviver

import (
	"context"

	"github.com/anthonyyaghi/betterautoheal/internal/config"
	"github.com/anthonyyaghi/betterautoheal/internal/docker"
)

type Restart struct {
	docker      *docker.Client
	stopTimeout int
}

func NewRestart(d *docker.Client, stopTimeoutSec int) *Restart {
	return &Restart{docker: d, stopTimeout: stopTimeoutSec}
}

func (r *Restart) Mode() config.Mode { return config.ModeRestart }

func (r *Restart) Revive(ctx context.Context, c ContainerInfo) error {
	return r.docker.Restart(ctx, c.ID, r.stopTimeout)
}
