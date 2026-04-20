package config

import (
	"testing"
)

func TestResolveMode(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		def    Mode
		want   Mode
	}{
		{"default when label missing", nil, ModeRestart, ModeRestart},
		{"label restart wins", map[string]string{LabelMode: "restart"}, ModeCompose, ModeRestart},
		{"label compose wins", map[string]string{LabelMode: "compose"}, ModeRestart, ModeCompose},
		{"unknown label falls through to default", map[string]string{LabelMode: "bogus"}, ModeCompose, ModeCompose},
		{"trims and lowercases", map[string]string{LabelMode: " Compose "}, ModeRestart, ModeCompose},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveMode(tt.labels, tt.def); got != tt.want {
				t.Fatalf("ResolveMode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLogLines(t *testing.T) {
	if got := ResolveLogLines(nil, 50); got != 50 {
		t.Fatalf("default not honored: got %d", got)
	}
	if got := ResolveLogLines(map[string]string{LabelLogLines: "200"}, 50); got != 200 {
		t.Fatalf("override not honored: got %d", got)
	}
	if got := ResolveLogLines(map[string]string{LabelLogLines: "not-a-number"}, 50); got != 50 {
		t.Fatalf("invalid value should fall back to default: got %d", got)
	}
	if got := ResolveLogLines(map[string]string{LabelLogLines: "0"}, 50); got != 50 {
		t.Fatalf("zero should fall back to default: got %d", got)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("BAH_NOTIFY", "false")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DefaultMode != ModeRestart {
		t.Fatalf("DefaultMode = %q, want restart", c.DefaultMode)
	}
	if c.LogLines != 100 {
		t.Fatalf("LogLines = %d, want 100", c.LogLines)
	}
}

func TestLoadRequiresWebhookWhenNotifying(t *testing.T) {
	t.Setenv("BAH_NOTIFY", "true")
	t.Setenv("BAH_SLACK_WEBHOOK_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when notify=true and webhook empty")
	}
}
