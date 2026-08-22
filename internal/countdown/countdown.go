// Package countdown owns the timer state machine.
package countdown

import (
	"sync"
	"time"

	"github.com/onlinealarmkur/timer-cli/internal/clock"
	"github.com/onlinealarmkur/timer-cli/internal/timerlimit"
)

// Snapshot is an immutable view of a countdown.
type Snapshot struct {
	Initial    time.Duration
	Total      time.Duration
	Remaining  time.Duration
	Elapsed    time.Duration
	Progress   float64
	Target     time.Time
	ObservedAt time.Time
	Paused     bool
	Finished   bool
}

// Countdown calculates remaining time from an absolute wall-clock target.
type Countdown struct {
	mu              sync.Mutex
	clock           clock.Clock
	initial         time.Duration
	total           time.Duration
	target          time.Time
	lastObserved    time.Time
	paused          bool
	pausedRemaining time.Duration
	finished        bool
	completionSent  bool
}

// New starts a countdown immediately.
func New(c clock.Clock, d time.Duration) *Countdown {
	now := wallTime(c.Now())
	return &Countdown{
		clock: c, initial: d, total: d, target: now.Add(d), lastObserved: now,
	}
}

// Tick returns current state and true once, on the transition to finished.
func (c *Countdown) Tick() (Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := c.snapshotLocked()
	if s.Remaining == 0 && !c.finished {
		c.finished = true
		c.paused = false
		s.Finished = true
	}
	completedNow := c.finished && !c.completionSent
	if completedNow {
		c.completionSent = true
	}
	return s, completedNow
}

// Snapshot returns current state without consuming the completion transition.
func (c *Countdown) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// TogglePause pauses or resumes the countdown.
func (c *Countdown) TogglePause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	now := c.observeLocked()
	if c.paused {
		c.target = now.Add(c.pausedRemaining)
		c.paused = false
		return
	}
	c.pausedRemaining = c.remainingLocked(now)
	c.paused = true
}

// Restart restores the original duration and starts it from now.
func (c *Countdown) Restart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := wallTime(c.clock.Now())
	c.target = now.Add(c.initial)
	c.lastObserved = now
	c.total = c.initial
	c.pausedRemaining = 0
	c.paused = false
	c.finished = false
	c.completionSent = false
}

// Add increases remaining time without exceeding the shared timer limit.
func (c *Countdown) Add(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 || c.finished || c.total <= 0 || c.total > timerlimit.MaxDuration ||
		d > timerlimit.MaxDuration-c.total {
		return false
	}
	now := c.observeLocked()
	if c.remainingLocked(now) == 0 {
		return false
	}
	c.total += d
	if c.paused {
		c.pausedRemaining += d
		return true
	}
	c.target = c.target.Add(d)
	return true
}

// Subtract decreases remaining time, clamped at zero.
func (c *Countdown) Subtract(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 || c.finished {
		return
	}
	now := c.observeLocked()
	remaining := c.remainingLocked(now)
	removed := min(d, remaining)
	c.total -= removed
	if removed == remaining {
		if c.paused {
			c.pausedRemaining = 0
		} else {
			c.target = now
		}
		return
	}
	if c.paused {
		c.pausedRemaining -= removed
	} else {
		c.target = c.target.Add(-removed)
	}
}

func (c *Countdown) snapshotLocked() Snapshot {
	observedAt := c.observeLocked()
	remaining := c.remainingLocked(observedAt)
	elapsed := c.total - remaining
	if elapsed < 0 {
		elapsed = 0
	}
	progress := 1.0
	if c.total > 0 {
		progress = float64(elapsed) / float64(c.total)
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	target := c.target
	if c.paused {
		target = observedAt.Add(remaining)
	}
	finished := c.finished || remaining == 0
	return Snapshot{
		Initial: c.initial, Total: c.total, Remaining: remaining, Elapsed: elapsed,
		Progress: progress, Target: target, ObservedAt: observedAt,
		Paused: c.paused && !finished, Finished: finished,
	}
}

func (c *Countdown) observeLocked() time.Time {
	now := wallTime(c.clock.Now())
	if !c.paused && !c.finished && now.Before(c.lastObserved) {
		c.target = c.target.Add(now.Sub(c.lastObserved))
	}
	c.lastObserved = now
	return now
}

func (c *Countdown) remainingLocked(now time.Time) time.Duration {
	if c.finished {
		return 0
	}
	if c.paused {
		return max(c.pausedRemaining, 0)
	}
	return max(c.target.Sub(now), 0)
}

// Round(0) strips Go's monotonic reading. Some monotonic clocks stop while a
// computer sleeps; the absolute wall target must still expire after wake.
func wallTime(t time.Time) time.Time { return t.Round(0) }
