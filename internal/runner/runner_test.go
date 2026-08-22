package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onlinealarmkur/timer-cli/internal/countdown"
	"github.com/onlinealarmkur/timer-cli/internal/keyboard"
	terminalui "github.com/onlinealarmkur/timer-cli/internal/terminal"
	"github.com/onlinealarmkur/timer-cli/internal/timerlimit"
)

type runnerClock struct{ now time.Time }

func (c *runnerClock) Now() time.Time { return c.now }

type stagedCancelContext struct{ errCalls int }

func (*stagedCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*stagedCancelContext) Done() <-chan struct{}       { return nil }
func (c *stagedCancelContext) Err() error {
	c.errCalls++
	if c.errCalls > 1 {
		return context.Canceled
	}
	return nil
}
func (*stagedCancelContext) Value(any) any { return nil }

type fakeRenderer struct {
	renders         int
	finishes        int
	closed          int
	status          string
	message         string
	renderErr       error
	finishErr       error
	closeErr        error
	onRender        func()
	snapshots       []countdown.Snapshot
	finishStatuses  []string
	finishSnapshots []countdown.Snapshot
	finishSnapshot  countdown.Snapshot
	finishedAt      time.Time
}

func (r *fakeRenderer) Render(snapshot countdown.Snapshot) error {
	r.renders++
	r.snapshots = append(r.snapshots, snapshot)
	if r.onRender != nil {
		r.onRender()
	}
	return r.renderErr
}

func (r *fakeRenderer) Finish(snapshot countdown.Snapshot, status, message string, finishedAt time.Time) error {
	r.finishes++
	r.status = status
	r.message = message
	r.finishSnapshot = snapshot
	r.finishStatuses = append(r.finishStatuses, status)
	r.finishSnapshots = append(r.finishSnapshots, snapshot)
	r.finishedAt = finishedAt
	return r.finishErr
}

func (r *fakeRenderer) Close() error {
	r.closed++
	return r.closeErr
}

type fakeAlert struct {
	rings int
	err   error
}

type callbackRenderer struct {
	Renderer
	onRender func(countdown.Snapshot)
}

func (r *callbackRenderer) Render(snapshot countdown.Snapshot) error {
	if err := r.Renderer.Render(snapshot); err != nil {
		return err
	}
	if r.onRender != nil {
		r.onRender(snapshot)
	}
	return nil
}

func (a *fakeAlert) Ring() error {
	a.rings++
	return a.err
}

func TestCompletionOccursExactlyOnce(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(10 * time.Second)
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}
	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Ticks: make(chan time.Time),
	})
	if err != nil || status != Completed {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 1 || renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 1 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
	if renderer.message != "Time's up!" {
		t.Fatalf("default completion message = %q", renderer.message)
	}
	if !renderer.finishedAt.Equal(clock.now) || !renderer.finishSnapshot.ObservedAt.Equal(clock.now) ||
		!renderer.finishedAt.Equal(renderer.finishSnapshot.ObservedAt) {
		t.Fatalf("completion snapshot=%+v finishedAt=%v, want %v", renderer.finishSnapshot, renderer.finishedAt, clock.now)
	}
}

func TestInjectedWakeDrivesCompletionExactlyOnce(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	ticks := make(chan time.Time, 1)
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		if renderer.renders == 1 {
			clock.now = startedAt.Add(time.Second)
			ticks <- clock.now
		}
	}
	alert := &fakeAlert{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, time.Second), Renderer: renderer,
		Alert: alert, Ticks: ticks,
	})
	if err != nil || status != Completed {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 2 || renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 1 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestLoopRestartsAfterEachCompletionUntilCanceled(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	ticks := make(chan time.Time, 2)
	actions := make(chan keyboard.Action, 2)
	actions <- keyboard.ActionAddMinute
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		switch renderer.renders {
		case 2:
			clock.now = clock.now.Add(time.Minute + time.Second)
			ticks <- clock.now
		case 4:
			clock.now = clock.now.Add(time.Second)
			ticks <- clock.now
		case 6:
			actions <- keyboard.ActionQuit
		}
	}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, time.Second), Renderer: renderer,
		Alert: alert, Loop: true, Actions: actions, Ticks: ticks,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 6 || renderer.finishes != 3 || renderer.closed != 1 || alert.rings != 2 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
	if got := strings.Join(renderer.finishStatuses, ","); got != "completed,completed,canceled" {
		t.Fatalf("finish statuses = %q", got)
	}

	if adjusted := renderer.snapshots[1]; adjusted.Total != time.Minute+time.Second ||
		adjusted.Remaining != time.Minute+time.Second {
		t.Fatalf("adjusted first cycle = %+v", adjusted)
	}
	if completed := renderer.finishSnapshots[0]; completed.Total != time.Minute+time.Second {
		t.Fatalf("completed first cycle = %+v", completed)
	}

	for _, index := range []int{3, 5} {
		snapshot := renderer.snapshots[index]
		if snapshot.Initial != time.Second || snapshot.Total != time.Second ||
			snapshot.Remaining != time.Second || snapshot.Elapsed != 0 ||
			snapshot.Finished || snapshot.Paused {
			t.Fatalf("restarted snapshot %d = %+v", index, snapshot)
		}
		if want := snapshot.ObservedAt.Add(time.Second); !snapshot.Target.Equal(want) {
			t.Fatalf("restarted target %d = %v, want %v", index, snapshot.Target, want)
		}
	}
	if got := renderer.finishSnapshots[2].Remaining; got != time.Second {
		t.Fatalf("quit snapshot remaining = %v, want %v", got, time.Second)
	}
}

func TestPendingCancellationPreemptsExpiredCompletion(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 4, 5, 6, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(ctx, Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Loop: true,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 0 || renderer.finishes != 1 || renderer.status != "canceled" ||
		renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestPendingQuitPreemptsExpiredCompletion(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 5, 6, 7, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionQuit
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Loop: true, Actions: actions,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 0 || renderer.finishes != 1 || renderer.status != "canceled" ||
		renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestPendingRestartPreemptsExpiredCompletion(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 6, 7, 8, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	actions := make(chan keyboard.Action, 2)
	actions <- keyboard.ActionRestart
	actions <- keyboard.ActionQuit
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Loop: true, Actions: actions,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 0 || renderer.finishes != 1 || renderer.status != "canceled" ||
		renderer.finishSnapshot.Remaining != time.Second || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestPendingRestartSkipsExpiredCompletion(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 6, 30, 0, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	actions := make(chan keyboard.Action, 2)
	actions <- keyboard.ActionRestart
	renderer := &fakeRenderer{}
	renderer.onRender = func() { actions <- keyboard.ActionQuit }
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Loop: true, Actions: actions,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 1 || renderer.finishes != 1 || renderer.status != "canceled" ||
		renderer.finishSnapshot.Remaining != time.Second || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestCompletionEventsQueuedDuringRenderPreemptAlerts(t *testing.T) {
	t.Parallel()
	t.Run("quit", func(t *testing.T) {
		t.Parallel()
		startedAt := time.Date(2026, 1, 2, 6, 40, 0, 0, time.UTC)
		clock := &runnerClock{now: startedAt}
		timer := countdown.New(clock, time.Second)
		clock.now = startedAt.Add(time.Second)
		actions := make(chan keyboard.Action, 1)
		renderer := &fakeRenderer{onRender: func() { actions <- keyboard.ActionQuit }}
		alert := &fakeAlert{}

		status, err := Run(context.Background(), Options{
			Countdown: timer, Renderer: renderer, Alert: alert, Loop: true, Actions: actions,
		})
		if err != nil || status != Canceled || renderer.renders != 1 ||
			renderer.finishes != 1 || renderer.status != "canceled" || alert.rings != 0 {
			t.Fatalf("Run=%s, %v renderer=%+v alert=%+v", status, err, renderer, alert)
		}
	})

	t.Run("restart", func(t *testing.T) {
		t.Parallel()
		startedAt := time.Date(2026, 1, 2, 6, 50, 0, 0, time.UTC)
		clock := &runnerClock{now: startedAt}
		timer := countdown.New(clock, time.Second)
		clock.now = startedAt.Add(time.Second)
		actions := make(chan keyboard.Action, 2)
		renderer := &fakeRenderer{}
		renderer.onRender = func() {
			if renderer.renders == 1 {
				actions <- keyboard.ActionRestart
			} else {
				actions <- keyboard.ActionQuit
			}
		}
		alert := &fakeAlert{}

		status, err := Run(context.Background(), Options{
			Countdown: timer, Renderer: renderer, Alert: alert, Loop: true, Actions: actions,
		})
		if err != nil || status != Canceled || renderer.renders != 2 ||
			renderer.finishes != 1 || renderer.status != "canceled" || alert.rings != 0 {
			t.Fatalf("Run=%s, %v renderer=%+v alert=%+v", status, err, renderer, alert)
		}
	})

	t.Run("input failure", func(t *testing.T) {
		t.Parallel()
		startedAt := time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
		clock := &runnerClock{now: startedAt}
		timer := countdown.New(clock, time.Second)
		clock.now = startedAt.Add(time.Second)
		inputFailure := errors.New("render boundary input failure")
		inputErrors := make(chan error, 1)
		renderer := &fakeRenderer{onRender: func() { inputErrors <- inputFailure }}
		alert := &fakeAlert{}

		status, err := Run(context.Background(), Options{
			Countdown: timer, Renderer: renderer, Alert: alert, Loop: true, InputErrors: inputErrors,
		})
		if status != Canceled || !errors.Is(err, inputFailure) || renderer.renders != 1 ||
			renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
			t.Fatalf("Run=%s, %v renderer=%+v alert=%+v", status, err, renderer, alert)
		}
	})
}

func TestCompletionBoundaryDrainsQueuedControls(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		action keyboard.Action
	}{
		{name: "none", action: keyboard.ActionNone},
		{name: "toggle pause", action: keyboard.ActionTogglePause},
		{name: "add minute", action: keyboard.ActionAddMinute},
		{name: "subtract minute", action: keyboard.ActionSubtractMinute},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			startedAt := time.Date(2026, 1, 2, 7, 8, 9, 0, time.UTC)
			clock := &runnerClock{now: startedAt}
			timer := countdown.New(clock, time.Second)
			clock.now = startedAt.Add(time.Second)
			actions := make(chan keyboard.Action, 1)
			actions <- test.action
			renderer := &fakeRenderer{}
			alert := &fakeAlert{}

			status, err := Run(context.Background(), Options{
				Countdown: timer, Renderer: renderer, Alert: alert, Actions: actions,
			})
			if err != nil || status != Completed {
				t.Fatalf("Run = %s, %v", status, err)
			}
			if renderer.renders != 1 || renderer.finishes != 1 || renderer.status != "completed" ||
				renderer.closed != 1 || alert.rings != 1 {
				t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
			}
		})
	}
}

func TestQueuedAddMinuteBeforeCompletionIsDiscarded(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 2, 7, 15, 0, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionAddMinute
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Actions: actions,
	})
	if err != nil || status != Completed {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 1 || renderer.finishes != 1 || renderer.status != "completed" ||
		renderer.closed != 1 || alert.rings != 1 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
	// A queued add-minute is a no-op on a finished countdown; the completion
	// must not be extended by it.
	if got := renderer.finishSnapshot.Total; got != time.Second {
		t.Fatalf("completed total = %v, want %v (queued add minute must be discarded on a finished countdown)", got, time.Second)
	}
	if got := renderer.finishSnapshot.Remaining; got != 0 {
		t.Fatalf("completed remaining = %v, want 0", got)
	}
}

func TestCompletionBoundaryHandlesInputChannelStates(t *testing.T) {
	t.Parallel()
	t.Run("nil event", func(t *testing.T) {
		t.Parallel()
		startedAt := time.Date(2026, 1, 2, 8, 9, 10, 0, time.UTC)
		clock := &runnerClock{now: startedAt}
		timer := countdown.New(clock, time.Second)
		clock.now = startedAt.Add(time.Second)
		inputErrors := make(chan error, 1)
		inputErrors <- nil
		actions := make(chan keyboard.Action)
		close(actions)
		renderer := &fakeRenderer{}
		alert := &fakeAlert{}

		status, err := Run(context.Background(), Options{
			Countdown: timer, Renderer: renderer, Alert: alert,
			Actions: actions, InputErrors: inputErrors,
		})
		if err != nil || status != Completed || renderer.finishes != 1 ||
			renderer.closed != 1 || alert.rings != 1 {
			t.Fatalf("Run=%s, %v renderer=%+v alert=%+v", status, err, renderer, alert)
		}
	})

	t.Run("closed input channel", func(t *testing.T) {
		t.Parallel()
		startedAt := time.Date(2026, 1, 2, 8, 30, 0, 0, time.UTC)
		clock := &runnerClock{now: startedAt}
		timer := countdown.New(clock, time.Second)
		clock.now = startedAt.Add(time.Second)
		inputErrors := make(chan error)
		close(inputErrors)
		renderer := &fakeRenderer{}
		alert := &fakeAlert{}

		status, err := Run(context.Background(), Options{
			Countdown: timer, Renderer: renderer, Alert: alert, InputErrors: inputErrors,
		})
		if err != nil || status != Completed || renderer.finishes != 1 ||
			renderer.closed != 1 || alert.rings != 1 {
			t.Fatalf("Run=%s, %v renderer=%+v alert=%+v", status, err, renderer, alert)
		}
	})

	t.Run("input failure", func(t *testing.T) {
		t.Parallel()
		startedAt := time.Date(2026, 1, 2, 9, 10, 11, 0, time.UTC)
		clock := &runnerClock{now: startedAt}
		timer := countdown.New(clock, time.Second)
		clock.now = startedAt.Add(time.Second)
		inputFailure := errors.New("boundary input failure")
		inputErrors := make(chan error, 1)
		inputErrors <- inputFailure
		renderer := &fakeRenderer{}
		alert := &fakeAlert{}

		status, err := Run(context.Background(), Options{
			Countdown: timer, Renderer: renderer, Alert: alert, InputErrors: inputErrors,
		})
		if status != Canceled || !errors.Is(err, inputFailure) {
			t.Fatalf("Run = %s, %v", status, err)
		}
		if renderer.renders != 0 || renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
			t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
		}
	})
}

func TestCompletionPreemptionRechecksContextAtCommitPoint(t *testing.T) {
	t.Parallel()
	clock := &runnerClock{now: time.Date(2026, 1, 2, 10, 11, 12, 0, time.UTC)}
	opts := Options{Countdown: countdown.New(clock, time.Minute)}
	preempted, err := preemptCompletion(&stagedCancelContext{}, &opts)
	if err != nil || !preempted {
		t.Fatalf("preemptCompletion = %v, %v; want true, nil", preempted, err)
	}
}

func TestCompletionPreemptionUsesBoundedActionSnapshot(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionSuspend
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	suspendCalls := 0
	opts := Options{
		Actions: actions,
		Suspend: func() error {
			suspendCalls++
			// Replenish the recognized action synchronously. An unbounded drain
			// consumes this new action too and repeats forever; canceling on the
			// second call gives that behavior a deterministic test escape hatch.
			actions <- keyboard.ActionSuspend
			if suspendCalls == 2 {
				cancel()
			}
			return nil
		},
	}

	preempted, err := preemptCompletion(ctx, &opts)
	if err != nil || preempted {
		t.Fatalf("preemptCompletion = %v, %v; want false, nil", preempted, err)
	}
	if suspendCalls != 1 {
		t.Fatalf("suspend calls = %d, want 1", suspendCalls)
	}
	if ctx.Err() != nil {
		t.Fatalf("context was canceled after consuming a later action: %v", ctx.Err())
	}
	if got := len(actions); got != 1 {
		t.Fatalf("later queued actions = %d, want 1 left for the next boundary", got)
	}
}

func TestInternalWakeTimerPathCanBeCanceled(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionQuit
	clock := &runnerClock{now: time.Date(2026, 1, 2, 11, 12, 13, 0, time.UTC)}
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, time.Minute), Renderer: renderer,
		Alert: alert, Actions: actions,
	})
	if err != nil || status != Canceled || renderer.renders != 1 ||
		renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("Run=%s, %v renderer=%+v alert=%+v", status, err, renderer, alert)
	}
}

func TestSuspendActionRunsSerializedLifecycleAndContinues(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 2)
	actions <- keyboard.ActionSuspend
	actions <- keyboard.ActionQuit
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}
	suspendCalls := 0

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Now()}, time.Minute),
		Renderer:  renderer, Alert: alert, Actions: actions,
		Suspend: func() error {
			suspendCalls++
			return nil
		},
	})
	if err != nil || status != Canceled || suspendCalls != 1 {
		t.Fatalf("Run=%s, %v suspend calls=%d", status, err, suspendCalls)
	}
	if renderer.renders != 2 || renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestSuspendSignalRunsLifecycleAndContinues(t *testing.T) {
	t.Parallel()
	suspendSignals := make(chan os.Signal, 1)
	suspendSignals <- os.Interrupt
	actions := make(chan keyboard.Action, 1)
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}
	suspendCalls := 0

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Now()}, time.Minute),
		Renderer:  renderer, Alert: alert, Actions: actions, SuspendSignals: suspendSignals,
		Suspend: func() error {
			suspendCalls++
			actions <- keyboard.ActionQuit
			return nil
		},
	})
	if err != nil || status != Canceled || suspendCalls != 1 {
		t.Fatalf("Run=%s, %v suspend calls=%d", status, err, suspendCalls)
	}
	if renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestClosedSuspendSignalChannelDisablesAndContinues(t *testing.T) {
	t.Parallel()
	suspendSignals := make(chan os.Signal)
	close(suspendSignals)
	actions := make(chan keyboard.Action, 1)
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		if renderer.renders == 2 {
			actions <- keyboard.ActionQuit
		}
	}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Now()}, time.Minute),
		Renderer:  renderer, Alert: alert, Actions: actions, SuspendSignals: suspendSignals,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.renders != 2 || renderer.finishes != 1 || renderer.status != "canceled" ||
		renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestSuspendSignalFailureStopsWithoutCompletion(t *testing.T) {
	t.Parallel()
	suspendFailure := errors.New("signal suspension failed")
	suspendSignals := make(chan os.Signal, 1)
	suspendSignals <- os.Interrupt
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Now()}, time.Minute),
		Renderer:  renderer, Alert: alert, SuspendSignals: suspendSignals,
		Suspend: func() error { return suspendFailure },
	})
	if status != Canceled || !errors.Is(err, suspendFailure) || !strings.Contains(err.Error(), "suspend timer") {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestSuspendFailureStopsWithoutCompletion(t *testing.T) {
	t.Parallel()
	suspendFailure := errors.New("job control unavailable")
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionSuspend
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Now()}, time.Minute),
		Renderer:  renderer, Alert: alert, Actions: actions,
		Suspend: func() error { return suspendFailure },
	})
	if status != Canceled || !errors.Is(err, suspendFailure) || !strings.Contains(err.Error(), "suspend timer") {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestSuspendActionWithoutLifecycleFailsExplicitly(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionSuspend
	renderer := &fakeRenderer{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Now()}, time.Minute),
		Renderer:  renderer, Alert: &fakeAlert{}, Actions: actions,
	})
	if status != Canceled || err == nil || !strings.Contains(err.Error(), "suspension is unavailable") {
		t.Fatalf("Run=%s, %v", status, err)
	}
}

func TestPendingSuspendAtCompletionDoesNotDuplicateAlert(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 3, 1, 2, 3, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	actions := make(chan keyboard.Action, 2)
	actions <- keyboard.ActionSuspend
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}
	suspendCalls := 0

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Actions: actions,
		Suspend: func() error {
			suspendCalls++
			actions <- keyboard.ActionQuit
			return nil
		},
	})
	if err != nil || status != Canceled || suspendCalls != 1 {
		t.Fatalf("Run=%s, %v suspend calls=%d", status, err, suspendCalls)
	}
	if renderer.finishes != 1 || renderer.status != "canceled" || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestPendingSuspendSignalAtCompletionDoesNotDuplicateAlert(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 3, 2, 3, 4, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	suspendSignals := make(chan os.Signal, 1)
	suspendSignals <- os.Interrupt
	actions := make(chan keyboard.Action, 1)
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert,
		Actions: actions, SuspendSignals: suspendSignals,
		Suspend: func() error {
			actions <- keyboard.ActionQuit
			return nil
		},
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.finishes != 1 || renderer.status != "canceled" || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestCompletionHandlesClosedSuspendSignalChannel(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	suspendSignals := make(chan os.Signal)
	close(suspendSignals)
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, SuspendSignals: suspendSignals,
	})
	if err != nil || status != Completed {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.renders != 1 || renderer.finishes != 1 || renderer.status != "completed" ||
		renderer.closed != 1 || alert.rings != 1 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestCompletionSuspendSignalFailureStopsBeforeOutput(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	suspendFailure := errors.New("completion suspension failed")
	suspendSignals := make(chan os.Signal, 1)
	suspendSignals <- os.Interrupt
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, SuspendSignals: suspendSignals,
		Suspend: func() error { return suspendFailure },
	})
	if status != Canceled || !errors.Is(err, suspendFailure) || !strings.Contains(err.Error(), "suspend timer") {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.renders != 0 || renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestCompletionSuspendActionWithoutLifecycleFailsBeforeOutput(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 3, 5, 6, 7, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	timer := countdown.New(clock, time.Second)
	clock.now = startedAt.Add(time.Second)
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionSuspend
	renderer := &fakeRenderer{}
	alert := &fakeAlert{}

	status, err := Run(context.Background(), Options{
		Countdown: timer, Renderer: renderer, Alert: alert, Actions: actions,
	})
	if status != Canceled || err == nil || !strings.Contains(err.Error(), "suspension is unavailable") {
		t.Fatalf("Run=%s, %v", status, err)
	}
	if renderer.renders != 0 || renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestPreemptCompletionHandlesAlreadyCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	preempted, err := preemptCompletion(ctx, &Options{})
	if err != nil || !preempted {
		t.Fatalf("preemptCompletion = %v, %v; want true, nil", preempted, err)
	}
}

func TestAddActionCannotReviveTimerThatExpiredAfterRender(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	actions := make(chan keyboard.Action, 1)
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		if renderer.renders == 1 {
			clock.now = startedAt.Add(time.Second)
			actions <- keyboard.ActionAddMinute
		}
	}
	alert := &fakeAlert{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, time.Second), Renderer: renderer,
		Alert: alert, Actions: actions, Ticks: make(chan time.Time),
	})
	if err != nil || status != Completed {
		t.Fatalf("Run = %s, %v; expired add action must not prevent completion", status, err)
	}
	if renderer.renders != 2 || renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 1 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
	if got := renderer.finishSnapshot.Total; got != time.Second {
		t.Fatalf("completed total = %v, want %v", got, time.Second)
	}
}

func TestNextWakeDelay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		snapshot countdown.Snapshot
		interval time.Duration
		want     time.Duration
	}{
		{
			name: "paused", snapshot: countdown.Snapshot{
				Total: time.Minute, Remaining: time.Minute, Paused: true,
			}, interval: 100 * time.Millisecond, want: pausedRenderInterval,
		},
		{
			name: "paused ignores short remaining", snapshot: countdown.Snapshot{
				Total: time.Minute, Remaining: 250 * time.Millisecond, Paused: true,
			}, interval: 100 * time.Millisecond, want: pausedRenderInterval,
		},
		{
			name: "complete", snapshot: countdown.Snapshot{Total: time.Minute},
			interval: 100 * time.Millisecond,
		},
		{
			name: "completion sooner than one-second cadence", snapshot: countdown.Snapshot{
				Total: time.Minute, Remaining: 250 * time.Millisecond,
			}, interval: time.Second, want: 250 * time.Millisecond,
		},
		{
			name: "one-second cadence", snapshot: countdown.Snapshot{
				Total: time.Minute, Remaining: 30 * time.Second,
			}, interval: time.Second, want: time.Second,
		},
		{
			name: "generic one-second cadence does not become adaptive", snapshot: countdown.Snapshot{
				Total: 30 * 24 * time.Hour, Remaining: 30 * 24 * time.Hour,
			}, interval: time.Second, want: time.Second,
		},
		{
			name: "short visible timer", snapshot: countdown.Snapshot{
				Total: 5 * time.Second, Remaining: 5 * time.Second,
			}, interval: 100 * time.Millisecond, want: 100 * time.Millisecond,
		},
		{
			name: "long visible timer", snapshot: countdown.Snapshot{
				Total: timerlimit.MaxDuration, Remaining: timerlimit.MaxDuration,
			}, interval: 100 * time.Millisecond, want: time.Second,
		},
		{
			name: "fractional display boundary", snapshot: countdown.Snapshot{
				Total: 5 * time.Minute, Remaining: 2250 * time.Millisecond,
			}, interval: 100 * time.Millisecond, want: 250 * time.Millisecond,
		},
		{
			name: "tiny display boundary uses interval floor", snapshot: countdown.Snapshot{
				Total: 5 * time.Minute, Remaining: 2050 * time.Millisecond,
			}, interval: 100 * time.Millisecond, want: 100 * time.Millisecond,
		},
		{
			name: "invalid interval uses fallback", snapshot: countdown.Snapshot{
				Total: 5 * time.Second, Remaining: 5 * time.Second,
			}, want: 250 * time.Millisecond,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := nextWakeDelay(test.snapshot, test.interval, false); got != test.want {
				t.Fatalf("nextWakeDelay() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAdaptiveWakeSchedulingMatchesRedirectedRecords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		total   time.Duration
		cadence time.Duration
		wakes   int
	}{
		{name: "59 minutes", total: 59 * time.Minute, cadence: time.Second, wakes: 3540},
		{name: "one hour", total: time.Hour, cadence: time.Second, wakes: 3600},
		{name: "24 hours", total: 24 * time.Hour, cadence: time.Minute, wakes: 1440},
		{name: "30 days", total: 30 * 24 * time.Hour, cadence: 10 * time.Minute, wakes: 4320},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			renderer := terminalui.New(&output, terminalui.Options{})
			remaining := test.total
			wakes := 0
			for {
				progress := float64(test.total-remaining) / float64(test.total)
				snapshot := countdown.Snapshot{
					Initial: test.total, Total: test.total, Remaining: remaining,
					Elapsed: test.total - remaining, Progress: progress, Finished: remaining == 0,
				}
				if err := renderer.Render(snapshot); err != nil {
					t.Fatal(err)
				}
				if remaining == 0 {
					break
				}
				delay := nextWakeDelay(snapshot, time.Second, true)
				if delay <= 0 || delay > remaining {
					t.Fatalf("invalid delay %v at remaining %v", delay, remaining)
				}
				remaining -= delay
				wakes++
			}

			if wakes != test.wakes {
				t.Fatalf("wakes = %d, want %d", wakes, test.wakes)
			}
			records := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
			if len(records) != test.wakes+1 {
				t.Fatalf("records = %d, want %d", len(records), test.wakes+1)
			}
			for index, record := range records {
				wantRemaining := test.total - time.Duration(index)*test.cadence
				wantPrefix := "remaining=" + terminalui.FormatDuration(wantRemaining) + " "
				if !strings.HasPrefix(record, wantPrefix) {
					t.Fatalf("record %d = %q, want prefix %q", index, record, wantPrefix)
				}
			}
			if !strings.HasSuffix(records[len(records)-1], "progress=100% state=running") {
				t.Fatalf("completion record = %q", records[len(records)-1])
			}
		})
	}
}

func TestAdaptiveWakeRemainsResponsiveToBufferedInput(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionQuit
	renderer := &fakeRenderer{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, 30*24*time.Hour), Renderer: renderer,
		Alert: &fakeAlert{}, Actions: actions, Interval: time.Second, RecordCadence: true,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 1 || renderer.finishes != 1 || renderer.closed != 1 {
		t.Fatalf("renderer = %+v", renderer)
	}
}

func TestPausedWakeKeepsStateAndSuppressesRedirectedOutput(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 3, 1, 2, 3, 4, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	actions := make(chan keyboard.Action, 1)
	ticks := make(chan time.Time, 1)
	var output bytes.Buffer
	renderer := &callbackRenderer{Renderer: terminalui.New(&output, terminalui.Options{})}
	var snapshots []countdown.Snapshot
	renderer.onRender = func(snapshot countdown.Snapshot) {
		snapshots = append(snapshots, snapshot)
		switch len(snapshots) {
		case 1:
			actions <- keyboard.ActionTogglePause
		case 2:
			clock.now = startedAt.Add(10 * time.Second)
			ticks <- clock.now
		case 3:
			actions <- keyboard.ActionQuit
		}
	}

	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, 2*time.Minute), Renderer: renderer,
		Alert: &fakeAlert{}, Actions: actions, Ticks: ticks,
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if len(snapshots) != 3 || !snapshots[1].Paused || !snapshots[2].Paused {
		t.Fatalf("snapshots = %+v, want two paused redraws", snapshots)
	}
	if snapshots[1].Remaining != snapshots[2].Remaining || snapshots[1].Elapsed != snapshots[2].Elapsed {
		t.Fatalf("paused state changed across wake: before=%+v after=%+v", snapshots[1], snapshots[2])
	}
	if records := strings.Count(output.String(), "\n"); records != 2 {
		t.Fatalf("redirected records = %d, want active and paused only; output=%q", records, output.String())
	}
}

func TestCancellationDoesNotAlert(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	startedAt := time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	renderer := &fakeRenderer{onRender: func() {
		clock.now = clock.now.Add(7 * time.Second)
		cancel()
	}}
	alert := &fakeAlert{}
	status, err := Run(ctx, Options{
		Countdown: countdown.New(clock, time.Minute), Renderer: renderer,
		Alert: alert, Ticks: make(chan time.Time),
	})
	if err != nil || status != Canceled || alert.rings != 0 || renderer.status != "canceled" || renderer.closed != 1 {
		t.Fatalf("status=%s err=%v renderer=%+v alert=%+v", status, err, renderer, alert)
	}
	wantObservedAt := startedAt.Add(7 * time.Second)
	if !renderer.finishedAt.Equal(wantObservedAt) || !renderer.finishSnapshot.ObservedAt.Equal(wantObservedAt) ||
		renderer.finishSnapshot.Remaining != 53*time.Second {
		t.Fatalf("cancellation snapshot=%+v finishedAt=%v, want observation %v", renderer.finishSnapshot, renderer.finishedAt, wantObservedAt)
	}
}

func TestResetTimerReusesAllocation(t *testing.T) {
	t.Parallel()
	timer := time.NewTimer(time.Hour)
	t.Cleanup(func() { stopTimer(timer) })
	if got := resetTimer(timer, time.Hour); got != timer {
		t.Fatalf("resetTimer returned %p, want existing timer %p", got, timer)
	}
}

func TestResetTimerCreatesAllocation(t *testing.T) {
	t.Parallel()
	timer := resetTimer(nil, time.Hour)
	t.Cleanup(func() { stopTimer(timer) })
	if timer == nil {
		t.Fatal("resetTimer(nil) returned nil")
	}
}

func TestResetTimerReuseDoesNotAllocate(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	t.Cleanup(func() { stopTimer(timer) })
	if allocs := testing.AllocsPerRun(1000, func() {
		resetTimer(timer, time.Hour)
	}); allocs != 0 {
		t.Fatalf("resetting an existing wake timer allocated %.2f objects per call", allocs)
	}
}

func TestTogglePauseAndResumeActions(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 3)
	actions <- keyboard.ActionTogglePause
	actions <- keyboard.ActionTogglePause
	actions <- keyboard.ActionQuit
	clock := &runnerClock{now: time.Date(2026, 3, 1, 2, 3, 4, 0, time.UTC)}
	renderer := &fakeRenderer{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, 2*time.Minute), Renderer: renderer,
		Alert: &fakeAlert{}, Actions: actions, Ticks: make(chan time.Time),
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if len(renderer.snapshots) != 3 {
		t.Fatalf("rendered snapshots = %d, want 3", len(renderer.snapshots))
	}
	if renderer.snapshots[0].Paused || !renderer.snapshots[1].Paused || renderer.snapshots[2].Paused {
		t.Fatalf("pause snapshots = %v, %v, %v", renderer.snapshots[0].Paused, renderer.snapshots[1].Paused, renderer.snapshots[2].Paused)
	}
}

func TestCountdownMutationActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		action        keyboard.Action
		advanceBefore time.Duration
		wantTotal     time.Duration
		wantRemaining time.Duration
	}{
		{name: "restart", action: keyboard.ActionRestart, advanceBefore: 30 * time.Second, wantTotal: 2 * time.Minute, wantRemaining: 2 * time.Minute},
		{name: "add minute", action: keyboard.ActionAddMinute, wantTotal: 3 * time.Minute, wantRemaining: 3 * time.Minute},
		{name: "subtract minute", action: keyboard.ActionSubtractMinute, wantTotal: time.Minute, wantRemaining: time.Minute},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			actions := make(chan keyboard.Action, 2)
			actions <- test.action
			actions <- keyboard.ActionQuit
			startedAt := time.Date(2026, 3, 2, 3, 4, 5, 0, time.UTC)
			clock := &runnerClock{now: startedAt}
			timer := countdown.New(clock, 2*time.Minute)
			clock.now = clock.now.Add(test.advanceBefore)
			renderer := &fakeRenderer{}
			status, err := Run(context.Background(), Options{
				Countdown: timer, Renderer: renderer, Alert: &fakeAlert{},
				Actions: actions, Ticks: make(chan time.Time),
			})
			if err != nil || status != Canceled {
				t.Fatalf("Run = %s, %v", status, err)
			}
			if len(renderer.snapshots) != 2 {
				t.Fatalf("rendered snapshots = %d, want 2", len(renderer.snapshots))
			}
			got := renderer.snapshots[1]
			if got.Total != test.wantTotal || got.Remaining != test.wantRemaining {
				t.Fatalf("mutated snapshot=%+v, want total=%v remaining=%v", got, test.wantTotal, test.wantRemaining)
			}
			if test.action == keyboard.ActionRestart && !got.Target.Equal(clock.now.Add(2*time.Minute)) {
				t.Fatalf("restart target=%v, want %v", got.Target, clock.now.Add(2*time.Minute))
			}
		})
	}
}

func TestRepeatedRunnerAdditionsNeverExceedMaximum(t *testing.T) {
	t.Parallel()
	const additions = 20
	actions := make(chan keyboard.Action, additions+1)
	for range additions {
		actions <- keyboard.ActionAddMinute
	}
	actions <- keyboard.ActionQuit

	clock := &runnerClock{now: time.Date(2026, 3, 2, 4, 5, 6, 0, time.UTC)}
	renderer := &fakeRenderer{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, timerlimit.MaxDuration-time.Minute),
		Renderer:  renderer, Alert: &fakeAlert{}, Actions: actions, Ticks: make(chan time.Time),
	})
	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if len(renderer.snapshots) != additions+1 {
		t.Fatalf("rendered snapshots = %d, want %d", len(renderer.snapshots), additions+1)
	}
	for index, snapshot := range renderer.snapshots {
		if snapshot.Total > timerlimit.MaxDuration {
			t.Fatalf("snapshot %d total = %v, maximum %v", index, snapshot.Total, timerlimit.MaxDuration)
		}
	}
	if got := renderer.finishSnapshot.Total; got != timerlimit.MaxDuration {
		t.Fatalf("final total = %v, want %v", got, timerlimit.MaxDuration)
	}
}

func TestQuitActionDoesNotAlert(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionQuit
	startedAt := time.Date(2026, 3, 3, 4, 5, 6, 0, time.UTC)
	clock := &runnerClock{now: startedAt}
	renderer := &fakeRenderer{onRender: func() { clock.now = clock.now.Add(11 * time.Second) }}
	alert := &fakeAlert{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(clock, time.Minute), Renderer: renderer,
		Alert: alert, Actions: actions, Ticks: make(chan time.Time),
	})
	if err != nil || status != Canceled || alert.rings != 0 || renderer.finishes != 1 || renderer.closed != 1 {
		t.Fatalf("status=%s err=%v renderer=%+v alert=%+v", status, err, renderer, alert)
	}
	wantObservedAt := startedAt.Add(11 * time.Second)
	if !renderer.finishedAt.Equal(wantObservedAt) || !renderer.finishSnapshot.ObservedAt.Equal(wantObservedAt) ||
		renderer.finishSnapshot.Remaining != 49*time.Second {
		t.Fatalf("quit snapshot=%+v finishedAt=%v, want observation %v", renderer.finishSnapshot, renderer.finishedAt, wantObservedAt)
	}
}

func TestClosedActionChannelIsDisabled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	actions := make(chan keyboard.Action)
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		switch renderer.renders {
		case 1:
			close(actions)
		case 2:
			cancel()
		}
	}
	status, err := Run(ctx, Options{
		Countdown: countdown.New(&runnerClock{now: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)}, time.Minute),
		Renderer:  renderer, Alert: &fakeAlert{}, Actions: actions, Ticks: make(chan time.Time),
	})
	if err != nil || status != Canceled || renderer.renders != 2 || renderer.finishes != 1 || renderer.closed != 1 {
		t.Fatalf("status=%s err=%v renderer=%+v", status, err, renderer)
	}
}

func TestInputErrorStopsPausedTimerAndCleansUp(t *testing.T) {
	t.Parallel()
	inputFailure := errors.New("keyboard reader failed")
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionTogglePause
	inputErrors := make(chan error, 1)
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		if renderer.renders == 2 {
			inputErrors <- inputFailure
		}
	}
	alert := &fakeAlert{}
	status, err := Run(context.Background(), Options{
		Countdown:   countdown.New(&runnerClock{now: time.Date(2026, 3, 4, 6, 7, 8, 0, time.UTC)}, time.Minute),
		Renderer:    renderer,
		Alert:       alert,
		Actions:     actions,
		InputErrors: inputErrors,
		Ticks:       make(chan time.Time),
	})
	if status != Canceled || !errors.Is(err, inputFailure) || !strings.Contains(err.Error(), "keyboard input failed") {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if len(renderer.snapshots) != 2 || !renderer.snapshots[1].Paused {
		t.Fatalf("snapshots = %+v, want second snapshot paused", renderer.snapshots)
	}
	if renderer.finishes != 0 || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestClosedInputErrorChannelIsDisabled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	inputErrors := make(chan error)
	close(inputErrors)
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		if renderer.renders == 2 {
			cancel()
		}
	}
	status, err := Run(ctx, Options{
		Countdown:   countdown.New(&runnerClock{now: time.Date(2026, 3, 4, 7, 8, 9, 0, time.UTC)}, time.Minute),
		Renderer:    renderer,
		Alert:       &fakeAlert{},
		InputErrors: inputErrors,
		Ticks:       make(chan time.Time),
	})
	if err != nil || status != Canceled || renderer.renders != 2 || renderer.finishes != 1 || renderer.closed != 1 {
		t.Fatalf("status=%s err=%v renderer=%+v", status, err, renderer)
	}
}

func TestRunCancelsPausedTimerWhenAllInputSourcesAreGone(t *testing.T) {
	t.Parallel()
	actions := make(chan keyboard.Action, 1)
	actions <- keyboard.ActionTogglePause
	inputErrors := make(chan error, 1)
	clock := &runnerClock{now: time.Date(2026, 3, 6, 7, 8, 9, 0, time.UTC)}
	renderer := &fakeRenderer{}
	renderer.onRender = func() {
		if renderer.renders == 2 {
			close(actions)
			close(inputErrors)
		}
	}
	alert := &fakeAlert{}

	// Safety net only: a healthy guard returns almost immediately. If the
	// guard regresses, Run blocks until ctx.Done() fires at this deadline
	// instead of hanging the suite forever, and the elapsed-time assertion
	// below catches the regression as a failure.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	started := time.Now()
	status, err := Run(ctx, Options{
		Countdown:   countdown.New(clock, 2*time.Minute),
		Renderer:    renderer,
		Alert:       alert,
		Actions:     actions,
		InputErrors: inputErrors,
	})
	elapsed := time.Since(started)

	if err != nil || status != Canceled {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if elapsed > time.Second {
		t.Fatalf("Run took %v to return after a paused timer lost all input sources; want a prompt guard-driven return, not the ctx safety net", elapsed)
	}
	if renderer.finishes != 1 || renderer.status != "canceled" || renderer.closed != 1 || alert.rings != 0 {
		t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
	}
}

func TestClosedInjectedTickChannelFailsWithoutSpinning(t *testing.T) {
	t.Parallel()
	ticks := make(chan time.Time)
	close(ticks)
	renderer := &fakeRenderer{}
	status, err := Run(context.Background(), Options{
		Countdown: countdown.New(&runnerClock{now: time.Date(2026, 3, 5, 6, 7, 8, 0, time.UTC)}, time.Minute),
		Renderer:  renderer, Alert: &fakeAlert{}, Ticks: ticks,
	})
	if status != Canceled || err == nil || !strings.Contains(err.Error(), "tick channel closed") {
		t.Fatalf("Run = %s, %v", status, err)
	}
	if renderer.renders != 1 || renderer.finishes != 0 || renderer.closed != 1 {
		t.Fatalf("renderer=%+v", renderer)
	}
}

func TestStopTimerHandlesInactiveAndConsumedTimers(t *testing.T) {
	t.Parallel()
	stopTimer(nil)

	active := time.NewTimer(time.Hour)
	stopTimer(active)

	// An expired timer is safe to stop, and cleanup must not leave its channel
	// readable regardless of whether the runtime has made its tick receivable.
	fired := time.NewTimer(0)
	time.Sleep(2 * time.Millisecond)
	stopTimer(fired)
	select {
	case <-fired.C:
		t.Fatal("stopTimer left a fired timer's channel readable")
	default:
	}
}

func TestRuntimeFailuresAndCleanup(t *testing.T) {
	t.Parallel()
	renderFailure := errors.New("render failed")
	finishFailure := errors.New("finish failed")
	alertFailure := errors.New("alert failed")
	closeFailure := errors.New("close failed")
	tests := []struct {
		name       string
		configure  func(*runnerClock, *countdown.Countdown, *fakeRenderer, *fakeAlert) context.Context
		wantErrors []error
		wantRender int
		wantFinish int
		wantRings  int
	}{
		{
			name: "render", wantErrors: []error{renderFailure}, wantRender: 1,
			configure: func(_ *runnerClock, _ *countdown.Countdown, renderer *fakeRenderer, _ *fakeAlert) context.Context {
				renderer.renderErr = renderFailure
				return context.Background()
			},
		},
		{
			name: "finish", wantErrors: []error{finishFailure}, wantRender: 1, wantFinish: 1,
			configure: func(_ *runnerClock, _ *countdown.Countdown, renderer *fakeRenderer, _ *fakeAlert) context.Context {
				renderer.finishErr = finishFailure
				ctx, cancel := context.WithCancel(context.Background())
				renderer.onRender = cancel
				return ctx
			},
		},
		{
			name: "alert", wantErrors: []error{alertFailure}, wantRender: 1, wantFinish: 1, wantRings: 1,
			configure: func(clock *runnerClock, _ *countdown.Countdown, _ *fakeRenderer, alert *fakeAlert) context.Context {
				clock.now = clock.now.Add(2 * time.Minute)
				alert.err = alertFailure
				return context.Background()
			},
		},
		{
			name: "close", wantErrors: []error{closeFailure}, wantRender: 1, wantFinish: 1,
			configure: func(_ *runnerClock, _ *countdown.Countdown, renderer *fakeRenderer, _ *fakeAlert) context.Context {
				renderer.closeErr = closeFailure
				ctx, cancel := context.WithCancel(context.Background())
				renderer.onRender = cancel
				return ctx
			},
		},
		{
			name: "primary and close errors are joined", wantErrors: []error{renderFailure, closeFailure}, wantRender: 1,
			configure: func(_ *runnerClock, _ *countdown.Countdown, renderer *fakeRenderer, _ *fakeAlert) context.Context {
				renderer.renderErr = renderFailure
				renderer.closeErr = closeFailure
				return context.Background()
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clock := &runnerClock{now: time.Date(2026, 4, 4, 5, 6, 7, 0, time.UTC)}
			timer := countdown.New(clock, time.Minute)
			renderer := &fakeRenderer{}
			alert := &fakeAlert{}
			ctx := test.configure(clock, timer, renderer, alert)
			status, err := Run(ctx, Options{
				Countdown: timer, Renderer: renderer, Alert: alert, Ticks: make(chan time.Time),
			})
			if status != Canceled || err == nil {
				t.Fatalf("Run = %s, %v", status, err)
			}
			for _, wantErr := range test.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Errorf("error %q does not contain %q", err, wantErr)
				}
			}
			if renderer.renders != test.wantRender || renderer.finishes != test.wantFinish ||
				renderer.closed != 1 || alert.rings != test.wantRings {
				t.Errorf("renderer=%+v alert=%+v", renderer, alert)
			}
			if renderer.closeErr != nil && !strings.Contains(err.Error(), "close renderer") {
				t.Errorf("close error lacks context: %v", err)
			}
		})
	}
}

func TestCompletionAndQuitFinishFailuresDoNotAlert(t *testing.T) {
	t.Parallel()
	finishFailure := errors.New("finish failed")
	startedAt := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	tests := []struct {
		name      string
		clockNow  time.Time
		actions   <-chan keyboard.Action
		wantLabel string
	}{
		{
			name: "completion", clockNow: startedAt.Add(2 * time.Minute),
			wantLabel: "render completion",
		},
		{
			name: "quit", clockNow: startedAt,
			actions: func() <-chan keyboard.Action {
				actions := make(chan keyboard.Action, 1)
				actions <- keyboard.ActionQuit
				return actions
			}(),
			wantLabel: "render cancellation",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clock := &runnerClock{now: startedAt}
			timer := countdown.New(clock, time.Minute)
			clock.now = test.clockNow
			renderer := &fakeRenderer{finishErr: finishFailure}
			alert := &fakeAlert{}
			status, err := Run(context.Background(), Options{
				Countdown: timer, Renderer: renderer, Alert: alert,
				Actions: test.actions, Ticks: make(chan time.Time),
			})
			if status != Canceled || !errors.Is(err, finishFailure) || !strings.Contains(err.Error(), test.wantLabel) {
				t.Fatalf("Run = %s, %v", status, err)
			}
			if renderer.renders != 1 || renderer.finishes != 1 || renderer.closed != 1 || alert.rings != 0 {
				t.Fatalf("renderer=%+v alert=%+v", renderer, alert)
			}
		})
	}
}

func TestMissingRendererReturnsDependencyError(t *testing.T) {
	t.Parallel()
	status, err := Run(context.Background(), Options{})
	if status != Canceled || err == nil || !strings.Contains(err.Error(), "requires countdown, renderer, and alert") {
		t.Fatalf("Run = %s, %v", status, err)
	}
}

func TestRendererClosesWhenOtherDependencyIsMissing(t *testing.T) {
	t.Parallel()
	renderer := &fakeRenderer{}
	status, err := Run(context.Background(), Options{Renderer: renderer})
	if status != Canceled || err == nil || renderer.closed != 1 {
		t.Fatalf("Run = %s, %v; closed=%d", status, err, renderer.closed)
	}
}
