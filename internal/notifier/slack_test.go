package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTruncateKeepsTail(t *testing.T) {
	in := strings.Repeat("a", 200) + "TAIL"
	got := truncate(in, 64)
	if !strings.HasSuffix(got, "TAIL") {
		t.Fatalf("expected tail preserved, got %q", got)
	}
	if len(got) > 64 {
		t.Fatalf("expected length <= 64, got %d", len(got))
	}
}

func TestTruncateNoOpUnderLimit(t *testing.T) {
	in := "short string"
	if got := truncate(in, 100); got != in {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestBuildPayloadIncludesLogsAndFields(t *testing.T) {
	e := Event{
		ContainerName: "web",
		ContainerID:   "abcdef1234567890",
		Mode:          "compose",
		Project:       "demo",
		Service:       "web",
		Logs:          "boom",
		Outcome:       "detected",
		Timestamp:     time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC),
	}
	p := buildPayload("BetterAutoHeal", e)
	b, _ := json.Marshal(p)
	s := string(b)
	for _, want := range []string{"web", "abcdef123456", "compose", "demo", "boom", "Unhealthy"} {
		if !strings.Contains(s, want) {
			t.Errorf("payload missing %q: %s", want, s)
		}
	}
}

func TestBuildPayloadIncludesProjectNamePrefix(t *testing.T) {
	e := Event{
		ContainerName: "web",
		ContainerID:   "abcdef1234567890",
		Mode:          "compose",
		ProjectName:   "Home Server",
		Outcome:       "detected",
		Timestamp:     time.Now(),
	}
	p := buildPayload("BetterAutoHeal", e)
	b, _ := json.Marshal(p)
	s := string(b)
	if !strings.Contains(s, "[Home Server] Unhealthy: web") {
		t.Fatalf("expected project name prefix in header, got %s", s)
	}
}

func TestNotifyPostsToWebhook(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSlack(srv.URL, "BAH")
	err := s.Notify(context.Background(), Event{
		ContainerName: "x", ContainerID: "abcdef1234567890",
		Mode: "restart", Outcome: "revived", Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !strings.Contains(got, "Revived") {
		t.Fatalf("expected payload to include Revived header, got %q", got)
	}
}

func TestNotifyReturnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	s := NewSlack(srv.URL, "BAH")
	err := s.Notify(context.Background(), Event{ContainerName: "x", ContainerID: "y", Mode: "restart", Outcome: "detected", Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}
