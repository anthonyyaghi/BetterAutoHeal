package monitor

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"

	"github.com/anthonyyaghi/betterautoheal/internal/config"
	"github.com/anthonyyaghi/betterautoheal/internal/docker"
	"github.com/anthonyyaghi/betterautoheal/internal/notifier"
	"github.com/anthonyyaghi/betterautoheal/internal/reviver"
)

const (
	composeProjectLabel    = "com.docker.compose.project"
	composeServiceLabel    = "com.docker.compose.service"
	composeWorkingDirLabel = "com.docker.compose.project.working_dir"
)

type Monitor struct {
	cfg      config.Config
	docker   *docker.Client
	slack    *notifier.Slack
	restart  *reviver.Restart
	compose  *reviver.Compose
	log      *slog.Logger

	mu        sync.Mutex
	inFlight  map[string]struct{}
	lastNotif map[string]time.Time

	loops *loopTracker
}

func New(cfg config.Config, d *docker.Client, s *notifier.Slack, r *reviver.Restart, co *reviver.Compose, log *slog.Logger) *Monitor {
	return &Monitor{
		cfg:       cfg,
		docker:    d,
		slack:     s,
		restart:   r,
		compose:   co,
		log:       log,
		inFlight:  make(map[string]struct{}),
		lastNotif: make(map[string]time.Time),
		loops:     newLoopTracker(),
	}
}

// Run loops until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()

	m.log.Info("monitor started",
		"interval", m.cfg.Interval,
		"label_filter", m.cfg.LabelFilter,
		"default_mode", m.cfg.DefaultMode,
		"loop_detection", m.cfg.LoopDetection,
		"loop_threshold", m.cfg.LoopThreshold,
		"loop_window", m.cfg.LoopWindow,
		"loop_cooldown", m.cfg.LoopCooldown,
	)

	// Run a tick immediately so we don't wait the interval on startup.
	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			m.log.Info("monitor stopping")
			return ctx.Err()
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Monitor) tick(ctx context.Context) {
	key, val, ok := splitLabel(m.cfg.LabelFilter)
	if !ok {
		m.log.Error("invalid label filter", "value", m.cfg.LabelFilter)
		return
	}
	containers, err := m.docker.ListByLabel(ctx, key, val)
	if err != nil {
		m.log.Error("list containers failed", "err", err)
		return
	}

	seen := make(map[string]struct{}, len(containers))
	for _, c := range containers {
		c := c
		seen[c.ID] = struct{}{}
		if m.cfg.LoopDetection {
			m.checkLoop(ctx, c)
		}
		if m.shouldHandle(ctx, c) {
			go m.handle(ctx, c)
		}
	}
	// Drop history for containers that no longer exist (recreated / removed).
	m.loops.mu.Lock()
	for id := range m.loops.hist {
		if _, ok := seen[id]; !ok {
			delete(m.loops.hist, id)
		}
	}
	m.loops.mu.Unlock()
}

func (m *Monitor) checkLoop(ctx context.Context, c types.Container) {
	if config.ResolveLoopAction(c.Labels) == config.LoopActionIgnore {
		return
	}
	insp, err := m.docker.Inspect(ctx, c.ID)
	if err != nil {
		m.log.Warn("inspect for loop-check failed", "id", c.ID, "err", err)
		return
	}
	if insp.ContainerJSONBase == nil {
		return
	}
	name := containerDisplayName(c)
	shortID := c.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	res := m.loops.Observe(c.ID, insp.RestartCount, time.Now(),
		m.cfg.LoopThreshold, m.cfg.LoopWindow, m.cfg.LoopCooldown)

	switch {
	case res.Baseline:
		m.log.Info("loop tracker baseline",
			"container", name, "id", shortID,
			"restart_count", insp.RestartCount,
			"threshold", m.cfg.LoopThreshold,
			"window", m.cfg.LoopWindow,
		)
	case res.Delta > 0:
		m.log.Info("loop tracker increment",
			"container", name, "id", shortID,
			"delta", res.Delta,
			"count_in_window", res.WindowCount,
			"threshold", m.cfg.LoopThreshold,
		)
	}
	if res.CooldownSuppressed {
		m.log.Debug("loop trigger suppressed (cooldown)",
			"container", name, "count_in_window", res.WindowCount,
			"cooldown", m.cfg.LoopCooldown,
		)
	}
	if !res.Triggered {
		return
	}
	go m.handleLoop(ctx, c, res.WindowCount)
}

func (m *Monitor) handleLoop(ctx context.Context, c types.Container, windowCount int) {
	name := containerDisplayName(c)
	action := config.ResolveLoopAction(c.Labels)
	logLines := config.ResolveLogLines(c.Labels, m.cfg.LogLines)

	info := reviver.ContainerInfo{
		ID:                c.ID,
		Name:              name,
		Labels:            c.Labels,
		ComposeProject:    c.Labels[composeProjectLabel],
		ComposeService:    c.Labels[composeServiceLabel],
		ComposeWorkingDir: c.Labels[composeWorkingDirLabel],
	}

	// recreate requires compose labels; degrade gracefully when they're missing.
	effective := action
	if effective == config.LoopActionRecreate {
		if info.ComposeProject == "" || info.ComposeService == "" || info.ComposeWorkingDir == "" {
			m.log.Warn("recreate requires compose labels; falling back to notify-only",
				"container", name)
			effective = config.LoopActionNotify
		}
	}

	m.log.Warn("restart loop detected",
		"container", name, "id", c.ID,
		"restarts_in_window", windowCount, "window", m.cfg.LoopWindow,
		"action", action, "effective", effective,
	)

	logs, err := m.docker.TailLogs(ctx, c.ID, logLines)
	if err != nil {
		logs = "(log capture failed: " + err.Error() + ")"
	}

	m.notify(ctx, notifier.Event{
		ContainerName:    name,
		ContainerID:      c.ID,
		Mode:             "loop",
		Project:          info.ComposeProject,
		Service:          info.ComposeService,
		ProjectName:      m.cfg.ProjectName,
		Logs:             logs,
		Outcome:          "loop_detected",
		RestartsInWindow: windowCount,
		LoopWindow:       m.cfg.LoopWindow,
		Timestamp:        time.Now(),
	})

	switch effective {
	case config.LoopActionStop:
		if err := m.docker.Stop(ctx, c.ID, m.cfg.StopTimeout); err != nil {
			m.log.Error("stop after loop detection failed", "container", name, "err", err)
			m.notify(ctx, notifier.Event{
				ContainerName: name,
				ContainerID:   c.ID,
				Mode:          "loop",
				ProjectName:   m.cfg.ProjectName,
				Outcome:       "loop_stop_failed",
				Err:           err,
				Timestamp:     time.Now(),
			})
			return
		}
		m.log.Warn("stopped looping container", "container", name)
		m.notify(ctx, notifier.Event{
			ContainerName: name,
			ContainerID:   c.ID,
			Mode:          "loop",
			ProjectName:   m.cfg.ProjectName,
			Outcome:       "loop_stopped",
			Timestamp:     time.Now(),
		})
	case config.LoopActionRecreate:
		if err := m.compose.Recreate(ctx, info); err != nil {
			m.log.Error("recreate after loop detection failed", "container", name, "err", err)
			m.notify(ctx, notifier.Event{
				ContainerName: name,
				ContainerID:   c.ID,
				Mode:          "loop",
				Project:       info.ComposeProject,
				Service:       info.ComposeService,
				ProjectName:   m.cfg.ProjectName,
				Outcome:       "loop_recreate_failed",
				Err:           err,
				Timestamp:     time.Now(),
			})
			return
		}
		m.log.Warn("recreated looping container", "container", name)
		m.notify(ctx, notifier.Event{
			ContainerName: name,
			ContainerID:   c.ID,
			Mode:          "loop",
			Project:       info.ComposeProject,
			Service:       info.ComposeService,
			ProjectName:   m.cfg.ProjectName,
			Outcome:       "loop_recreated",
			Timestamp:     time.Now(),
		})
		// Old container is gone; drop its history so the fresh one gets a clean baseline.
		m.loops.Forget(c.ID)
	case config.LoopActionNotify:
		// Alert already sent above.
	}
}

func (m *Monitor) shouldHandle(ctx context.Context, c types.Container) bool {
	insp, err := m.docker.Inspect(ctx, c.ID)
	if err != nil {
		m.log.Warn("inspect failed", "id", c.ID, "err", err)
		return false
	}
	if insp.State == nil || !insp.State.Running {
		return false
	}
	if insp.State.Health == nil {
		return false
	}
	if insp.State.Health.Status != types.Unhealthy {
		return false
	}
	if m.cfg.StartPeriod > 0 && insp.State.StartedAt != "" {
		if started, err := time.Parse(time.RFC3339Nano, insp.State.StartedAt); err == nil {
			if time.Since(started) < m.cfg.StartPeriod {
				return false
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, busy := m.inFlight[c.ID]; busy {
		return false
	}
	m.inFlight[c.ID] = struct{}{}
	return true
}

func (m *Monitor) handle(ctx context.Context, c types.Container) {
	defer func() {
		m.mu.Lock()
		delete(m.inFlight, c.ID)
		m.mu.Unlock()
	}()

	name := containerDisplayName(c)
	mode := config.ResolveMode(c.Labels, m.cfg.DefaultMode)
	logLines := config.ResolveLogLines(c.Labels, m.cfg.LogLines)

	m.log.Info("unhealthy detected", "container", name, "id", c.ID, "mode", mode)

	logs, err := m.docker.TailLogs(ctx, c.ID, logLines)
	if err != nil {
		m.log.Warn("could not capture logs", "container", name, "err", err)
		logs = "(log capture failed: " + err.Error() + ")"
	}

	info := reviver.ContainerInfo{
		ID:                c.ID,
		Name:              name,
		Labels:            c.Labels,
		ComposeProject:    c.Labels[composeProjectLabel],
		ComposeService:    c.Labels[composeServiceLabel],
		ComposeWorkingDir: c.Labels[composeWorkingDirLabel],
	}

	rev, err := reviver.Dispatch(mode, m.restart, m.compose)
	if err != nil {
		m.log.Error("dispatch reviver", "err", err)
		return
	}

	now := time.Now()
	if m.shouldNotify(c.ID, now) {
		m.notify(ctx, notifier.Event{
			ContainerName: name,
			ContainerID:   c.ID,
			Mode:          string(mode),
			Project:       info.ComposeProject,
			Service:       info.ComposeService,
			ProjectName:   m.cfg.ProjectName,
			Logs:          logs,
			Outcome:       "detected",
			Timestamp:     now,
		})
	}

	reviveErr := rev.Revive(ctx, info)
	outcome := "revived"
	if reviveErr != nil {
		outcome = "failed"
		m.log.Error("revive failed", "container", name, "mode", mode, "err", reviveErr)
	} else {
		m.log.Info("revived", "container", name, "mode", mode)
	}

	m.notify(ctx, notifier.Event{
		ContainerName: name,
		ContainerID:   c.ID,
		Mode:          string(mode),
		Project:       info.ComposeProject,
		Service:       info.ComposeService,
		ProjectName:   m.cfg.ProjectName,
		Outcome:       outcome,
		Err:           reviveErr,
		Timestamp:     time.Now(),
	})
}

func (m *Monitor) shouldNotify(id string, now time.Time) bool {
	if m.cfg.NotifyCooldown <= 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.lastNotif[id]
	if ok && now.Sub(last) < m.cfg.NotifyCooldown {
		return false
	}
	m.lastNotif[id] = now
	return true
}

func (m *Monitor) notify(ctx context.Context, e notifier.Event) {
	if !m.cfg.NotifyEnabled || m.slack == nil {
		return
	}
	if err := m.slack.Notify(ctx, e); err != nil {
		m.log.Warn("slack notify failed", "container", e.ContainerName, "outcome", e.Outcome, "err", err)
	}
}

func containerDisplayName(c types.Container) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:12]
}

func splitLabel(s string) (key, value string, ok bool) {
	idx := strings.Index(s, "=")
	if idx < 0 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}
