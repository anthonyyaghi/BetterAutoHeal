package monitor

import (
	"testing"
	"time"
)

func TestLoopTracker_FirstObservationDoesNotFire(t *testing.T) {
	tr := newLoopTracker()
	// First time we see a container, we just record baseline — even if restart count is high.
	fired, _ := tr.Observe("c1", 50, time.Now(), 3, time.Minute, time.Minute)
	if fired {
		t.Fatalf("first observation should never fire")
	}
}

func TestLoopTracker_FiresAfterThreshold(t *testing.T) {
	tr := newLoopTracker()
	now := time.Now()
	window := 2 * time.Minute
	cooldown := 10 * time.Minute

	// Baseline.
	tr.Observe("c1", 0, now, 3, window, cooldown)
	// +1 → count 1, no fire.
	if fired, count := tr.Observe("c1", 1, now.Add(10*time.Second), 3, window, cooldown); fired || count != 1 {
		t.Fatalf("unexpected: fired=%v count=%d", fired, count)
	}
	// +1 → count 2, no fire.
	if fired, count := tr.Observe("c1", 2, now.Add(20*time.Second), 3, window, cooldown); fired || count != 2 {
		t.Fatalf("unexpected: fired=%v count=%d", fired, count)
	}
	// +1 → count 3, fires.
	if fired, count := tr.Observe("c1", 3, now.Add(30*time.Second), 3, window, cooldown); !fired || count != 3 {
		t.Fatalf("expected fire at threshold, got fired=%v count=%d", fired, count)
	}
}

func TestLoopTracker_CooldownSuppresses(t *testing.T) {
	tr := newLoopTracker()
	now := time.Now()
	window := 2 * time.Minute
	cooldown := 5 * time.Minute
	tr.Observe("c1", 0, now, 3, window, cooldown)
	tr.Observe("c1", 1, now.Add(10*time.Second), 3, window, cooldown)
	tr.Observe("c1", 2, now.Add(20*time.Second), 3, window, cooldown)
	fired1, _ := tr.Observe("c1", 3, now.Add(30*time.Second), 3, window, cooldown)
	if !fired1 {
		t.Fatalf("expected first fire")
	}
	// Another restart within cooldown → suppressed.
	fired2, _ := tr.Observe("c1", 4, now.Add(60*time.Second), 3, window, cooldown)
	if fired2 {
		t.Fatalf("expected suppression during cooldown")
	}
	// Past cooldown → can fire again if threshold met.
	future := now.Add(10 * time.Minute)
	// Need threshold more restarts in window ending at `future`; bump count 3× within window.
	tr.Observe("c1", 5, future.Add(-60*time.Second), 3, window, cooldown)
	tr.Observe("c1", 6, future.Add(-30*time.Second), 3, window, cooldown)
	fired3, count := tr.Observe("c1", 7, future, 3, window, cooldown)
	if !fired3 {
		t.Fatalf("expected fire after cooldown, got fired=%v count=%d", fired3, count)
	}
}

func TestLoopTracker_WindowPrunes(t *testing.T) {
	tr := newLoopTracker()
	now := time.Now()
	window := 30 * time.Second
	cooldown := time.Hour
	tr.Observe("c1", 0, now, 3, window, cooldown)
	tr.Observe("c1", 1, now.Add(1*time.Second), 3, window, cooldown)
	tr.Observe("c1", 2, now.Add(2*time.Second), 3, window, cooldown)
	// Jump outside window — previous restarts should be pruned, so a new one alone doesn't fire.
	fired, count := tr.Observe("c1", 3, now.Add(5*time.Minute), 3, window, cooldown)
	if fired {
		t.Fatalf("should not fire when all prior restarts fell outside window")
	}
	if count != 1 {
		t.Fatalf("expected window count=1 after prune, got %d", count)
	}
}

func TestLoopTracker_RecreateResetsHistory(t *testing.T) {
	tr := newLoopTracker()
	now := time.Now()
	window := 2 * time.Minute
	cooldown := 10 * time.Minute
	tr.Observe("c1", 0, now, 3, window, cooldown)
	tr.Observe("c1", 1, now.Add(10*time.Second), 3, window, cooldown)
	tr.Observe("c1", 2, now.Add(20*time.Second), 3, window, cooldown)
	// Container recreated — RestartCount resets to 0 (or any value less than the previous).
	fired, count := tr.Observe("c1", 0, now.Add(30*time.Second), 3, window, cooldown)
	if fired || count != 0 {
		t.Fatalf("recreate should clear history, got fired=%v count=%d", fired, count)
	}
}

func TestLoopTracker_Forget(t *testing.T) {
	tr := newLoopTracker()
	tr.Observe("c1", 5, time.Now(), 3, time.Minute, time.Minute)
	tr.Forget("c1")
	// After forget, next observation is baseline again.
	fired, _ := tr.Observe("c1", 5, time.Now(), 3, time.Minute, time.Minute)
	if fired {
		t.Fatalf("post-forget observation should be a fresh baseline")
	}
}
