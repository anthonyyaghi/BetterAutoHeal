package reviver

import (
	"context"
	"fmt"

	"github.com/anthonyyaghi/betterautoheal/internal/config"
)

// ContainerInfo carries everything a reviver needs without coupling to the docker types package.
type ContainerInfo struct {
	ID           string
	Name         string
	Labels       map[string]string
	ComposeProject    string
	ComposeService    string
	ComposeWorkingDir string
}

// Reviver brings an unhealthy container back to life.
type Reviver interface {
	Mode() config.Mode
	Revive(ctx context.Context, c ContainerInfo) error
}

// Dispatch returns the reviver matching the requested mode.
func Dispatch(mode config.Mode, restart *Restart, compose *Compose) (Reviver, error) {
	switch mode {
	case config.ModeRestart:
		return restart, nil
	case config.ModeCompose:
		return compose, nil
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
}
