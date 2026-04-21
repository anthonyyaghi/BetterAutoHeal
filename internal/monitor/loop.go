package monitor

import (
	"sync"
	"time"
)

// loopHistory tracks restart activity for a single container so the detector
// can decide whether it's stuck in a crash loop.
type loopHistory struct {
	lastRestartCount int
	seeded           bool // true after the first observation — first delta is discarded
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

// Observe records the current restart count for a container at time `now` and
// returns (triggered, windowCount) indicating whether the loop threshold was
// crossed. A trigger is suppressed until the cooldown elapses.
func (t *loopTracker) Observe(id string, restartCount int, now time.Time, threshold int, window, cooldown time.Duration) (triggered bool, windowCount int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	h, ok := t.hist[id]
	if !ok {
		h = &loopHistory{lastRestartCount: restartCount, seeded: true}
		t.hist[id] = h
		return false, 0
	}
	if !h.seeded {
		h.lastRestartCount = restartCount
		h.seeded = true
		return false, 0
	}

	delta := restartCount - h.lastRestartCount
	h.lastRestartCount = restartCount
	if delta < 0 {
		// Container recreated — Docker resets RestartCount. Clear the history.
		h.restartTimes = h.restartTimes[:0]
	} else {
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

	windowCount = len(h.restartTimes)
	if windowCount < threshold {
		return false, windowCount
	}
	if !h.lastNotify.IsZero() && now.Sub(h.lastNotify) < cooldown {
		return false, windowCount
	}
	h.lastNotify = now
	return true, windowCount
}

// Forget drops the tracked history for a container (useful on removal/rename).
func (t *loopTracker) Forget(id string) {
	t.mu.Lock()
	delete(t.hist, id)
	t.mu.Unlock()
}
