// Package keyboard translates raw terminal input into countdown actions.
package keyboard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	escapeSequenceTimeout = 30 * time.Millisecond
)

// Action is a supported interactive command.
type Action int

const (
	ActionNone Action = iota
	ActionTogglePause
	ActionRestart
	ActionAddMinute
	ActionSubtractMinute
	ActionSuspend
	ActionQuit
)

type decoderState uint8

const (
	stateNormal decoderState = iota
	statePendingEscape
	stateCSI
	stateSS3
)

type decoder struct {
	state decoderState
}

func (d *decoder) feed(input []byte) []Action {
	var actions []Action
	for _, b := range input {
		if b == 0x03 || b == 0x1a {
			d.state = stateNormal
			actions = append(actions, actionForByte(b))
			continue
		}
		switch d.state {
		case stateNormal:
			if b == 0x1b {
				d.state = statePendingEscape
				continue
			}
			if action := actionForByte(b); action != ActionNone {
				actions = append(actions, action)
			}
		case statePendingEscape:
			switch b {
			case '[':
				d.state = stateCSI
			case 'O':
				d.state = stateSS3
			default:
				if b == 0x1b {
					actions = append(actions, ActionQuit)
					d.state = statePendingEscape
				} else {
					d.state = stateNormal
				}
			}
		case stateCSI, stateSS3:
			if b >= 0x40 && b <= 0x7e {
				d.state = stateNormal
			}
		}
	}
	return actions
}

func (d *decoder) flushEscape() (Action, bool) {
	if d.state != statePendingEscape {
		return ActionNone, false
	}
	d.state = stateNormal
	return ActionQuit, true
}

func (d *decoder) escapeFlushPending() bool {
	return d.state == statePendingEscape
}

func actionForByte(b byte) Action {
	switch b {
	case 0x03: // Ctrl+C arrives as a byte while the terminal is in raw mode.
		return ActionQuit
	case 0x1a: // Ctrl+Z must be decoded explicitly while ISIG is disabled.
		return ActionSuspend
	case ' ':
		return ActionTogglePause
	case 'r', 'R':
		return ActionRestart
	case '+', '=':
		return ActionAddMinute
	case '-', '_':
		return ActionSubtractMinute
	case 'q', 'Q':
		return ActionQuit
	default:
		return ActionNone
	}
}

type restoreFunc func(int, *term.State) error
type makeRawFunc func(int) (*term.State, error)
type timeoutFunc func(time.Duration) <-chan time.Time
type pipeFunc func() (*os.File, *os.File, error)
type pollFunc func([]unix.PollFd, int) (int, error)
type readFunc func([]byte) (int, error)

// Controller owns raw-mode input and restores it when closed.
type Controller struct {
	file        *os.File
	reader      *os.File
	wakeRead    *os.File
	wakeWrite   *os.File
	state       *term.State
	restore     restoreFunc
	makeRaw     makeRawFunc
	timeout     timeoutFunc
	poll        pollFunc
	readInput   readFunc
	actions     chan Action
	errs        chan error
	input       chan []byte
	done        chan struct{}
	wg          sync.WaitGroup
	once        sync.Once
	shutdownMu  sync.Mutex
	terminalMu  sync.Mutex
	raw         bool
	closed      bool
	closeErr    error
	readStarted chan struct{}
}

// Open puts a terminal in raw mode and starts reading controls.
func Open(file *os.File) (*Controller, error) {
	if file == nil {
		return nil, errors.New("keyboard input file is required")
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return nil, nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("enable keyboard controls: %w", err)
	}
	duplicateFD, err := unix.Dup(fd)
	if err != nil {
		restoreErr := term.Restore(fd, state)
		return nil, errors.Join(
			fmt.Errorf("duplicate keyboard input: %w", err),
			wrapRestoreError(restoreErr),
		)
	}
	reader := os.NewFile(uintptr(duplicateFD), file.Name())
	if reader == nil {
		_ = unix.Close(duplicateFD)
		restoreErr := term.Restore(fd, state)
		return nil, errors.Join(
			fmt.Errorf("duplicate keyboard input: invalid file descriptor"),
			wrapRestoreError(restoreErr),
		)
	}

	c, err := newController(file, reader, state, term.Restore, time.After, os.Pipe)
	if err != nil {
		return nil, err
	}
	c.start()
	return c, nil
}

func newController(file, reader *os.File, state *term.State, restore restoreFunc, timeout timeoutFunc, makePipe pipeFunc) (*Controller, error) {
	wakeRead, wakeWrite, err := makePipe()
	if err == nil && (wakeRead == nil || wakeWrite == nil) {
		err = errors.New("invalid wake pipe")
	}
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open keyboard wake pipe: %w", err),
			closeFileError(wakeWrite, "close keyboard wake writer"),
			closeFileError(wakeRead, "close keyboard wake reader"),
			closeFileError(reader, "close keyboard reader"),
			wrapRestoreError(restore(int(file.Fd()), state)),
		)
	}
	return &Controller{
		file: file, reader: reader, wakeRead: wakeRead, wakeWrite: wakeWrite,
		state: state, restore: restore, makeRaw: term.MakeRaw, timeout: timeout, poll: unix.Poll, readInput: reader.Read,
		actions: make(chan Action, 8), errs: make(chan error, 1), input: make(chan []byte), done: make(chan struct{}),
		raw: true,
	}, nil
}

func (c *Controller) start() {
	c.wg.Add(2)
	go c.read()
	go c.dispatch()
}

// Actions returns interactive commands.
func (c *Controller) Actions() <-chan Action { return c.actions }

// Errors reports unexpected input failures. A clean shutdown or EOF closes the
// channel without sending an error.
func (c *Controller) Errors() <-chan error { return c.errs }

// Suspend restores the terminal mode while retaining the controller's input
// resources so they can be reused after the process resumes.
func (c *Controller) Suspend() error {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	if c.closed || !c.raw {
		return nil
	}
	if err := c.restore(int(c.file.Fd()), c.state); err != nil {
		return fmt.Errorf("restore keyboard terminal state for suspension: %w", err)
	}
	c.raw = false
	return nil
}

// Resume returns the caller's terminal to raw mode after suspension.
func (c *Controller) Resume() error {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	if c.closed {
		return errors.New("resume keyboard controls: controller is closed")
	}
	if c.raw {
		return nil
	}
	state, err := c.makeRaw(int(c.file.Fd()))
	if err != nil {
		return fmt.Errorf("resume keyboard controls: %w", err)
	}
	c.state = state
	c.raw = true
	return nil
}

// Close stops the reader and restores the terminal mode exactly once.
func (c *Controller) Close() error {
	c.once.Do(func() {
		c.shutdownMu.Lock()
		close(c.done)
		c.shutdownMu.Unlock()
		var errs []error
		if n, err := c.wakeWrite.Write([]byte{1}); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, fmt.Errorf("wake keyboard reader: %w", err))
		} else if err == nil && n != 1 {
			errs = append(errs, fmt.Errorf("wake keyboard reader: short write"))
		}
		if err := closeFileError(c.wakeWrite, "close keyboard wake writer"); err != nil {
			errs = append(errs, err)
		}
		c.wg.Wait()
		if err := closeFileError(c.reader, "close keyboard reader"); err != nil {
			errs = append(errs, err)
		}
		if err := closeFileError(c.wakeRead, "close keyboard wake reader"); err != nil {
			errs = append(errs, err)
		}
		c.terminalMu.Lock()
		c.closed = true
		if c.raw {
			if err := c.restore(int(c.file.Fd()), c.state); err != nil {
				errs = append(errs, fmt.Errorf("restore keyboard terminal state: %w", err))
			} else {
				c.raw = false
			}
		}
		c.terminalMu.Unlock()
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}

func (c *Controller) read() {
	defer c.wg.Done()
	defer close(c.input)
	defer close(c.errs)

	buf := make([]byte, 32)
	pollFDs := []unix.PollFd{
		{Fd: int32(c.reader.Fd()), Events: unix.POLLIN},
		{Fd: int32(c.wakeRead.Fd()), Events: unix.POLLIN},
	}
	if c.readStarted != nil {
		close(c.readStarted)
	}
	for {
		if c.stopped() {
			return
		}
		pollFDs[0].Revents = 0
		pollFDs[1].Revents = 0
		ready, err := c.poll(pollFDs, -1)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			c.reportError(fmt.Errorf("poll keyboard input: %w", err))
			return
		}
		if c.stopped() {
			return
		}
		inputEvents := pollFDs[0].Revents
		wakeEvents := pollFDs[1].Revents
		if inputEvents&unix.POLLNVAL != 0 {
			c.reportError(errors.New("poll keyboard input: invalid file descriptor"))
			return
		}
		if wakeEvents&unix.POLLNVAL != 0 {
			c.reportError(errors.New("poll keyboard wake pipe: invalid file descriptor"))
			return
		}
		if inputEvents&unix.POLLERR != 0 {
			c.reportError(errors.New("poll keyboard input: device error"))
			return
		}
		if wakeEvents&unix.POLLERR != 0 {
			c.reportError(errors.New("poll keyboard wake pipe: device error"))
			return
		}
		if wakeEvents&(unix.POLLIN|unix.POLLHUP) != 0 {
			return
		}
		if ready == 0 || inputEvents&(unix.POLLIN|unix.POLLHUP) == 0 {
			continue
		}

		n, err := c.readInput(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			select {
			case c.input <- chunk:
			case <-c.done:
				return
			}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			c.reportError(fmt.Errorf("read keyboard input: %w", err))
			return
		}
		if err != nil || n == 0 {
			return
		}
	}
}

func (c *Controller) reportError(err error) {
	if err == nil {
		return
	}
	c.shutdownMu.Lock()
	defer c.shutdownMu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	c.errs <- err
}

func (c *Controller) stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Controller) dispatch() {
	defer c.wg.Done()
	defer close(c.actions)

	var d decoder
	var escapeTimeout <-chan time.Time
	for {
		select {
		case <-c.done:
			return
		case chunk, ok := <-c.input:
			if !ok {
				if action, flush := d.flushEscape(); flush {
					c.emit([]Action{action})
				}
				return
			}
			if !c.emit(d.feed(chunk)) {
				return
			}
			if d.escapeFlushPending() {
				escapeTimeout = c.timeout(escapeSequenceTimeout)
			} else {
				escapeTimeout = nil
			}
		case <-escapeTimeout:
			if action, flush := d.flushEscape(); flush && !c.emit([]Action{action}) {
				return
			}
			escapeTimeout = nil
		}
	}
}

func (c *Controller) emit(actions []Action) bool {
	for _, action := range actions {
		select {
		case c.actions <- action:
		case <-c.done:
			return false
		}
	}
	return true
}

func wrapRestoreError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restore keyboard terminal state: %w", err)
}

func closeFileError(file *os.File, context string) error {
	if file == nil {
		return nil
	}
	if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("%s: %w", context, err)
	}
	return nil
}
