package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// Slack hard-limits a text block at 3000 chars; we leave headroom for code fences.
	maxLogChars = 2500
)

type Event struct {
	ContainerName    string
	ContainerID      string
	Mode             string
	Project          string
	Service          string
	ProjectName      string // optional friendly name (BAH_PROJECT_NAME)
	Logs             string // already truncated by caller if desired
	Outcome          string // "detected", "revived", "failed", "loop_detected", "loop_stopped", "loop_stop_failed"
	Err              error
	RestartsInWindow int           // only set for loop_* outcomes
	LoopWindow       time.Duration // only set for loop_* outcomes
	Timestamp        time.Time
}

type Slack struct {
	webhookURL string
	username   string
	httpClient *http.Client
}

func NewSlack(webhookURL, username string) *Slack {
	return &Slack{
		webhookURL: webhookURL,
		username:   username,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Notify posts a Block Kit message describing the event. Returns the HTTP error if any.
func (s *Slack) Notify(ctx context.Context, e Event) error {
	if s == nil || s.webhookURL == "" {
		return nil
	}
	payload := buildPayload(s.username, e)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func buildPayload(username string, e Event) map[string]any {
	icon := ":warning:"
	headerText := fmt.Sprintf("Unhealthy: %s", e.ContainerName)
	switch e.Outcome {
	case "revived":
		icon = ":white_check_mark:"
		headerText = fmt.Sprintf("Revived: %s", e.ContainerName)
	case "failed":
		icon = ":x:"
		headerText = fmt.Sprintf("Revive failed: %s", e.ContainerName)
	case "loop_detected":
		icon = ":recycle:"
		headerText = fmt.Sprintf("Restart loop: %s", e.ContainerName)
		if e.RestartsInWindow > 0 && e.LoopWindow > 0 {
			headerText = fmt.Sprintf("Restart loop: %s (%d× in %s)",
				e.ContainerName, e.RestartsInWindow, e.LoopWindow)
		}
	case "loop_stopped":
		icon = ":octagonal_sign:"
		headerText = fmt.Sprintf("Stopped looping container: %s", e.ContainerName)
	case "loop_stop_failed":
		icon = ":x:"
		headerText = fmt.Sprintf("Failed to stop looping container: %s", e.ContainerName)
	case "loop_recreated":
		icon = ":arrows_counterclockwise:"
		headerText = fmt.Sprintf("Recreated looping container: %s", e.ContainerName)
	case "loop_recreate_failed":
		icon = ":x:"
		headerText = fmt.Sprintf("Failed to recreate looping container: %s", e.ContainerName)
	}
	if e.ProjectName != "" {
		headerText = fmt.Sprintf("[%s] %s", e.ProjectName, headerText)
	}

	short := e.ContainerID
	if len(short) > 12 {
		short = short[:12]
	}

	fields := []map[string]any{
		{"type": "mrkdwn", "text": fmt.Sprintf("*Container*\n`%s`", short)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Mode*\n%s", e.Mode)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Time*\n%s", e.Timestamp.UTC().Format(time.RFC3339))},
	}
	if e.Project != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Project*\n%s", e.Project)})
	}
	if e.Service != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Service*\n%s", e.Service)})
	}

	blocks := []map[string]any{
		{"type": "header", "text": map[string]any{"type": "plain_text", "text": fmt.Sprintf("%s %s", icon, headerText)}},
		{"type": "section", "fields": fields},
	}

	if e.Err != nil {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Error*\n```%s```", truncate(e.Err.Error(), 500))},
		})
	}

	if e.Logs != "" {
		logs := truncate(e.Logs, maxLogChars)
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Logs (tail)*\n```%s```", logs)},
		})
	}

	return map[string]any{
		"username": username,
		"blocks":   blocks,
		// Plain-text fallback for clients that do not render blocks.
		"text": fmt.Sprintf("%s %s (mode=%s)", icon, headerText, e.Mode),
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Keep the tail — most useful for diagnosing the failure.
	const marker = "...[truncated]...\n"
	keep := max - len(marker)
	if keep < 0 {
		keep = 0
	}
	return marker + s[len(s)-keep:]
}
