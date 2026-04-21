package monitor

import (
	"testing"
	"time"
)

func TestLoopTracker_FirstObservationIsBaseline(t *testing.T) {
	tr := newLoopTracker()
	res := tr.Observe("c1", 50, time.Now(), 3, time.Minute, time.Minute)
	if !res.Baseline {
		t.Fatalf("expected Baseline=true on first observation, got %+v", res)
	}
	if res.Triggered || res.Delta != 0 || res.WindowCount != 0 {
		t.Fatalf("baseline result should have empty state, got %+v", res)
	}
}

func TestLoopTracker_FiresAfterThreshold(t *testing.T) {
	tr := newLoopTracker()
	now := time.Now()
	window := 2 * time.Minute
	cooldown := 10 * time.Minute

	tr.Observe("c1", 0, now, 3, window, cooldown) // baseline
	if res := tr.Observe("c1", 1, now.Add(10*time.Second), 3, window, cooldown); res.Triggered || res.WindowCount != 1 || res.Delta != 1 {
		t.Fatalf("unexpected: %+v", res)
	}
	if res := tr.Observe("c1", 2, now.Add(20*time.Second), 3, window, cooldown); res.Triggered || res.WindowCount != 2 {
		t.Fatalf("unexpected: %+v", res)
	}
	res := tr.Observe("c1", 3, now.Add(30*time.Second), 3, window, cooldown)
	if !res.Triggered || res.WindowCount != 3 {
		t.Fatalf("expected fire at threshold, got %+v", res)
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
	if res := tr.Observe("c1", 3, now.Add(30*time.Second), 3, window, cooldown); !res.Triggered {
		t.Fatalf("expected first fire, got %+v", res)
	}
	// Within cooldown — suppressed and flagged.
	if res := tr.Observe("c1", 4, now.Add(60*time.Second), 3, window, cooldown); res.Triggered || !res.CooldownSuppressed {
		t.Fatalf("expected cooldown suppression, got %+v", res)
	}
	// Past cooldown + fresh restarts → fires again.
	future := now.Add(10 * time.Minute)
	tr.Observe("c1", 5, future.Add(-60*time.Second), 3, window, cooldown)
	tr.Observe("c1", 6, future.Add(-30*time.Second), 3, window, cooldown)
	res := tr.Observe("c1", 7, future, 3, window, cooldown)
	if !res.Triggered {
		t.Fatalf("expected fire after cooldown, got %+v", res)
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
	// Jump outside window — prior restarts pruned.
	res := tr.Observe("c1", 3, now.Add(5*time.Minute), 3, window, cooldown)
	if res.Triggered || res.WindowCount != 1 {
		t.Fatalf("expected prune + count=1, got %+v", res)
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
	// Container recreated — RestartCount resets.
	res := tr.Observe("c1", 0, now.Add(30*time.Second), 3, window, cooldown)
	if res.Triggered || res.WindowCount != 0 || res.Delta != 0 {
		t.Fatalf("recreate should clear history, got %+v", res)
	}
}

func TestLoopTracker_Forget(t *testing.T) {
	tr := newLoopTracker()
	tr.Observe("c1", 5, time.Now(), 3, time.Minute, time.Minute)
	tr.Forget("c1")
	if res := tr.Observe("c1", 5, time.Now(), 3, time.Minute, time.Minute); !res.Baseline {
		t.Fatalf("post-forget observation should be a fresh baseline, got %+v", res)
	}
}

func TestLoopTracker_MultipleRestartsBetweenPolls(t *testing.T) {
	tr := newLoopTracker()
	now := time.Now()
	window := 2 * time.Minute
	cooldown := 10 * time.Minute

	tr.Observe("c1", 0, now, 3, window, cooldown) // baseline
	// Three restarts happened between polls; one Observe should catch them all.
	res := tr.Observe("c1", 3, now.Add(10*time.Second), 3, window, cooldown)
	if !res.Triggered || res.Delta != 3 || res.WindowCount != 3 {
		t.Fatalf("expected bulk delta to fire, got %+v", res)
	}
}
