package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Mode string

const (
	ModeRestart Mode = "restart"
	ModeCompose Mode = "compose"
)

type LoopAction string

const (
	LoopActionNotify   LoopAction = "notify"
	LoopActionStop     LoopAction = "stop"
	LoopActionRecreate LoopAction = "recreate"
	LoopActionIgnore   LoopAction = "ignore"
)

const (
	LabelEnable     = "betterautoheal.enable"
	LabelMode       = "betterautoheal.mode"
	LabelLogLines   = "betterautoheal.log_lines"
	LabelLoopAction = "betterautoheal.loop_action"
)

type Config struct {
	Interval        time.Duration
	StartPeriod     time.Duration
	StopTimeout     int
	DefaultMode     Mode
	LabelFilter     string
	LogLines        int
	SlackWebhookURL string
	SlackUsername   string
	NotifyEnabled   bool
	LogLevel        string
	NotifyCooldown  time.Duration
	ProjectName     string

	LoopDetection bool
	LoopThreshold int
	LoopWindow    time.Duration
	LoopCooldown  time.Duration
}

func Load() (Config, error) {
	c := Config{
		Interval:        durationEnv("BAH_INTERVAL", 5*time.Second),
		StartPeriod:     durationEnv("BAH_START_PERIOD", 0),
		StopTimeout:     intEnv("BAH_STOP_TIMEOUT", 10),
		DefaultMode:     Mode(stringEnv("BAH_DEFAULT_MODE", string(ModeRestart))),
		LabelFilter:     stringEnv("BAH_LABEL_FILTER", LabelEnable+"=true"),
		LogLines:        intEnv("BAH_LOG_LINES", 100),
		SlackWebhookURL: os.Getenv("BAH_SLACK_WEBHOOK_URL"),
		SlackUsername:   stringEnv("BAH_SLACK_USERNAME", "BetterAutoHeal"),
		NotifyEnabled:   boolEnv("BAH_NOTIFY", true),
		LogLevel:        stringEnv("BAH_LOG_LEVEL", "info"),
		NotifyCooldown:  durationEnv("BAH_NOTIFY_COOLDOWN", 30*time.Second),
		ProjectName:     strings.TrimSpace(os.Getenv("BAH_PROJECT_NAME")),

		LoopDetection: boolEnv("BAH_LOOP_DETECTION", true),
		LoopThreshold: intEnv("BAH_LOOP_THRESHOLD", 3),
		LoopWindow:    durationEnv("BAH_LOOP_WINDOW", 2*time.Minute),
		LoopCooldown:  durationEnv("BAH_LOOP_COOLDOWN", 15*time.Minute),
	}
	if c.DefaultMode != ModeRestart && c.DefaultMode != ModeCompose {
		return c, fmt.Errorf("BAH_DEFAULT_MODE must be %q or %q, got %q", ModeRestart, ModeCompose, c.DefaultMode)
	}
	if c.NotifyEnabled && c.SlackWebhookURL == "" {
		return c, fmt.Errorf("BAH_NOTIFY is true but BAH_SLACK_WEBHOOK_URL is empty")
	}
	if !strings.Contains(c.LabelFilter, "=") {
		return c, fmt.Errorf("BAH_LABEL_FILTER must be of form key=value, got %q", c.LabelFilter)
	}
	if c.LoopThreshold < 2 {
		return c, fmt.Errorf("BAH_LOOP_THRESHOLD must be >= 2, got %d", c.LoopThreshold)
	}
	if c.LoopWindow <= 0 {
		return c, fmt.Errorf("BAH_LOOP_WINDOW must be positive, got %s", c.LoopWindow)
	}
	return c, nil
}

func stringEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func boolEnv(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func durationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// ResolveMode returns the revive mode for a container given its labels and the global default.
func ResolveMode(labels map[string]string, def Mode) Mode {
	if v, ok := labels[LabelMode]; ok {
		m := Mode(strings.ToLower(strings.TrimSpace(v)))
		if m == ModeRestart || m == ModeCompose {
			return m
		}
	}
	return def
}

// ResolveLogLines returns the per-container log line override or the global default.
func ResolveLogLines(labels map[string]string, def int) int {
	if v, ok := labels[LabelLogLines]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// ResolveLoopAction returns the container's loop_action label or the default (recreate).
// Containers without compose labels will have recreate downgraded to notify at handle time.
func ResolveLoopAction(labels map[string]string) LoopAction {
	if v, ok := labels[LabelLoopAction]; ok {
		a := LoopAction(strings.ToLower(strings.TrimSpace(v)))
		switch a {
		case LoopActionNotify, LoopActionStop, LoopActionRecreate, LoopActionIgnore:
			return a
		}
	}
	return LoopActionRecreate
}
