package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/anthonyyaghi/betterautoheal/internal/config"
	"github.com/anthonyyaghi/betterautoheal/internal/docker"
	"github.com/anthonyyaghi/betterautoheal/internal/monitor"
	"github.com/anthonyyaghi/betterautoheal/internal/notifier"
	"github.com/anthonyyaghi/betterautoheal/internal/reviver"
)

func main() {
	cfg, err := config.Load()
	logger := newLogger(cfg.LogLevel)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(2)
	}

	d, err := docker.New()
	if err != nil {
		logger.Error("docker client", "err", err)
		os.Exit(1)
	}
	defer d.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := d.Ping(ctx); err != nil {
		logger.Error("docker ping", "err", err)
		os.Exit(1)
	}

	var slack *notifier.Slack
	if cfg.NotifyEnabled {
		slack = notifier.NewSlack(cfg.SlackWebhookURL, cfg.SlackUsername)
	}

	mon := monitor.New(
		cfg,
		d,
		slack,
		reviver.NewRestart(d, cfg.StopTimeout),
		reviver.NewCompose(),
		logger,
	)

	if err := mon.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("monitor exited", "err", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
