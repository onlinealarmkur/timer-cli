package countdown

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/onlinealarmkur/timer-cli/internal/timerlimit"
)

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	reads int
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *fakeClock) Reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)}
}

type snapshotWant struct {
	initial    time.Duration
	total      time.Duration
	remaining  time.Duration
	elapsed    time.Duration
	progress   float64
	target     time.Time
	observedAt time.Time
	paused     bool
	finished   bool
}

type countdownState struct {
	initial         time.Duration
	total           time.Duration
	target          time.Time
	lastObserved    time.Time
	paused          bool
	pausedRemaining time.Duration
	finished        bool
	completionSent  bool
}

func captureCountdownState(c *Countdown) countdownState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return countdownState{
		initial: c.initial, total: c.total, target: c.target, lastObserved: c.lastObserved,
		paused: c.paused, pausedRemaining: c.pausedRemaining,
		finished: c.finished, completionSent: c.completionSent,
	}
}

func assertAddRejectedWithoutMutation(t *testing.T, timer *Countdown, clock *fakeClock, d time.Duration) {
	t.Helper()
	wantState := captureCountdownState(timer)
	wantReads := clock.Reads()
	if timer.Add(d) {
		t.Fatalf("Add(%v) succeeded, want rejection", d)
	}
	if got := captureCountdownState(timer); got != wantState {
		t.Fatalf("Add(%v) mutated state = %+v, want %+v", d, got, wantState)
	}
	if got := clock.Reads(); got != wantReads {
		t.Fatalf("Add(%v) clock reads = %d, want unchanged %d", d, got, wantReads)
	}
}

func assertSnapshot(t *testing.T, got Snapshot, want snapshotWant) {
	t.Helper()
	assertSnapshotInvariants(t, got)
	if got.Initial != want.initial || got.Total != want.total ||
		got.Remaining != want.remaining || got.Elapsed != want.elapsed ||
		math.Abs(got.Progress-want.progress) > 1e-12 ||
		!got.Target.Equal(want.target) || !got.ObservedAt.Equal(want.observedAt) ||
		got.Paused != want.paused || got.Finished != want.finished {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
}

func assertSnapshotInvariants(t *testing.T, got Snapshot) {
	t.Helper()
	if got.Total < 0 || got.Remaining < 0 || got.Elapsed < 0 {
		t.Fatalf("snapshot contains a negative duration: %+v", got)
	}
	if got.Remaining > got.Total || got.Elapsed > got.Total || got.Elapsed+got.Remaining != got.Total {
		t.Fatalf("snapshot durations are incoherent: %+v", got)
	}
	if got.Progress < 0 || got.Progress > 1 {
		t.Fatalf("snapshot progress is outside [0,1]: %+v", got)
	}
	if !got.Finished && got.Target.Sub(got.ObservedAt) != got.Remaining {
		t.Fatalf("snapshot target does not match remaining duration: %+v", got)
	}
}

func TestBackwardClockStepPreservesRunningState(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	clock.Advance(10 * time.Second)
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 50 * time.Second,
		elapsed: 10 * time.Second, progress: 1.0 / 6.0,
		target: startedAt.Add(time.Minute), observedAt: startedAt.Add(10 * time.Second),
	})

	clock.Advance(-2 * time.Minute)
	before := clock.Reads()
	snapshot := timer.Snapshot()
	if got := clock.Reads() - before; got != 1 {
		t.Fatalf("rollback Snapshot clock reads = %d, want 1", got)
	}
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 50 * time.Second,
		elapsed: 10 * time.Second, progress: 1.0 / 6.0,
		target: startedAt.Add(-time.Minute), observedAt: startedAt.Add(-110 * time.Second),
	})
}

func TestBackwardClockStepThenSubtractCompletesCoherently(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	clock.Advance(10 * time.Second)
	timer.Snapshot()
	clock.Advance(-2 * time.Minute)
	timer.Snapshot()

	before := clock.Reads()
	timer.Subtract(time.Minute)
	if got := clock.Reads() - before; got != 1 {
		t.Fatalf("rollback Subtract clock reads = %d, want 1", got)
	}
	want := snapshotWant{
		initial: time.Minute, total: 10 * time.Second, elapsed: 10 * time.Second,
		progress: 1, target: startedAt.Add(-110 * time.Second),
		observedAt: startedAt.Add(-110 * time.Second), finished: true,
	}
	assertSnapshot(t, timer.Snapshot(), want)
	snapshot, completed := timer.Tick()
	if !completed {
		t.Fatal("subtraction after rollback did not complete on the next tick")
	}
	assertSnapshot(t, snapshot, want)
	if _, completedAgain := timer.Tick(); completedAgain {
		t.Fatal("completion reported more than once")
	}
}

func TestBackwardClockStepWhilePausedPreservesState(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	clock.Advance(10 * time.Second)
	timer.TogglePause()
	clock.Advance(-2 * time.Minute)

	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 50 * time.Second,
		elapsed: 10 * time.Second, progress: 1.0 / 6.0,
		target: startedAt.Add(-time.Minute), observedAt: startedAt.Add(-110 * time.Second),
		paused: true,
	})
	timer.TogglePause()
	clock.Advance(5 * time.Second)
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 45 * time.Second,
		elapsed: 15 * time.Second, progress: 0.25,
		target: startedAt.Add(-time.Minute), observedAt: startedAt.Add(-105 * time.Second),
	})
}

func TestDelayedTickAndSleepRecovery(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	clock.Advance(45 * time.Second) // no intermediate scheduler ticks
	snapshot, completed := timer.Tick()
	if completed {
		t.Fatal("countdown completed before its target")
	}
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 15 * time.Second,
		elapsed: 45 * time.Second, progress: 0.75, target: startedAt.Add(time.Minute),
		observedAt: startedAt.Add(45 * time.Second),
	})

	clock.Advance(time.Hour) // simulate sleep/wake past the wall-clock target
	snapshot, completed = timer.Tick()
	if !completed {
		t.Fatal("countdown did not complete after sleep")
	}
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, elapsed: time.Minute, progress: 1,
		target: startedAt.Add(time.Minute), observedAt: startedAt.Add(time.Hour + 45*time.Second),
		finished: true,
	})
	if _, completedAgain := timer.Tick(); completedAgain {
		t.Fatal("completion reported more than once")
	}
}

func TestSnapshotsStripMonotonicReadings(t *testing.T) {
	t.Parallel()
	fixture := time.Now()
	if fixture == fixture.Round(0) {
		t.Fatal("time.Now() fixture lacks a monotonic reading")
	}

	clock := &fakeClock{now: fixture}
	timer := New(clock, time.Minute)
	assertStripped := func(label string, snapshot Snapshot) {
		t.Helper()
		if snapshot.ObservedAt != snapshot.ObservedAt.Round(0) {
			t.Fatalf("%s ObservedAt retained a monotonic reading: %v", label, snapshot.ObservedAt)
		}
		if snapshot.Target != snapshot.Target.Round(0) {
			t.Fatalf("%s Target retained a monotonic reading: %v", label, snapshot.Target)
		}
	}

	assertStripped("initial snapshot", timer.Snapshot())
	clock.Advance(time.Second)
	assertStripped("advanced snapshot", timer.Snapshot())
}

func TestPauseResumeAndRestartSnapshots(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	clock.Advance(10 * time.Second)
	timer.TogglePause()
	clock.Advance(time.Hour)
	snapshot := timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 50 * time.Second,
		elapsed: 10 * time.Second, progress: 1.0 / 6.0,
		target: startedAt.Add(time.Hour + time.Minute), observedAt: startedAt.Add(time.Hour + 10*time.Second),
		paused: true,
	})

	clock.Advance(5 * time.Second)
	snapshot = timer.Snapshot()
	if want := startedAt.Add(time.Hour + time.Minute + 5*time.Second); !snapshot.Target.Equal(want) {
		t.Fatalf("sliding paused target = %v, want %v", snapshot.Target, want)
	}
	if snapshot.Remaining != 50*time.Second || snapshot.Elapsed != 10*time.Second {
		t.Fatalf("paused timing changed: %+v", snapshot)
	}

	timer.TogglePause()
	clock.Advance(5 * time.Second)
	snapshot = timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: 45 * time.Second,
		elapsed: 15 * time.Second, progress: 0.25,
		target:     startedAt.Add(time.Hour + time.Minute + 5*time.Second),
		observedAt: startedAt.Add(time.Hour + 20*time.Second),
	})

	timer.Restart()
	snapshot = timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: time.Minute,
		target:     startedAt.Add(time.Hour + 80*time.Second),
		observedAt: startedAt.Add(time.Hour + 20*time.Second),
	})
}

func TestRestartResetsCompletionDelivery(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Second)
	clock.Advance(time.Second)
	if _, completed := timer.Tick(); !completed {
		t.Fatal("first run did not complete")
	}
	timer.Restart()
	snapshot := timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Second, total: time.Second, remaining: time.Second,
		target: startedAt.Add(2 * time.Second), observedAt: startedAt.Add(time.Second),
	})
	clock.Advance(time.Second)
	if snapshot, completed := timer.Tick(); !completed || !snapshot.Finished {
		t.Fatalf("restarted run snapshot=%+v completed=%v", snapshot, completed)
	}
}

func TestZeroDurationCompletesImmediately(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, 0)
	snapshot, completed := timer.Tick()
	if !completed {
		t.Fatal("zero-duration countdown did not complete immediately")
	}
	assertSnapshot(t, snapshot, snapshotWant{
		progress: 1, target: startedAt, observedAt: startedAt, finished: true,
	})
}

func TestInvalidAndFinishedMutationsAreNoOps(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	before := clock.Reads()
	assertAddRejectedWithoutMutation(t, timer, clock, 0)
	assertAddRejectedWithoutMutation(t, timer, clock, -time.Second)
	timer.Subtract(0)
	timer.Subtract(-time.Second)
	if got := clock.Reads() - before; got != 0 {
		t.Fatalf("invalid mutations read clock %d times, want 0", got)
	}
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: time.Minute, total: time.Minute, remaining: time.Minute,
		target: startedAt.Add(time.Minute), observedAt: startedAt,
	})

	clock.Advance(time.Minute)
	if _, completed := timer.Tick(); !completed {
		t.Fatal("countdown did not complete")
	}
	before = clock.Reads()
	timer.TogglePause()
	assertAddRejectedWithoutMutation(t, timer, clock, time.Minute)
	timer.Subtract(time.Minute)
	if got := clock.Reads() - before; got != 0 {
		t.Fatalf("finished mutations read clock %d times, want 0", got)
	}
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: time.Minute, total: time.Minute, elapsed: time.Minute, progress: 1,
		target: startedAt.Add(time.Minute), observedAt: startedAt.Add(time.Minute), finished: true,
	})
}

func TestExpiredBeforeTickRejectsAddAndStillCompletesOnce(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, time.Minute)
	clock.Advance(time.Minute)

	expired := timer.Snapshot()
	assertSnapshot(t, expired, snapshotWant{
		initial: time.Minute, total: time.Minute, elapsed: time.Minute, progress: 1,
		target: startedAt.Add(time.Minute), observedAt: startedAt.Add(time.Minute), finished: true,
	})
	wantState := captureCountdownState(timer)
	beforeReads := clock.Reads()
	if timer.Add(time.Minute) {
		t.Fatal("Add revived an expired countdown before Tick consumed completion")
	}
	if got := clock.Reads() - beforeReads; got != 1 {
		t.Fatalf("expired Add clock reads = %d, want 1", got)
	}
	if got := captureCountdownState(timer); got != wantState {
		t.Fatalf("expired Add mutated state = %+v, want %+v", got, wantState)
	}

	snapshot, completed := timer.Tick()
	if !completed {
		t.Fatal("expired countdown did not complete after rejected addition")
	}
	assertSnapshot(t, snapshot, snapshotWant{
		initial: time.Minute, total: time.Minute, elapsed: time.Minute, progress: 1,
		target: startedAt.Add(time.Minute), observedAt: startedAt.Add(time.Minute), finished: true,
	})
	if _, completedAgain := timer.Tick(); completedAgain {
		t.Fatal("completion reported more than once")
	}
}

func TestRunningAddEnforcesMaximumAtomically(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, timerlimit.MaxDuration-time.Minute)
	clock.Advance(10 * time.Second)
	before := clock.Reads()
	if !timer.Add(time.Minute) {
		t.Fatal("addition to the exact maximum was rejected")
	}
	if got := clock.Reads() - before; got != 1 {
		t.Fatalf("successful Add clock reads = %d, want 1", got)
	}
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: timerlimit.MaxDuration - time.Minute,
		total:   timerlimit.MaxDuration, remaining: timerlimit.MaxDuration - 10*time.Second,
		elapsed: 10 * time.Second, progress: float64(10*time.Second) / float64(timerlimit.MaxDuration),
		target: startedAt.Add(timerlimit.MaxDuration), observedAt: startedAt.Add(10 * time.Second),
	})

	clock.Advance(time.Second)
	assertAddRejectedWithoutMutation(t, timer, clock, time.Minute)
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: timerlimit.MaxDuration - time.Minute,
		total:   timerlimit.MaxDuration, remaining: timerlimit.MaxDuration - 11*time.Second,
		elapsed: 11 * time.Second, progress: float64(11*time.Second) / float64(timerlimit.MaxDuration),
		target: startedAt.Add(timerlimit.MaxDuration), observedAt: startedAt.Add(11 * time.Second),
	})
}

func TestAddRejectsOversizedOverflowAndInvalidTotalsWithoutMutation(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	timer := New(clock, time.Minute)
	assertAddRejectedWithoutMutation(t, timer, clock, timerlimit.MaxDuration)
	assertAddRejectedWithoutMutation(t, timer, clock, time.Duration(math.MaxInt64))

	invalidHighClock := newFakeClock()
	invalidHigh := New(invalidHighClock, timerlimit.MaxDuration+time.Second)
	assertAddRejectedWithoutMutation(t, invalidHigh, invalidHighClock, time.Second)

	invalidZeroClock := newFakeClock()
	invalidZero := New(invalidZeroClock, 0)
	assertAddRejectedWithoutMutation(t, invalidZero, invalidZeroClock, time.Second)

	invalidNegativeClock := newFakeClock()
	invalidNegative := New(invalidNegativeClock, -time.Second)
	assertAddRejectedWithoutMutation(t, invalidNegative, invalidNegativeClock, time.Second)
}

func TestPausedAddEnforcesMaximumAtomically(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, timerlimit.MaxDuration-time.Minute)
	timer.TogglePause()
	if !timer.Add(time.Minute) {
		t.Fatal("paused addition to the exact maximum was rejected")
	}
	clock.Advance(time.Hour)
	assertAddRejectedWithoutMutation(t, timer, clock, time.Minute)
	assertAddRejectedWithoutMutation(t, timer, clock, time.Duration(math.MaxInt64))
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: timerlimit.MaxDuration - time.Minute,
		total:   timerlimit.MaxDuration, remaining: timerlimit.MaxDuration,
		target:     startedAt.Add(time.Hour + timerlimit.MaxDuration),
		observedAt: startedAt.Add(time.Hour), paused: true,
	})
}

func TestSubtractThenReAddMayReturnToMaximum(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, timerlimit.MaxDuration)
	timer.Subtract(time.Minute)
	if !timer.Add(time.Minute) {
		t.Fatal("re-addition after subtraction was rejected")
	}
	assertSnapshot(t, timer.Snapshot(), snapshotWant{
		initial: timerlimit.MaxDuration, total: timerlimit.MaxDuration,
		remaining: timerlimit.MaxDuration, target: startedAt.Add(timerlimit.MaxDuration),
		observedAt: startedAt,
	})
}

func TestRunningAddAndSubtractPreserveElapsed(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, 30*time.Second)
	clock.Advance(10 * time.Second)
	timer.Add(time.Minute)
	snapshot := timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: 30 * time.Second, total: 90 * time.Second, remaining: 80 * time.Second,
		elapsed: 10 * time.Second, progress: 10.0 / 90.0,
		target: startedAt.Add(90 * time.Second), observedAt: startedAt.Add(10 * time.Second),
	})

	timer.Subtract(time.Minute)
	snapshot = timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: 30 * time.Second, total: 30 * time.Second, remaining: 20 * time.Second,
		elapsed: 10 * time.Second, progress: 1.0 / 3.0,
		target: startedAt.Add(30 * time.Second), observedAt: startedAt.Add(10 * time.Second),
	})
}

func TestPausedAddAndSubtractPreserveElapsed(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, 2*time.Minute)
	clock.Advance(20 * time.Second)
	timer.TogglePause()
	timer.Add(time.Minute)
	snapshot := timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: 2 * time.Minute, total: 3 * time.Minute, remaining: 160 * time.Second,
		elapsed: 20 * time.Second, progress: 1.0 / 9.0,
		target: startedAt.Add(3 * time.Minute), observedAt: startedAt.Add(20 * time.Second),
		paused: true,
	})

	clock.Advance(time.Hour)
	timer.Subtract(time.Minute)
	snapshot = timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: 2 * time.Minute, total: 2 * time.Minute, remaining: 100 * time.Second,
		elapsed: 20 * time.Second, progress: 1.0 / 6.0,
		target:     startedAt.Add(time.Hour + 2*time.Minute),
		observedAt: startedAt.Add(time.Hour + 20*time.Second), paused: true,
	})
}

func TestSubtractToZeroKeepsTotalAtElapsedAndCompletesOnce(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	startedAt := clock.now
	timer := New(clock, 30*time.Second)
	clock.Advance(10 * time.Second)
	timer.TogglePause()
	timer.Subtract(time.Minute)
	snapshot := timer.Snapshot()
	assertSnapshot(t, snapshot, snapshotWant{
		initial: 30 * time.Second, total: 10 * time.Second, elapsed: 10 * time.Second,
		progress: 1, target: startedAt.Add(10 * time.Second),
		observedAt: startedAt.Add(10 * time.Second), finished: true,
	})

	snapshot, completed := timer.Tick()
	if !completed {
		t.Fatal("subtraction to zero did not complete on the next tick")
	}
	assertSnapshot(t, snapshot, snapshotWant{
		initial: 30 * time.Second, total: 10 * time.Second, elapsed: 10 * time.Second,
		progress: 1, target: startedAt.Add(10 * time.Second),
		observedAt: startedAt.Add(10 * time.Second), finished: true,
	})
	if snapshot.Total < snapshot.Elapsed {
		t.Fatalf("total %v fell below elapsed %v", snapshot.Total, snapshot.Elapsed)
	}
	if _, completedAgain := timer.Tick(); completedAgain {
		t.Fatal("completion reported more than once")
	}
}

func TestSnapshotReadsClockOnce(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	timer := New(clock, time.Minute)
	before := clock.Reads()
	timer.Snapshot()
	if got := clock.Reads() - before; got != 1 {
		t.Fatalf("Snapshot clock reads = %d, want 1", got)
	}
	before = clock.Reads()
	timer.Tick()
	if got := clock.Reads() - before; got != 1 {
		t.Fatalf("Tick clock reads = %d, want 1", got)
	}
}
