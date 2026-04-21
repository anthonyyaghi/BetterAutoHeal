package monitor

import (
	"sync"
	"time"
)

// loopHistory tracks restart activity for a single container so the detector
// can decide whether it's stuck in a crash loop.
type loopHistory struct {
	lastRestartCount int
	restartTimes     []time.Time
	lastNotify       time.Time
}

// loopTracker is a concurrency-safe collection of per-container histories.
type loopTracker struct {
	mu   sync.Mutex
	hist map[string]*loopHistory
}

func newLoopTracker() *loopTracker {
	return &loopTracker{hist: make(map[string]*loopHistory)}
}

// ObserveResult describes what Observe saw on a single poll for one container.
type ObserveResult struct {
	// Baseline is true on the first observation for a container ID: the current
	// RestartCount is stored as a reference point and never counts as a restart.
	Baseline bool
	// Delta is the number of restarts observed since the previous observation
	// (0 on baseline; can be > 1 if several restarts happened between polls).
	Delta int
	// WindowCount is the number of restart timestamps currently inside the
	// sliding window after any pruning.
	WindowCount int
	// Triggered is true when WindowCount has reached the threshold and the
	// per-container cooldown has elapsed; fires at most once per cooldown.
	Triggered bool
	// CooldownSuppressed is true when the threshold was met but the cooldown
	// is still active, so no trigger fired.
	CooldownSuppressed bool
}

// Observe records the current restart count for a container at time `now` and
// returns what it saw and whether the threshold fired.
func (t *loopTracker) Observe(id string, restartCount int, now time.Time, threshold int, window, cooldown time.Duration) ObserveResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	h, ok := t.hist[id]
	if !ok {
		t.hist[id] = &loopHistory{lastRestartCount: restartCount}
		return ObserveResult{Baseline: true}
	}

	delta := restartCount - h.lastRestartCount
	h.lastRestartCount = restartCount
	if delta < 0 {
		// Container recreated — Docker resets RestartCount. Clear the history.
		h.restartTimes = h.restartTimes[:0]
		delta = 0
	} else if delta > 0 {
		for i := 0; i < delta; i++ {
			h.restartTimes = append(h.restartTimes, now)
		}
	}

	// Prune entries outside the window.
	cutoff := now.Add(-window)
	pruned := h.restartTimes[:0]
	for _, ts := range h.restartTimes {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	h.restartTimes = pruned

	res := ObserveResult{
		Delta:       delta,
		WindowCount: len(h.restartTimes),
	}
	if res.WindowCount < threshold {
		return res
	}
	if !h.lastNotify.IsZero() && now.Sub(h.lastNotify) < cooldown {
		res.CooldownSuppressed = true
		return res
	}
	h.lastNotify = now
	res.Triggered = true
	return res
}

// Forget drops the tracked history for a container (useful on removal/rename).
func (t *loopTracker) Forget(id string) {
	t.mu.Lock()
	delete(t.hist, id)
	t.mu.Unlock()
}
