// Package runner coordinates countdown state, rendering, input, and alerts.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/onlinealarmkur/timer-cli/internal/countdown"
	"github.com/onlinealarmkur/timer-cli/internal/keyboard"
	"github.com/onlinealarmkur/timer-cli/internal/recordcadence"
)

const pausedRenderInterval = time.Second

// Status describes how a foreground timer ended.
type Status string

const (
	Completed Status = "completed"
	Canceled  Status = "canceled"
)

// Renderer is implemented by terminal output modes.
type Renderer interface {
	Render(countdown.Snapshot) error
	Finish(countdown.Snapshot, string, string, time.Time) error
	Close() error
}

// Alert emits completion feedback.
type Alert interface {
	Ring() error
}

// Options are runtime dependencies. Ticks may be injected for deterministic tests.
type Options struct {
	Countdown      *countdown.Countdown
	Renderer       Renderer
	Alert          Alert
	Loop           bool
	Actions        <-chan keyboard.Action
	InputErrors    <-chan error
	SuspendSignals <-chan os.Signal
	Suspend        func() error
	Ticks          <-chan time.Time
	Interval       time.Duration
	RecordCadence  bool
	Message        string
}

// Run blocks until completion or intentional cancellation.
func Run(ctx context.Context, opts Options) (status Status, runErr error) {
	status = Canceled
	if opts.Renderer == nil {
		return status, fmt.Errorf("runner requires countdown, renderer, and alert")
	}
	var wakeTimer *time.Timer
	defer func() {
		stopTimer(wakeTimer)
		closeErr := opts.Renderer.Close()
		if closeErr == nil {
			return
		}
		wrapped := fmt.Errorf("close renderer: %w", closeErr)
		if runErr == nil {
			runErr = wrapped
			return
		}
		runErr = errors.Join(runErr, wrapped)
	}()
	if opts.Countdown == nil || opts.Alert == nil {
		return status, fmt.Errorf("runner requires countdown, renderer, and alert")
	}
	message := opts.Message
	if message == "" {
		message = "Time's up!"
	}

	for {
		if ctx.Err() != nil {
			return finishCanceled(opts, message)
		}

		snapshot, completedNow := opts.Countdown.Tick()
		if completedNow {
			result, terminate, restart, resultErr := resolveCompletionPreemption(ctx, &opts, message)
			if terminate {
				return result, resultErr
			}
			if restart {
				continue
			}
		}
		if err := opts.Renderer.Render(snapshot); err != nil {
			return status, fmt.Errorf("render timer: %w", err)
		}
		if completedNow {
			result, terminate, restart, resultErr := resolveCompletionPreemption(ctx, &opts, message)
			if terminate {
				return result, resultErr
			}
			if restart {
				continue
			}
			if err := opts.Renderer.Finish(snapshot, string(Completed), message, snapshot.ObservedAt); err != nil {
				return status, fmt.Errorf("render completion: %w", err)
			}
			if err := opts.Alert.Ring(); err != nil {
				return status, fmt.Errorf("completion alert: %w", err)
			}
			if !opts.Loop {
				return Completed, nil
			}
			opts.Countdown.Restart()
			continue
		}

		// Preserve the production guard for a paused timer whose input
		// sources are both gone. The periodic redraw wake cannot resume it.
		if snapshot.Paused && opts.Ticks == nil && opts.Actions == nil && opts.InputErrors == nil {
			return finishCanceled(opts, message)
		}

		wake := opts.Ticks
		if wake == nil {
			if delay := nextWakeDelay(snapshot, opts.Interval, opts.RecordCadence); delay > 0 {
				wakeTimer = resetTimer(wakeTimer, delay)
				wake = wakeTimer.C
			}
		}

		select {
		case <-ctx.Done():
			stopTimer(wakeTimer)
			return finishCanceled(opts, message)
		case inputErr, ok := <-opts.InputErrors:
			stopTimer(wakeTimer)
			if !ok {
				opts.InputErrors = nil
				continue
			}
			if inputErr != nil {
				return status, fmt.Errorf("keyboard input failed: %w", inputErr)
			}
		case action, ok := <-opts.Actions:
			stopTimer(wakeTimer)
			if !ok {
				opts.Actions = nil
				continue
			}
			quit, actionErr := handleAction(&opts, action)
			if actionErr != nil {
				return status, actionErr
			}
			if quit {
				return finishCanceled(opts, message)
			}
		case _, ok := <-opts.SuspendSignals:
			stopTimer(wakeTimer)
			if !ok {
				opts.SuspendSignals = nil
				continue
			}
			if err := suspend(&opts); err != nil {
				return status, err
			}
		case _, ok := <-wake:
			stopTimer(wakeTimer)
			if opts.Ticks != nil && !ok {
				return status, errors.New("runner tick channel closed")
			}
		}
	}
}

// preemptCompletion processes a bounded snapshot of events that were already
// queued when the countdown reached zero. A pending cancellation or quit wins
// the completion commit point, so it cannot race with the message or bell. A
// queued restart begins a fresh timer without completing the expired cycle;
// queued pause, add, and subtract are no-ops on a finished countdown and are
// discarded.
func preemptCompletion(ctx context.Context, opts *Options) (bool, error) {
	if ctx.Err() != nil {
		return true, nil
	}

	type completionEvent struct {
		kind     uint8
		action   keyboard.Action
		inputErr error
	}
	const (
		completionInputError uint8 = iota
		completionAction
		completionSuspend
	)

	// len reports the buffered events visible at the boundary. An otherwise
	// empty, non-nil channel gets one non-blocking probe so an already-waiting
	// unbuffered sender or a closed channel keeps its existing semantics. The
	// fixed budgets are captured before handlers run, preventing a handler or
	// producer from extending this boundary indefinitely with later events.
	inputBudget := 0
	if opts.InputErrors != nil {
		inputBudget = len(opts.InputErrors)
		if inputBudget == 0 {
			inputBudget = 1
		}
	}
	actionBudget := 0
	if opts.Actions != nil {
		actionBudget = len(opts.Actions)
		if actionBudget == 0 {
			actionBudget = 1
		}
	}
	suspendBudget := 0
	if opts.SuspendSignals != nil {
		suspendBudget = len(opts.SuspendSignals)
		if suspendBudget == 0 {
			suspendBudget = 1
		}
	}

	var events []completionEvent
capture:
	for inputBudget > 0 || actionBudget > 0 || suspendBudget > 0 {
		if ctx.Err() != nil {
			return true, nil
		}
		inputErrors := opts.InputErrors
		if inputBudget == 0 {
			inputErrors = nil
		}
		actions := opts.Actions
		if actionBudget == 0 {
			actions = nil
		}
		suspendSignals := opts.SuspendSignals
		if suspendBudget == 0 {
			suspendSignals = nil
		}
		select {
		case <-ctx.Done():
			return true, nil
		case inputErr, ok := <-inputErrors:
			inputBudget--
			if !ok {
				opts.InputErrors = nil
				inputBudget = 0
				continue
			}
			events = append(events, completionEvent{kind: completionInputError, inputErr: inputErr})
		case action, ok := <-actions:
			actionBudget--
			if !ok {
				opts.Actions = nil
				actionBudget = 0
				continue
			}
			events = append(events, completionEvent{kind: completionAction, action: action})
		case _, ok := <-suspendSignals:
			suspendBudget--
			if !ok {
				opts.SuspendSignals = nil
				suspendBudget = 0
				continue
			}
			events = append(events, completionEvent{kind: completionSuspend})
		default:
			break capture
		}
	}

	for _, event := range events {
		if ctx.Err() != nil {
			return true, nil
		}
		switch event.kind {
		case completionInputError:
			if event.inputErr != nil {
				return false, fmt.Errorf("keyboard input failed: %w", event.inputErr)
			}
		case completionAction:
			quit, actionErr := handleAction(opts, event.action)
			if actionErr != nil {
				return false, actionErr
			}
			if quit {
				return true, nil
			}
		case completionSuspend:
			if err := suspend(opts); err != nil {
				return false, err
			}
		}
	}
	if ctx.Err() != nil {
		return true, nil
	}
	return false, nil
}

// resolveCompletionPreemption drains queued input at a completion boundary.
// If terminate is true, the caller must return (result, err) as-is;
// otherwise restart reports a queued restart that began a fresh cycle before
// the expired one was reported.
func resolveCompletionPreemption(ctx context.Context, opts *Options, message string) (result Status, terminate, restart bool, err error) {
	preempted, preemptErr := preemptCompletion(ctx, opts)
	if preemptErr != nil {
		return Canceled, true, false, preemptErr
	}
	if preempted {
		result, err = finishCanceled(*opts, message)
		return result, true, false, err
	}
	return "", false, opts.Countdown.Snapshot().Remaining > 0, nil
}

// applyAction applies one interactive control. It reports whether the action
// requests termination (quit).
func applyAction(opts *Options, action keyboard.Action) bool {
	switch action {
	case keyboard.ActionTogglePause:
		opts.Countdown.TogglePause()
	case keyboard.ActionRestart:
		opts.Countdown.Restart()
	case keyboard.ActionAddMinute:
		opts.Countdown.Add(time.Minute)
	case keyboard.ActionSubtractMinute:
		opts.Countdown.Subtract(time.Minute)
	case keyboard.ActionQuit:
		return true
	}
	return false
}

func handleAction(opts *Options, action keyboard.Action) (bool, error) {
	if action == keyboard.ActionSuspend {
		return false, suspend(opts)
	}
	return applyAction(opts, action), nil
}

func suspend(opts *Options) error {
	if opts.Suspend == nil {
		return errors.New("suspend timer: suspension is unavailable")
	}
	if err := opts.Suspend(); err != nil {
		return fmt.Errorf("suspend timer: %w", err)
	}
	return nil
}

func finishCanceled(opts Options, message string) (Status, error) {
	snapshot := opts.Countdown.Snapshot()
	if err := opts.Renderer.Finish(snapshot, string(Canceled), message, snapshot.ObservedAt); err != nil {
		return Canceled, fmt.Errorf("render cancellation: %w", err)
	}
	return Canceled, nil
}

func nextWakeDelay(snapshot countdown.Snapshot, interval time.Duration, recordCadence bool) time.Duration {
	if snapshot.Remaining <= 0 {
		return 0
	}
	if snapshot.Paused {
		// TTY renderers use this low-frequency wake to detect resizes;
		// unchanged TTY frames and redirected records suppress output.
		return pausedRenderInterval
	}
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	if recordCadence {
		return recordcadence.NextBoundary(snapshot.Total, snapshot.Remaining)
	}
	if interval >= time.Second {
		return min(time.Second, snapshot.Remaining)
	}

	progressInterval := snapshot.Total / 100
	progressInterval = max(progressInterval, interval)
	progressInterval = min(progressInterval, time.Second)

	displayBoundary := snapshot.Remaining % time.Second
	if displayBoundary == 0 {
		displayBoundary = time.Second
	}
	displayBoundary = max(displayBoundary, interval)

	return min(progressInterval, displayBoundary, time.Second, snapshot.Remaining)
}

func resetTimer(timer *time.Timer, delay time.Duration) *time.Timer {
	if timer == nil {
		return time.NewTimer(delay)
	}
	stopTimer(timer)
	timer.Reset(delay)
	return timer
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
