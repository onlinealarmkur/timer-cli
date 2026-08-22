package keyboard

import (
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func TestDecoderEmitsEveryActionForArbitraryChunks(t *testing.T) {
	t.Parallel()
	want := []Action{
		ActionTogglePause,
		ActionRestart,
		ActionAddMinute,
		ActionSubtractMinute,
		ActionQuit,
	}
	tests := []struct {
		name   string
		chunks [][]byte
	}{
		{name: "one read", chunks: [][]byte{[]byte(" r+-q")}},
		{name: "mixed reads", chunks: [][]byte{[]byte(" r"), []byte("+-"), []byte("q")}},
		{name: "one byte reads", chunks: byteChunks([]byte(" r+-q"))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := decodeChunks(test.chunks); !reflect.DeepEqual(got, want) {
				t.Fatalf("actions = %v, want %v", got, want)
			}
		})
	}
}

func TestDecoderIgnoresFragmentedTerminalSequences(t *testing.T) {
	t.Parallel()
	sequences := map[string][]byte{
		"CSI with parameters": []byte("\x1b[1;5A"),
		"SS3":                 []byte("\x1bOP"),
	}
	for name, sequence := range sequences {
		name, sequence := name, sequence
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			shapes := [][][]byte{
				{sequence},
				byteChunks(sequence),
			}
			for split := 1; split < len(sequence); split++ {
				shapes = append(shapes, [][]byte{sequence[:split], sequence[split:]})
			}
			for index, chunks := range shapes {
				chunks = append(chunks, []byte("q"))
				if got, want := decodeChunks(chunks), []Action{ActionQuit}; !reflect.DeepEqual(got, want) {
					t.Errorf("shape %d actions = %v, want %v", index, got, want)
				}
			}
		})
	}
}

func TestDecoderPendingEscapeBeforeNormalByte(t *testing.T) {
	t.Parallel()
	var d decoder
	if got := d.feed([]byte("\x1b")); len(got) != 0 {
		t.Fatalf("initial escape actions = %v, want none", got)
	}
	if got := d.feed([]byte("r")); len(got) != 0 {
		t.Fatalf("alt-modified key actions = %v, want none", got)
	}
	want := []Action{ActionRestart}
	if got := d.feed([]byte("r")); !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}

func TestDecoderAltModifiedKeysAreIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		b    byte
	}{
		{name: "Alt+B", b: 'b'},
		{name: "Alt+Q", b: 'q'},
		{name: "OSC start", b: ']'},
		{name: "DCS start", b: 'P'},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var d decoder
			if got := d.feed([]byte("\x1b")); len(got) != 0 {
				t.Fatalf("initial escape actions = %v, want none", got)
			}
			if got := d.feed([]byte{test.b}); len(got) != 0 {
				t.Fatalf("alt-modified key actions = %v, want none", got)
			}
			if action := actionForByte(test.b); action != ActionNone {
				want := []Action{action}
				if got := d.feed([]byte{test.b}); !reflect.DeepEqual(got, want) {
					t.Fatalf("plain key actions = %v, want %v", got, want)
				}
			}
		})
	}
}

func TestDecoderLoneEscapeFlushesToQuit(t *testing.T) {
	t.Parallel()
	var d decoder
	if got := d.feed([]byte("\x1b")); len(got) != 0 {
		t.Fatalf("initial escape actions = %v, want none", got)
	}
	if got, ok := d.flushEscape(); !ok || got != ActionQuit {
		t.Fatalf("flushed escape = %v, %v; want %v, true", got, ok, ActionQuit)
	}
}

func TestDecoderLateCSIAndSS3TailsIgnoreEscapeFlush(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
		final  byte
	}{
		{name: "CSI", prefix: "\x1b[", final: 'R'},
		{name: "SS3", prefix: "\x1bO", final: 'R'},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var d decoder
			if got := d.feed([]byte(test.prefix)); len(got) != 0 {
				t.Fatalf("prefix actions = %v, want none", got)
			}
			// The dispatcher's escape timeout calls flushEscape when it
			// fires; a pending CSI/SS3 sequence must not be reset by it.
			if action, flush := d.flushEscape(); flush {
				t.Fatalf("flushEscape flushed = %v while sequence pending, want no flush", action)
			}
			if got := d.feed([]byte{test.final}); len(got) != 0 {
				t.Fatalf("late sequence terminator actions = %v, want none", got)
			}
			want := []Action{ActionRestart}
			if got := d.feed([]byte("r")); !reflect.DeepEqual(got, want) {
				t.Fatalf("actions after completed sequence = %v, want %v", got, want)
			}
		})
	}
}

func TestDecoderRepeatedEscapePreservesBothQuitActions(t *testing.T) {
	t.Parallel()
	var d decoder
	if got := d.feed([]byte("\x1b")); len(got) != 0 {
		t.Fatalf("initial escape actions = %v, want none", got)
	}
	if got, want := d.feed([]byte("\x1b")), []Action{ActionQuit}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second escape actions = %v, want %v", got, want)
	}
	if got, ok := d.flushEscape(); !ok || got != ActionQuit {
		t.Fatalf("flushed escape = %v, %v; want %v, true", got, ok, ActionQuit)
	}
}

func TestDecoderCtrlCQuitsImmediately(t *testing.T) {
	t.Parallel()
	var d decoder
	if got, want := d.feed([]byte{0x03}), []Action{ActionQuit}; !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	if d.escapeFlushPending() {
		t.Fatal("Ctrl+C left an escape timeout pending")
	}
}

func TestDecoderCtrlZSuspendsImmediately(t *testing.T) {
	t.Parallel()
	var d decoder
	if got, want := d.feed([]byte{0x1a}), []Action{ActionSuspend}; !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	if d.escapeFlushPending() {
		t.Fatal("Ctrl+Z left an escape timeout pending")
	}
}

func TestDecoderCtrlCQuitsFromIncompleteSequence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		chunks [][]byte
	}{
		{name: "CSI in same chunk", chunks: [][]byte{[]byte("\x1b[\x03")}},
		{name: "CSI in later chunk", chunks: [][]byte{[]byte("\x1b["), {0x03}}},
		{name: "SS3 in same chunk", chunks: [][]byte{[]byte("\x1bO\x03")}},
		{name: "SS3 in later chunk", chunks: [][]byte{[]byte("\x1bO"), {0x03}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var d decoder
			var got []Action
			for _, chunk := range test.chunks {
				got = append(got, d.feed(chunk)...)
			}
			if want := []Action{ActionQuit}; !reflect.DeepEqual(got, want) {
				t.Fatalf("actions = %v, want %v", got, want)
			}
			if d.state != stateNormal {
				t.Fatalf("state = %v, want %v", d.state, stateNormal)
			}
			if d.escapeFlushPending() {
				t.Fatal("Ctrl+C left an escape timeout pending")
			}
			if got, want := d.feed([]byte("r")), []Action{ActionRestart}; !reflect.DeepEqual(got, want) {
				t.Fatalf("actions after Ctrl+C = %v, want %v", got, want)
			}
		})
	}
}

func TestDecoderCtrlZSuspendsFromIncompleteSequence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		chunks [][]byte
	}{
		{name: "CSI in same chunk", chunks: [][]byte{[]byte("\x1b[\x1a")}},
		{name: "CSI in later chunk", chunks: [][]byte{[]byte("\x1b["), {0x1a}}},
		{name: "SS3 in same chunk", chunks: [][]byte{[]byte("\x1bO\x1a")}},
		{name: "SS3 in later chunk", chunks: [][]byte{[]byte("\x1bO"), {0x1a}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var d decoder
			var got []Action
			for _, chunk := range test.chunks {
				got = append(got, d.feed(chunk)...)
			}
			if want := []Action{ActionSuspend}; !reflect.DeepEqual(got, want) {
				t.Fatalf("actions = %v, want %v", got, want)
			}
			if d.state != stateNormal {
				t.Fatalf("state = %v, want %v", d.state, stateNormal)
			}
			if got, want := d.feed([]byte("r")), []Action{ActionRestart}; !reflect.DeepEqual(got, want) {
				t.Fatalf("actions after Ctrl+Z = %v, want %v", got, want)
			}
		})
	}
}

func FuzzDecoderEmitsOnlyKnownActions(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(" r+-q"), []byte("\x1b[1;5A"), []byte("\x1b"), {0x03}, {0xff, 0x00},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		var d decoder
		actions := d.feed(input)
		if action, flush := d.flushEscape(); flush {
			actions = append(actions, action)
		}
		for _, action := range actions {
			if action <= ActionNone || action > ActionQuit {
				t.Fatalf("decoder emitted unknown action %d for %q", action, input)
			}
		}
	})
}

func TestOpenRejectsNilAndIgnoresNonTerminal(t *testing.T) {
	t.Parallel()
	if controller, err := Open(nil); controller != nil || err == nil || !strings.Contains(err.Error(), "file is required") {
		t.Fatalf("Open(nil) = %+v, %v", controller, err)
	}

	file, err := os.CreateTemp(t.TempDir(), "keyboard-input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	controller, err := Open(file)
	if err != nil || controller != nil {
		t.Fatalf("Open(non-terminal) = %+v, %v", controller, err)
	}
}

func TestOpenTerminalReadsActionsAndRestoresState(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})

	fd := int(slave.Fd())
	before, err := term.GetState(fd)
	if err != nil {
		t.Fatalf("read initial terminal state: %v", err)
	}
	controller, err := Open(slave)
	if err != nil {
		t.Fatalf("Open(terminal): %v", err)
	}
	if controller == nil {
		t.Fatal("Open(terminal) returned a nil controller")
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = controller.Close()
		}
	})

	raw, err := term.GetState(fd)
	if err != nil {
		t.Fatalf("read raw terminal state: %v", err)
	}
	if reflect.DeepEqual(raw, before) {
		t.Fatal("Open(terminal) did not enable raw mode")
	}
	if _, err := master.Write([]byte("q")); err != nil {
		t.Fatalf("write keyboard action: %v", err)
	}
	select {
	case action, ok := <-controller.Actions():
		if !ok || action != ActionQuit {
			t.Fatalf("Actions() = %v, %v; want %v, true", action, ok, ActionQuit)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for terminal keyboard action")
	}
	if err := controller.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	closed = true
	restored, err := term.GetState(fd)
	if err != nil {
		t.Fatalf("read restored terminal state: %v", err)
	}
	if !reflect.DeepEqual(restored, before) {
		t.Fatalf("terminal state was not restored: got %#v, want %#v", restored, before)
	}
}

func TestControllerReportsUnexpectedInputFailures(t *testing.T) {
	pollFailure := errors.New("poll unavailable")
	readFailure := errors.New("read unavailable")
	tests := []struct {
		name      string
		configure func(*Controller)
		wantError error
		wantText  string
	}{
		{
			name: "poll error",
			configure: func(c *Controller) {
				c.poll = func([]unix.PollFd, int) (int, error) { return 0, pollFailure }
			},
			wantError: pollFailure,
			wantText:  "poll keyboard input",
		},
		{
			name: "invalid input descriptor",
			configure: func(c *Controller) {
				c.poll = func(fds []unix.PollFd, _ int) (int, error) {
					fds[0].Revents = unix.POLLNVAL
					return 1, nil
				}
			},
			wantText: "poll keyboard input: invalid file descriptor",
		},
		{
			name: "input poll error event",
			configure: func(c *Controller) {
				c.poll = func(fds []unix.PollFd, _ int) (int, error) {
					fds[0].Revents = unix.POLLERR
					return 1, nil
				}
			},
			wantText: "poll keyboard input: device error",
		},
		{
			name: "invalid wake descriptor",
			configure: func(c *Controller) {
				c.poll = func(fds []unix.PollFd, _ int) (int, error) {
					fds[1].Revents = unix.POLLNVAL
					return 1, nil
				}
			},
			wantText: "poll keyboard wake pipe: invalid file descriptor",
		},
		{
			name: "wake poll error event",
			configure: func(c *Controller) {
				c.poll = func(fds []unix.PollFd, _ int) (int, error) {
					fds[1].Revents = unix.POLLERR
					return 1, nil
				}
			},
			wantText: "poll keyboard wake pipe: device error",
		},
		{
			name: "read error",
			configure: func(c *Controller) {
				c.poll = func(fds []unix.PollFd, _ int) (int, error) {
					fds[0].Revents = unix.POLLIN
					return 1, nil
				}
				c.readInput = func([]byte) (int, error) { return 0, readFailure }
			},
			wantError: readFailure,
			wantText:  "read keyboard input",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := newTestController(t)
			test.configure(c)
			c.start()

			select {
			case got, ok := <-c.Errors():
				if !ok || got == nil || !strings.Contains(got.Error(), test.wantText) {
					t.Fatalf("Errors() = %v, %v, want error containing %q", got, ok, test.wantText)
				}
				if test.wantError != nil && !errors.Is(got, test.wantError) {
					t.Fatalf("Errors() = %v, want wrapped %v", got, test.wantError)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for input error")
			}
			if _, ok := <-c.Errors(); ok {
				t.Fatal("error channel remained open after reader failure")
			}
			if _, ok := <-c.Actions(); ok {
				t.Fatal("actions channel remained open after reader failure")
			}
		})
	}
}

func TestControllerWakeHangupClosesChannelsWithoutError(t *testing.T) {
	c := newTestController(t)
	c.poll = func(fds []unix.PollFd, _ int) (int, error) {
		fds[1].Revents = unix.POLLHUP
		return 1, nil
	}
	c.start()

	select {
	case inputErr, ok := <-c.Errors():
		if ok {
			t.Fatalf("Errors() = %v, want clean closure", inputErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for error channel to close")
	}
	select {
	case action, ok := <-c.Actions():
		if ok {
			t.Fatalf("Actions() = %v, want clean closure", action)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for actions channel to close")
	}
}

func TestControllerCleanEOFAndInterruptedPollReportNoError(t *testing.T) {
	c := newTestController(t)
	pollCalls := 0
	c.poll = func(fds []unix.PollFd, _ int) (int, error) {
		pollCalls++
		if pollCalls == 1 {
			return 0, unix.EINTR
		}
		fds[0].Revents = unix.POLLHUP
		return 1, nil
	}
	c.readInput = func([]byte) (int, error) { return 0, io.EOF }
	c.start()

	if inputErr, ok := <-c.Errors(); ok {
		t.Fatalf("Errors() = %v, want clean closure", inputErr)
	}
	if pollCalls != 2 {
		t.Fatalf("poll calls = %d, want 2", pollCalls)
	}
}

func TestControllerCloseSuppressesConcurrentPollFailure(t *testing.T) {
	c := newTestController(t)
	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	c.poll = func([]unix.PollFd, int) (int, error) {
		close(pollStarted)
		<-releasePoll
		return 0, errors.New("poll failed during shutdown")
	}
	c.start()
	<-pollStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not begin shutdown")
	}
	close(releasePoll)
	if closeErr := <-closeDone; closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if inputErr, ok := <-c.Errors(); ok {
		t.Fatalf("Errors() = %v during deliberate close, want clean closure", inputErr)
	}
}

func TestDispatcherLoneEscapeUsesInjectedTimeout(t *testing.T) {
	t.Parallel()
	timeout := make(chan time.Time, 1)
	timeoutCalls := make(chan time.Duration, 1)
	c := &Controller{
		actions: make(chan Action, 8),
		input:   make(chan []byte),
		done:    make(chan struct{}),
		timeout: func(delay time.Duration) <-chan time.Time {
			timeoutCalls <- delay
			return timeout
		},
	}
	c.wg.Add(1)
	go c.dispatch()

	c.input <- []byte("\x1b")
	if got := <-timeoutCalls; got != escapeSequenceTimeout {
		t.Fatalf("escape timeout = %v, want %v", got, escapeSequenceTimeout)
	}
	select {
	case action := <-c.actions:
		t.Fatalf("action before timeout = %v", action)
	default:
	}

	timeout <- time.Time{}
	if got := <-c.actions; got != ActionQuit {
		t.Fatalf("timeout action = %v, want %v", got, ActionQuit)
	}
	close(c.input)
	c.wg.Wait()
}

func TestDispatcherIncompleteEscapeSequenceNeverArmsTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
	}{
		{name: "CSI", prefix: "\x1b["},
		{name: "SS3", prefix: "\x1bO"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			timeoutCalls := make(chan time.Duration, 1)
			c := &Controller{
				actions: make(chan Action, 8),
				input:   make(chan []byte),
				done:    make(chan struct{}),
				timeout: func(delay time.Duration) <-chan time.Time {
					timeoutCalls <- delay
					return make(chan time.Time)
				},
			}
			c.wg.Add(1)
			go c.dispatch()

			c.input <- []byte(test.prefix)
			close(c.input)
			c.wg.Wait()

			select {
			case delay := <-timeoutCalls:
				t.Fatalf("escape timeout armed for incomplete %s sequence: %v", test.name, delay)
			default:
			}
			if action, ok := <-c.actions; ok {
				t.Fatalf("incomplete %s sequence action = %v, want none", test.name, action)
			}
		})
	}
}

func TestDispatcherLateCSIAndSS3TailsCompleteSequenceWithoutFalseActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
		final  byte
	}{
		{name: "CSI", prefix: "\x1b[", final: 'A'},
		{name: "SS3", prefix: "\x1bO", final: 'P'},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			timeoutCalls := make(chan time.Duration, 1)
			c := &Controller{
				actions: make(chan Action, 8),
				input:   make(chan []byte),
				done:    make(chan struct{}),
				timeout: func(delay time.Duration) <-chan time.Time {
					timeoutCalls <- delay
					return make(chan time.Time)
				},
			}
			c.wg.Add(1)
			go c.dispatch()

			// The final byte arrives well after what used to be the 30ms
			// timeout window; with the timeout reset removed there is no
			// clock racing the sequence, so this must still resolve as one
			// completed CSI/SS3 sequence, not a fresh command byte.
			c.input <- []byte(test.prefix)
			c.input <- []byte{test.final}
			c.input <- []byte("r")

			select {
			case action := <-c.actions:
				if action != ActionRestart {
					t.Fatalf("action after completed sequence = %v, want %v", action, ActionRestart)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for action after completed sequence")
			}
			close(c.input)
			c.wg.Wait()
			if action, ok := <-c.actions; ok {
				t.Fatalf("unexpected extra action = %v", action)
			}
			select {
			case delay := <-timeoutCalls:
				t.Fatalf("escape timeout armed for incomplete %s sequence: %v", test.name, delay)
			default:
			}
		})
	}
}

func TestDispatcherCloseWhileEscapeSequencePending(t *testing.T) {
	t.Parallel()
	timeoutCalls := make(chan time.Duration, 1)
	c := &Controller{
		actions: make(chan Action, 8),
		input:   make(chan []byte),
		done:    make(chan struct{}),
		timeout: func(delay time.Duration) <-chan time.Time {
			timeoutCalls <- delay
			return make(chan time.Time)
		},
	}
	c.wg.Add(1)
	go c.dispatch()

	c.input <- []byte("\x1b[")
	close(c.done)
	c.wg.Wait()
	if action, ok := <-c.actions; ok {
		t.Fatalf("action while closing with sequence pending = %v, want none", action)
	}
	select {
	case delay := <-timeoutCalls:
		t.Fatalf("escape timeout armed for incomplete CSI sequence: %v", delay)
	default:
	}
}

func TestControllerIdleCloseIsIdempotentAndKeepsCallerFileOpen(t *testing.T) {
	original, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = original.Close()
		_ = writer.Close()
	})

	duplicateFD, err := unix.Dup(int(original.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(duplicateFD), original.Name())
	if reader == nil {
		_ = unix.Close(duplicateFD)
		t.Fatal("os.NewFile returned nil")
	}

	var restoreCalls atomic.Int32
	restore := func(int, *term.State) error {
		restoreCalls.Add(1)
		return nil
	}
	c, err := newController(original, reader, nil, restore, time.After, os.Pipe)
	if err != nil {
		t.Fatal(err)
	}
	readStarted := make(chan struct{})
	c.readStarted = readStarted
	c.start()
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("idle reader did not start")
	}
	wakeRead, wakeWrite := c.wakeRead, c.wakeWrite

	const closers = 8
	var wg sync.WaitGroup
	errs := make(chan error, closers)
	for range closers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Close()
		}()
	}
	closed := make(chan struct{})
	go func() {
		wg.Wait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Close calls did not return within five seconds")
	}
	close(errs)
	for closeErr := range errs {
		if closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}
	if got := restoreCalls.Load(); got != 1 {
		t.Fatalf("restore calls = %d, want 1", got)
	}
	if _, ok := <-c.Actions(); ok {
		t.Fatal("actions channel remained open")
	}
	if inputErr, ok := <-c.Errors(); ok {
		t.Fatalf("Errors() = %v after deliberate close, want clean closure", inputErr)
	}
	assertFileClosed(t, reader, "duplicated input reader")
	assertFileClosed(t, wakeRead, "wake reader")
	assertFileClosed(t, wakeWrite, "wake writer")
	flags, err := unix.FcntlInt(original.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("read caller file status: %v", err)
	}
	if flags&unix.O_NONBLOCK != 0 {
		t.Fatal("controller made the caller-owned file nonblocking")
	}

	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("write after controller close: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := original.Read(buf); err != nil {
		t.Fatalf("caller file was closed: %v", err)
	}
	if got := string(buf); got != "x" {
		t.Fatalf("caller file read %q, want x", got)
	}
}

func TestControllerCloseCachesRestoreFailureAndKeepsCallerFileOpen(t *testing.T) {
	original, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = original.Close()
		_ = writer.Close()
	})

	duplicateFD, err := unix.Dup(int(original.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(duplicateFD), original.Name())
	if reader == nil {
		_ = unix.Close(duplicateFD)
		t.Fatal("os.NewFile returned nil")
	}

	restoreErr := errors.New("restore unavailable")
	var restoreCalls atomic.Int32
	restore := func(int, *term.State) error {
		restoreCalls.Add(1)
		return restoreErr
	}
	c, err := newController(original, reader, &term.State{}, restore, time.After, os.Pipe)
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.raw = true
	readStarted := make(chan struct{})
	c.readStarted = readStarted
	c.start()
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("idle reader did not start")
	}
	wakeRead, wakeWrite := c.wakeRead, c.wakeWrite

	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	var closeErr error
	select {
	case closeErr = <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within five seconds")
	}
	if !errors.Is(closeErr, restoreErr) {
		t.Fatalf("Close() error = %v, want wrapped %v", closeErr, restoreErr)
	}
	if !strings.Contains(closeErr.Error(), "restore keyboard terminal state") {
		t.Fatalf("Close() error = %v, want restoration context", closeErr)
	}
	if secondErr := c.Close(); secondErr != closeErr {
		t.Fatalf("second Close() error = %v, want cached %v", secondErr, closeErr)
	}
	if got := restoreCalls.Load(); got != 1 {
		t.Fatalf("restore calls = %d, want 1", got)
	}
	if inputErr, ok := <-c.Errors(); ok {
		t.Fatalf("Errors() = %v after deliberate close, want clean closure", inputErr)
	}
	if _, ok := <-c.Actions(); ok {
		t.Fatal("actions channel remained open")
	}
	assertFileClosed(t, reader, "duplicated input reader")
	assertFileClosed(t, wakeRead, "wake reader")
	assertFileClosed(t, wakeWrite, "wake writer")

	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("write after controller close: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := original.Read(buf); err != nil {
		t.Fatalf("caller file was closed: %v", err)
	}
	if got := string(buf); got != "x" {
		t.Fatalf("caller file read %q, want x", got)
	}
}

func TestControllerSuspendResumeLifecycleIsIdempotent(t *testing.T) {
	c := newTestController(t)
	initialState := &term.State{}
	resumedState := &term.State{}
	c.state = initialState
	var restoreCalls, makeRawCalls int
	var restoredStates []*term.State
	c.restore = func(_ int, state *term.State) error {
		restoreCalls++
		restoredStates = append(restoredStates, state)
		return nil
	}
	c.makeRaw = func(int) (*term.State, error) {
		makeRawCalls++
		return resumedState, nil
	}

	if err := c.Suspend(); err != nil {
		t.Fatalf("Suspend(): %v", err)
	}
	if err := c.Suspend(); err != nil {
		t.Fatalf("second Suspend(): %v", err)
	}
	if restoreCalls != 1 {
		t.Fatalf("restore calls after suspension = %d, want 1", restoreCalls)
	}
	if err := c.Resume(); err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	if err := c.Resume(); err != nil {
		t.Fatalf("second Resume(): %v", err)
	}
	if makeRawCalls != 1 {
		t.Fatalf("make-raw calls after resume = %d, want 1", makeRawCalls)
	}
	if c.state != resumedState {
		t.Fatalf("saved terminal state = %p, want resumed shell state %p", c.state, resumedState)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if restoreCalls != 2 {
		t.Fatalf("restore calls after close = %d, want 2", restoreCalls)
	}
	if restoredStates[0] != initialState || restoredStates[1] != resumedState {
		t.Fatalf("restored terminal states = [%p %p], want [%p %p]", restoredStates[0], restoredStates[1], initialState, resumedState)
	}
	if err := c.Resume(); err == nil || !strings.Contains(err.Error(), "controller is closed") {
		t.Fatalf("Resume() after Close() error = %v, want closed-controller error", err)
	}
}

func TestControllerCloseWhileSuspendedDoesNotRestoreTwice(t *testing.T) {
	c := newTestController(t)
	restoreCalls := 0
	c.restore = func(int, *term.State) error {
		restoreCalls++
		return nil
	}
	if err := c.Suspend(); err != nil {
		t.Fatalf("Suspend(): %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if restoreCalls != 1 {
		t.Fatalf("restore calls = %d, want 1", restoreCalls)
	}
}

func TestControllerSuspendAndResumeFailuresCanBeRetried(t *testing.T) {
	t.Run("suspend restore", func(t *testing.T) {
		c := newTestController(t)
		restoreErr := errors.New("restore failed")
		c.restore = func(int, *term.State) error { return restoreErr }
		if err := c.Suspend(); !errors.Is(err, restoreErr) {
			t.Fatalf("Suspend() = %v, want %v", err, restoreErr)
		}
		c.restore = func(int, *term.State) error { return nil }
		if err := c.Suspend(); err != nil {
			t.Fatalf("retried Suspend(): %v", err)
		}
	})

	t.Run("resume raw mode", func(t *testing.T) {
		c := newTestController(t)
		c.restore = func(int, *term.State) error { return nil }
		if err := c.Suspend(); err != nil {
			t.Fatalf("Suspend(): %v", err)
		}
		makeRawErr := errors.New("make raw failed")
		c.makeRaw = func(int) (*term.State, error) { return nil, makeRawErr }
		if err := c.Resume(); !errors.Is(err, makeRawErr) {
			t.Fatalf("Resume() = %v, want %v", err, makeRawErr)
		}
		c.makeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
		if err := c.Resume(); err != nil {
			t.Fatalf("retried Resume(): %v", err)
		}
	})
}

func newTestController(t *testing.T) *Controller {
	t.Helper()
	file, fileWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader, readerWriter, err := os.Pipe()
	if err != nil {
		_ = file.Close()
		_ = fileWriter.Close()
		t.Fatal(err)
	}
	c, err := newController(file, reader, nil, func(int, *term.State) error { return nil }, time.After, os.Pipe)
	if err != nil {
		_ = file.Close()
		_ = fileWriter.Close()
		_ = readerWriter.Close()
		t.Fatal(err)
	}
	if cap(c.errs) != 1 {
		t.Fatalf("error channel capacity = %d, want 1", cap(c.errs))
	}
	t.Cleanup(func() {
		if closeErr := c.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
		_ = file.Close()
		_ = fileWriter.Close()
		_ = readerWriter.Close()
	})
	return c
}

func TestNewControllerWakePipeFailureCleansUpAndRestores(t *testing.T) {
	original, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = original.Close()
		_ = writer.Close()
	})

	duplicateFD, err := unix.Dup(int(original.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(duplicateFD), original.Name())
	if reader == nil {
		_ = unix.Close(duplicateFD)
		t.Fatal("os.NewFile returned nil")
	}
	wakeRead, wakeWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pipeErr := errors.New("wake pipe unavailable")
	restoreErr := errors.New("restore unavailable")
	var restoreCalls atomic.Int32
	restore := func(int, *term.State) error {
		restoreCalls.Add(1)
		return restoreErr
	}
	controller, err := newController(original, reader, nil, restore, time.After, func() (*os.File, *os.File, error) {
		return wakeRead, wakeWrite, pipeErr
	})
	if controller != nil {
		t.Fatal("newController returned a controller after wake-pipe failure")
	}
	if !errors.Is(err, pipeErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("newController error = %v, want pipe and restore errors", err)
	}
	if got := restoreCalls.Load(); got != 1 {
		t.Fatalf("restore calls = %d, want 1", got)
	}
	assertFileClosed(t, reader, "duplicated input reader")
	assertFileClosed(t, wakeRead, "wake reader")
	assertFileClosed(t, wakeWrite, "wake writer")

	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("write after constructor failure: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := original.Read(buf); err != nil {
		t.Fatalf("caller file was closed after constructor failure: %v", err)
	}
	if got := string(buf); got != "x" {
		t.Fatalf("caller file read %q, want x", got)
	}
}

func TestNewControllerRejectsNilWakePipe(t *testing.T) {
	original, originalWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader, readerWriter, err := os.Pipe()
	if err != nil {
		_ = original.Close()
		_ = originalWriter.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = original.Close()
		_ = originalWriter.Close()
		_ = readerWriter.Close()
	})

	restoreCalls := 0
	controller, err := newController(original, reader, nil, func(int, *term.State) error {
		restoreCalls++
		return nil
	}, time.After, func() (*os.File, *os.File, error) {
		return nil, nil, nil
	})
	if controller != nil || err == nil || !strings.Contains(err.Error(), "invalid wake pipe") {
		t.Fatalf("newController() = %+v, %v", controller, err)
	}
	if restoreCalls != 1 {
		t.Fatalf("restore calls = %d, want 1", restoreCalls)
	}
	assertFileClosed(t, reader, "keyboard reader")
}

func decodeChunks(chunks [][]byte) []Action {
	var d decoder
	var actions []Action
	for _, chunk := range chunks {
		actions = append(actions, d.feed(chunk)...)
	}
	return actions
}

func byteChunks(input []byte) [][]byte {
	chunks := make([][]byte, 0, len(input))
	for index := range input {
		chunks = append(chunks, input[index:index+1])
	}
	return chunks
}

func assertFileClosed(t *testing.T, file *os.File, name string) {
	t.Helper()
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("%s remained open: %v", name, err)
	}
}
