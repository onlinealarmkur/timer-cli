//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/onlinealarmkur/timer-cli/internal/keyboard"
	"golang.org/x/term"
)

const (
	// Process startup can be delayed when CI runs every package and the race
	// detector concurrently. Keep event assertions tighter once output begins.
	ptyStartupTimeout = 15 * time.Second
	ptyInitialTimeout = 5 * time.Second
	ptyExitTimeout    = 4 * time.Second
	ptyHideCursor     = "\x1b[?25l"
	ptyShowCursor     = "\x1b[?25h"
	ptyClearLine      = "\r\x1b[2K"
	ptyClearTwoRows   = "\r\x1b[2K\x1b[1A\x1b[2K\r"
)

var (
	ptyBuildOnce   sync.Once
	ptyBuildDir    string
	ptyBinaryPath  string
	ptyBuildErr    error
	ptyBuildOutput string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if ptyBuildDir != "" {
		_ = os.RemoveAll(ptyBuildDir)
	}
	os.Exit(code)
}

func ptyBinary(t *testing.T) string {
	t.Helper()
	ptyBuildOnce.Do(func() {
		ptyBuildDir, ptyBuildErr = os.MkdirTemp("", "timer-cli-pty-binary.*")
		if ptyBuildErr != nil {
			return
		}
		ptyBinaryPath = filepath.Join(ptyBuildDir, "timer-cli")
		_, currentFile, _, ok := runtime.Caller(0)
		if !ok {
			ptyBuildErr = errors.New("locate PTY integration test source")
			return
		}
		goBinary := os.Getenv("GO")
		if goBinary == "" {
			goBinary, ptyBuildErr = exec.LookPath("go")
			if ptyBuildErr != nil {
				ptyBuildErr = fmt.Errorf("locate go executable: %w", ptyBuildErr)
				return
			}
		}
		command := exec.Command(goBinary, "build", "-trimpath", "-buildvcs=false", "-o", ptyBinaryPath, "../../cmd/timer-cli")
		command.Dir = filepath.Dir(currentFile)
		output, err := command.CombinedOutput()
		ptyBuildOutput = string(output)
		if err != nil {
			ptyBuildErr = fmt.Errorf("build real timer-cli binary: %w", err)
		}
	})
	if ptyBuildErr != nil {
		t.Fatalf("%v\n%s", ptyBuildErr, ptyBuildOutput)
	}
	return ptyBinaryPath
}

type ptyProcess struct {
	t       *testing.T
	command *exec.Cmd
	master  *os.File
	slave   *os.File
	initial *term.State

	outputMu sync.Mutex
	output   bytes.Buffer
	updated  chan struct{}
	readDone chan struct{}
	exited   chan struct{}
	waitErr  error

	closeMaster sync.Once
	closeSlave  sync.Once
}

func startPTYProcess(t *testing.T, title string, args ...string) *ptyProcess {
	t.Helper()
	process := startPTYProcessWithTerminal(t, title, "xterm-256color", args...)
	output := process.outputString()
	if !strings.Contains(output, ptyHideCursor) {
		t.Fatalf("initial frame did not hide cursor; output=%q", output)
	}
	rawState, err := term.GetState(int(process.master.Fd()))
	if err != nil {
		t.Fatalf("capture raw PTY terminal state: %v; output=%q", err, output)
	}
	if reflect.DeepEqual(process.initial, rawState) {
		t.Fatalf("terminal attributes did not enter raw mode; initial=%#v current=%#v output=%q", process.initial, rawState, output)
	}
	return process
}

func startPTYProcessWithTerminal(t *testing.T, waitFor, terminalName string, args ...string) *ptyProcess {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open PTY: %v", err)
	}
	closeFiles := func() {
		_ = master.Close()
		_ = slave.Close()
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		closeFiles()
		t.Fatalf("set PTY size: %v", err)
	}
	initial, err := term.GetState(int(master.Fd()))
	if err != nil {
		closeFiles()
		t.Fatalf("capture initial PTY terminal state: %v", err)
	}

	command := exec.Command(ptyBinary(t), args...)
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.Env = append(os.Environ(), "TERM="+terminalName, "LC_ALL=C.UTF-8")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		closeFiles()
		t.Fatalf("start timer-cli under PTY: %v", err)
	}

	process := &ptyProcess{
		t: t, command: command, master: master, slave: slave, initial: initial,
		updated: make(chan struct{}, 1), readDone: make(chan struct{}), exited: make(chan struct{}),
	}
	go process.readOutput()
	go func() {
		process.waitErr = command.Wait()
		close(process.exited)
	}()
	t.Cleanup(process.cleanup)

	process.waitForAfter(0, waitFor, ptyStartupTimeout)
	return process
}

func (p *ptyProcess) readOutput() {
	defer close(p.readDone)
	buffer := make([]byte, 4096)
	for {
		count, err := p.master.Read(buffer)
		if count > 0 {
			p.outputMu.Lock()
			_, _ = p.output.Write(buffer[:count])
			p.outputMu.Unlock()
			select {
			case p.updated <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *ptyProcess) outputString() string {
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	return p.output.String()
}

func (p *ptyProcess) outputLen() int {
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	return p.output.Len()
}

func (p *ptyProcess) waitForAfter(offset int, marker string, timeout time.Duration) {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		output := p.outputString()
		if offset <= len(output) && strings.Contains(output[offset:], marker) {
			return
		}
		select {
		case <-p.updated:
		case <-deadline.C:
			p.t.Fatalf("timed out after %s waiting for %q after byte %d; exited=%v output=%q", timeout, marker, offset, p.hasExited(), output)
		}
	}
}

func (p *ptyProcess) waitForRawMode(timeout time.Duration) {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := term.GetState(int(p.master.Fd()))
		if err != nil {
			p.t.Fatalf("capture PTY terminal state: %v; output=%q", err, p.outputString())
		}
		if !reflect.DeepEqual(p.initial, state) {
			return
		}
		select {
		case <-p.exited:
			p.t.Fatalf("timer-cli exited before entering raw mode; output=%q", p.outputString())
		case <-ticker.C:
		case <-deadline.C:
			p.t.Fatalf("timer-cli did not enter raw mode within %s; output=%q", timeout, p.outputString())
		}
	}
}

func (p *ptyProcess) waitForCookedMode(timeout time.Duration) {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := term.GetState(int(p.master.Fd()))
		if err != nil {
			p.t.Fatalf("capture PTY terminal state: %v; output=%q", err, p.outputString())
		}
		if reflect.DeepEqual(p.initial, state) {
			return
		}
		select {
		case <-p.exited:
			p.t.Fatalf("timer-cli exited before restoring cooked mode; output=%q", p.outputString())
		case <-ticker.C:
		case <-deadline.C:
			p.t.Fatalf("timer-cli did not restore cooked mode within %s; output=%q", timeout, p.outputString())
		}
	}
}

func (p *ptyProcess) assertCookedModeFor(duration time.Duration) {
	p.t.Helper()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := term.GetState(int(p.master.Fd()))
		if err != nil {
			p.t.Fatalf("capture suspended PTY terminal state: %v; output=%q", err, p.outputString())
		}
		if !reflect.DeepEqual(p.initial, state) {
			p.t.Fatalf("timer-cli left cooked mode before SIGCONT; initial=%#v current=%#v output=%q", p.initial, state, p.outputString())
		}
		select {
		case <-p.exited:
			p.t.Fatalf("timer-cli exited while suspended; output=%q", p.outputString())
		case <-ticker.C:
		case <-deadline.C:
			return
		}
	}
}

func suspendAndResumePTY(t *testing.T, process *ptyProcess, trigger func()) {
	t.Helper()
	offset := process.outputLen()
	trigger()
	process.waitForAfter(offset, ptyShowCursor, ptyInitialTimeout)
	process.waitForCookedMode(ptyInitialTimeout)
	process.assertCookedModeFor(50 * time.Millisecond)
	process.signal(syscall.SIGCONT)
	process.waitForRawMode(ptyInitialTimeout)
	process.waitForAfter(offset, ptyHideCursor, ptyInitialTimeout)
}

func (p *ptyProcess) waitForLastScreen(timeout time.Duration, markers ...string) string {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		output := p.outputString()
		screenAt := strings.LastIndex(output, "\x1b[H\x1b[2J")
		if screenAt >= 0 {
			screen := output[screenAt:]
			complete := true
			for _, marker := range markers {
				if !strings.Contains(screen, marker) {
					complete = false
					break
				}
			}
			if complete {
				return screen
			}
		}
		select {
		case <-p.updated:
		case <-deadline.C:
			p.t.Fatalf("timed out after %s waiting for markers %q on the last screen; exited=%v output=%q",
				timeout, markers, p.hasExited(), output)
		}
	}
}

func (p *ptyProcess) write(input []byte) {
	p.t.Helper()
	if _, err := p.master.Write(input); err != nil {
		p.t.Fatalf("write PTY input %q: %v; output=%q", input, err, p.outputString())
	}
}

func (p *ptyProcess) signal(signal os.Signal) {
	p.t.Helper()
	if err := p.command.Process.Signal(signal); err != nil {
		p.t.Fatalf("signal timer-cli with %v: %v; output=%q", signal, err, p.outputString())
	}
}

func (p *ptyProcess) hasExited() bool {
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

func (p *ptyProcess) finish(timeout time.Duration) (int, string) {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case <-p.exited:
	case <-deadline.C:
		p.t.Fatalf("timer-cli did not exit within %s; output=%q", timeout, p.outputString())
	}

	finalState, err := term.GetState(int(p.master.Fd()))
	if err != nil {
		p.t.Fatalf("capture final PTY terminal state: %v; output=%q", err, p.outputString())
	}
	if !reflect.DeepEqual(p.initial, finalState) {
		p.t.Fatalf("terminal attributes were not restored; initial=%#v final=%#v output=%q", p.initial, finalState, p.outputString())
	}

	p.closeSlaveFile()
	readDeadline := time.NewTimer(time.Second)
	select {
	case <-p.readDone:
		readDeadline.Stop()
	case <-readDeadline.C:
		p.closeMasterFile()
		<-p.readDone
	}
	p.closeMasterFile()
	return p.exitCode(), p.outputString()
}

func (p *ptyProcess) exitCode() int {
	p.t.Helper()
	if p.waitErr == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(p.waitErr, &exitError) {
		return exitError.ExitCode()
	}
	p.t.Fatalf("wait for timer-cli: %v", p.waitErr)
	return -1
}

func (p *ptyProcess) closeMasterFile() {
	p.closeMaster.Do(func() { _ = p.master.Close() })
}

func (p *ptyProcess) closeSlaveFile() {
	p.closeSlave.Do(func() { _ = p.slave.Close() })
}

func (p *ptyProcess) cleanup() {
	if !p.hasExited() {
		_ = p.command.Process.Kill()
		select {
		case <-p.exited:
		case <-time.After(2 * time.Second):
		}
	}
	p.closeSlaveFile()
	p.closeMasterFile()
}

func assertPTYCanceled(t *testing.T, exitCode int, output string) {
	t.Helper()
	if exitCode != 130 {
		t.Fatalf("canceled exit code=%d, want 130; output=%q", exitCode, output)
	}
	assertPTYLifecycle(t, output)
	if strings.Contains(output, "Time's up!") || strings.ContainsRune(output, '\a') {
		t.Fatalf("cancellation emitted completion alert; output=%q", output)
	}
}

func assertPTYLifecycle(t *testing.T, output string) {
	t.Helper()
	hide := strings.Index(output, ptyHideCursor)
	show := strings.LastIndex(output, ptyShowCursor)
	if hide < 0 || show <= hide {
		t.Fatalf("cursor lifecycle hide=%d show=%d; output=%q", hide, show, output)
	}
}

func assertOnlyCRLFLineEndings(t *testing.T, output string) {
	t.Helper()
	for index := 0; index < len(output); index++ {
		if output[index] == '\n' && (index == 0 || output[index-1] != '\r') {
			t.Fatalf("output contains a bare LF at byte %d: %q", index, output)
		}
	}
}

func TestPTYHarness(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1m", "--title", "Tea", "--no-bell")
	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYDumbTerminalUsesPlainOutputWithoutANSI(t *testing.T) {
	process := startPTYProcessWithTerminal(t, "remaining=", "dumb", "2m", "--no-bell")
	offset := process.outputLen()
	process.write([]byte{'-'})
	process.waitForAfter(offset, "remaining=01:00", ptyInitialTimeout)
	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != exitCanceled {
		t.Fatalf("exit code=%d, want %d; output=%q", exitCode, exitCanceled, output)
	}
	if count := strings.Count(output, "remaining="); count < 2 {
		t.Fatalf("plain output record count=%d, want at least 2: %q", count, output)
	}
	for offset := 0; ; {
		recordAt := strings.Index(output[offset:], "remaining=")
		if recordAt < 0 {
			break
		}
		recordAt += offset
		if recordAt != 0 && (recordAt < 2 || output[recordAt-2:recordAt] != "\r\n") {
			t.Fatalf("plain record at byte %d did not begin at column zero: %q", recordAt, output)
		}
		offset = recordAt + len("remaining=")
	}
	assertOnlyCRLFLineEndings(t, output)
	if strings.ContainsRune(output, '\x1b') {
		t.Fatalf("TERM=dumb output contains ANSI escape bytes: %q", output)
	}
	if strings.ContainsRune(output, '\a') {
		t.Fatalf("canceled TERM=dumb timer emitted a bell: %q", output)
	}
}

func TestPTYJSONCompletionUsesCRLFInRawMode(t *testing.T) {
	process := startPTYProcessWithTerminal(t, `"status":"completed"`, "xterm-256color", "1s", "--json", "--no-bell")
	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != exitOK {
		t.Fatalf("JSON completion exit code=%d, want %d; output=%q", exitCode, exitOK, output)
	}
	if !strings.HasSuffix(output, "}\r\n") {
		t.Fatalf("JSON completion ending = %q, want CRLF", output)
	}
	assertOnlyCRLFLineEndings(t, output)
	if strings.ContainsAny(output, "\x1b\a") {
		t.Fatalf("JSON completion emitted terminal control bytes: %q", output)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", output, err)
	}
	if result.Status != "completed" {
		t.Fatalf("JSON status = %q, want completed", result.Status)
	}
}

func TestPTYFinalOnlyCancellationUsesCRLFInRawMode(t *testing.T) {
	process := startPTYProcessWithTerminal(t, "", "xterm-256color", "1m", "--final-only", "--no-bell")
	process.waitForRawMode(ptyInitialTimeout)
	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != exitCanceled {
		t.Fatalf("final-only cancellation exit code=%d, want %d; output=%q", exitCode, exitCanceled, output)
	}
	if want := ptyClearLine + "Timer canceled.\r\n"; output != want {
		t.Fatalf("final-only cancellation output=%q, want %q", output, want)
	}
	assertOnlyCRLFLineEndings(t, output)
	if strings.ContainsRune(output, '\a') {
		t.Fatalf("final-only cancellation emitted BEL: %q", output)
	}
}

type failingKeyboardController struct {
	actions    <-chan keyboard.Action
	errs       <-chan error
	closeErr   error
	closeFunc  func() error
	closeCalls int
}

func (c *failingKeyboardController) Actions() <-chan keyboard.Action { return c.actions }
func (c *failingKeyboardController) Errors() <-chan error            { return c.errs }
func (c *failingKeyboardController) Close() error {
	c.closeCalls++
	var closeFuncErr error
	if c.closeFunc != nil {
		closeFuncErr = c.closeFunc()
	}
	return errors.Join(c.closeErr, closeFuncErr)
}

func TestCLIKeyboardInputFailureReturnsRuntimeAndJoinsCleanup(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})
	inputFailure := errors.New("keyboard input unavailable")
	closeFailure := errors.New("terminal restore unavailable")
	inputErrors := make(chan error, 1)
	inputErrors <- inputFailure
	controller := &failingKeyboardController{errs: inputErrors, closeErr: closeFailure}
	var stderr bytes.Buffer
	code := runWithKeyboard(
		context.Background(), []string{"1m", "--quiet", "--no-bell"}, slave, slave, &stderr,
		func(string) string { return "" },
		func(file *os.File) (keyboardController, error) {
			if file != slave {
				t.Fatalf("open keyboard file = %v, want PTY slave", file)
			}
			return controller, nil
		},
	)
	if code != exitRuntime || !strings.Contains(stderr.String(), "keyboard input failed") ||
		!strings.Contains(stderr.String(), inputFailure.Error()) ||
		!strings.Contains(stderr.String(), "close keyboard controller") ||
		!strings.Contains(stderr.String(), closeFailure.Error()) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if controller.closeCalls != 1 {
		t.Fatalf("controller Close calls = %d, want 1", controller.closeCalls)
	}
}

func TestCLIVisibleRuntimeFailureClearsFrameAndRestoresTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("set PTY size: %v", err)
	}
	initial, err := term.GetState(int(master.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		_, _ = output.ReadFrom(master)
		close(readDone)
	}()

	inputFailure := errors.New("keyboard input unavailable")
	inputErrors := make(chan error, 1)
	inputErrors <- inputFailure
	controller := &failingKeyboardController{errs: inputErrors}
	var rawState *term.State
	controller.closeFunc = func() error {
		return term.Restore(int(slave.Fd()), rawState)
	}
	code := runWithKeyboard(
		context.Background(), []string{"1m", "--title", "Tea", "--no-bell"}, slave, slave, slave,
		func(string) string { return "" },
		func(file *os.File) (keyboardController, error) {
			if file != slave {
				t.Fatalf("open keyboard file = %v, want PTY slave", file)
			}
			rawState, err = term.MakeRaw(int(file.Fd()))
			return controller, err
		},
	)
	if rawState == nil {
		t.Fatal("keyboard controller did not enter raw mode")
	}
	restored, err := term.GetState(int(master.Fd()))
	if err != nil {
		t.Fatalf("capture restored terminal state: %v", err)
	}
	if !reflect.DeepEqual(initial, restored) {
		t.Fatalf("terminal attributes were not restored; initial=%#v restored=%#v", initial, restored)
	}
	_ = slave.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		_ = master.Close()
		<-readDone
		t.Fatal("timed out draining PTY output")
	}

	got := output.String()
	diagnostic := "timer-cli: keyboard input failed: " + inputFailure.Error()
	diagnosticAt := strings.Index(got, diagnostic)
	cleanupAt := strings.LastIndex(got, ptyClearTwoRows+ptyShowCursor)
	if code != exitRuntime || diagnosticAt < 0 {
		t.Fatalf("code=%d diagnostic=%d output=%q", code, diagnosticAt, got)
	}
	if cleanupAt < 0 || cleanupAt > diagnosticAt {
		t.Fatalf("live frame was not cleared before diagnostic; cleanup=%d diagnostic=%d output=%q", cleanupAt, diagnosticAt, got)
	}
	if controller.closeCalls != 1 {
		t.Fatalf("controller Close calls = %d, want 1", controller.closeCalls)
	}
}

func TestPTYQuit(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1m", "--title", "Tea", "--no-bell")
	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYEscape(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1m", "--title", "Tea", "--no-bell")
	process.write([]byte{0x1b})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYCtrlC(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1m", "--title", "Tea", "--no-bell")
	process.write([]byte{0x03})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYCtrlCAfterIncompleteSequence(t *testing.T) {
	tests := []struct {
		name   string
		prefix []byte
	}{
		{name: "CSI", prefix: []byte("\x1b[")},
		{name: "SS3", prefix: []byte("\x1bO")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			process := startPTYProcess(t, "Tea", "1m", "--title", "Tea", "--no-bell")
			process.write(test.prefix)
			process.write([]byte{0x03})
			exitCode, output := process.finish(ptyExitTimeout)
			assertPTYCanceled(t, exitCode, output)
		})
	}
}

func TestPTYSIGTERM(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1m", "--title", "Tea", "--no-bell")
	process.signal(syscall.SIGTERM)
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYSIGHUP(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1m", "--title", "Tea")
	process.signal(syscall.SIGHUP)
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYCtrlZSuspendAndResume(t *testing.T) {
	process := startPTYProcess(t, "Study", "1m", "--title", "Study", "--no-bell")
	suspendAndResumePTY(t, process, func() { process.write([]byte{0x1a}) })
	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
	if strings.Count(output, ptyHideCursor) < 2 || strings.Count(output, ptyShowCursor) < 2 {
		t.Fatalf("suspend/resume did not recreate cursor lifecycle; output=%q", output)
	}
}

func TestPTYExternalSIGTSTPSuspendAndResume(t *testing.T) {
	process := startPTYProcess(t, "Study", "1m", "--title", "Study", "--no-bell")
	suspendAndResumePTY(t, process, func() { process.signal(syscall.SIGTSTP) })
	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYRepeatedSuspendResumeThenSIGHUP(t *testing.T) {
	process := startPTYProcess(t, "Study", "1m", "--title", "Study")
	suspendAndResumePTY(t, process, func() { process.write([]byte{0x1a}) })
	suspendAndResumePTY(t, process, func() { process.signal(syscall.SIGTSTP) })
	process.signal(syscall.SIGHUP)
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
	if count := bytes.Count([]byte(output), []byte{'\a'}); count != 0 {
		t.Fatalf("SIGHUP cancellation emitted %d bells; output=%q", count, output)
	}
}

func TestPTYCompletionAfterSuspensionAlertsOnce(t *testing.T) {
	process := startPTYProcess(t, "Study", "2s", "--title", "Study")
	offset := process.outputLen()
	process.write([]byte{0x1a})
	process.waitForAfter(offset, ptyShowCursor, ptyInitialTimeout)
	process.waitForCookedMode(ptyInitialTimeout)
	process.assertCookedModeFor(2200 * time.Millisecond)
	process.signal(syscall.SIGCONT)
	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != exitOK {
		t.Fatalf("completion after suspension exit code=%d, want %d; output=%q", exitCode, exitOK, output)
	}
	if count := strings.Count(output, "Time's up!"); count != 1 {
		t.Fatalf("completion message count=%d, want 1; output=%q", count, output)
	}
	if count := bytes.Count([]byte(output), []byte{'\a'}); count != 1 {
		t.Fatalf("completion bell count=%d, want 1; output=%q", count, output)
	}
	assertPTYLifecycle(t, output)
}

func TestPTYCompletion(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1s", "--title", "Tea", "--no-bell")
	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != 0 {
		t.Fatalf("completion exit code=%d, want 0; output=%q", exitCode, output)
	}
	assertPTYLifecycle(t, output)
	if count := strings.Count(output, "Time's up!"); count != 1 {
		t.Fatalf("completion message count=%d, want 1; output=%q", count, output)
	}
	progressAt := strings.LastIndex(output, "100%")
	messageAt := strings.LastIndex(output, "Time's up!")
	if progressAt < 0 || messageAt < progressAt {
		t.Fatalf("completion did not retain 100%% before the message; progress=%d message=%d output=%q", progressAt, messageAt, output)
	}
	if tail := output[progressAt:]; strings.Contains(tail, "\x1b[2K") || strings.Contains(tail, "\x1b[2J") {
		t.Fatalf("completion cleared the final 100%% frame: %q", tail)
	}
	if !strings.Contains(output[progressAt:], "Time's up!\r\n"+ptyShowCursor) {
		t.Fatalf("completion did not return the shell cursor to the left margin: %q", output[progressAt:])
	}
	if strings.ContainsRune(output, '\a') {
		t.Fatalf("--no-bell completion emitted BEL; output=%q", output)
	}
}

func TestPTYDefaultBell(t *testing.T) {
	process := startPTYProcess(t, "Tea", "1s", "--title", "Tea", "--bell-count", "3")
	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != 0 {
		t.Fatalf("completion exit code=%d, want 0; output=%q", exitCode, output)
	}
	assertPTYLifecycle(t, output)
	if count := strings.Count(output, "Time's up!"); count != 1 {
		t.Fatalf("completion message count=%d, want 1; output=%q", count, output)
	}
	if count := bytes.Count([]byte(output), []byte{'\a'}); count != 3 {
		t.Fatalf("completion BEL count=%d, want 3; output=%q", count, output)
	}
}

func TestPTYLoopCompletesTwiceAndQuitsCleanly(t *testing.T) {
	const message = "Cycle complete"
	process := startPTYProcess(t, "Loop", "1m", "--title", "Loop", "--loop", "--message", message, "--bell-count", "2")
	offset := process.outputLen()
	process.write([]byte{'-'})
	process.waitForAfter(offset, message, ptyInitialTimeout)
	offset = process.outputLen()
	process.write([]byte{'-'})
	process.waitForAfter(offset, message, ptyInitialTimeout)
	process.write([]byte{'Q'})

	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != 130 {
		t.Fatalf("loop quit exit code=%d, want 130; output=%q", exitCode, output)
	}
	assertPTYLifecycle(t, output)
	if count := strings.Count(output, message); count != 2 {
		t.Fatalf("loop completion message count=%d, want 2; output=%q", count, output)
	}
	if count := bytes.Count([]byte(output), []byte{'\a'}); count != 4 {
		t.Fatalf("loop BEL count=%d, want 4; output=%q", count, output)
	}
}

func TestPTYFullscreenLoopKeepsCycleNoticeVisible(t *testing.T) {
	const message = "Cycle complete"
	process := startPTYProcess(t, "Fullscreen loop", "1s", "--title", "Fullscreen loop",
		"--loop", "--fullscreen", "--message", message, "--no-bell")
	process.waitForAfter(0, message, ptyInitialTimeout)
	screen := process.waitForLastScreen(ptyInitialTimeout, message, "00:01", "0%")
	if !strings.Contains(screen, "Fullscreen loop") {
		t.Fatalf("restarted fullscreen screen omits title: %q", screen)
	}
	process.write([]byte{'Q'})

	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != 130 {
		t.Fatalf("fullscreen loop quit exit code=%d, want 130; output=%q", exitCode, output)
	}
	assertPTYLifecycle(t, output)
	if strings.ContainsRune(output, '\a') {
		t.Fatalf("--no-bell fullscreen loop emitted BEL; output=%q", output)
	}
}

func TestPTYLoopSIGTERMAfterCompletionDoesNotAddAlert(t *testing.T) {
	const message = "Cycle complete"
	process := startPTYProcess(t, "Signal loop", "1m", "--title", "Signal loop",
		"--loop", "--message", message, "--no-bell")
	offset := process.outputLen()
	process.write([]byte{'-'})
	process.waitForAfter(offset, message, ptyInitialTimeout)
	process.signal(syscall.SIGTERM)

	exitCode, output := process.finish(ptyExitTimeout)
	if exitCode != 130 {
		t.Fatalf("loop SIGTERM exit code=%d, want 130; output=%q", exitCode, output)
	}
	assertPTYLifecycle(t, output)
	if count := strings.Count(output, message); count != 1 {
		t.Fatalf("loop SIGTERM completion message count=%d, want 1; output=%q", count, output)
	}
	if strings.ContainsRune(output, '\a') {
		t.Fatalf("--no-bell signal loop emitted BEL; output=%q", output)
	}
}

func TestPTYSpanishInterface(t *testing.T) {
	process := startPTYProcess(t, "Té", "1m", "--title", "Té", "--lang", "es", "--no-bell")
	process.waitForAfter(0, "termina a las", ptyInitialTimeout)

	offset := process.outputLen()
	process.write([]byte{' '})
	process.waitForAfter(offset, "quedan 01:00 · EN PAUSA", ptyInitialTimeout)

	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
	if strings.Contains(output, " remaining") || strings.Contains(output, "PAUSED") || strings.Contains(output, "¡Se acabó el tiempo!") {
		t.Fatalf("Spanish PTY output leaked English or completion text: %q", output)
	}
}

func TestPTYResizeRedrawsPausedTimer(t *testing.T) {
	process := startPTYProcess(t, "Resize", "5m", "--title", "Resize",
		"--fullscreen", "--controls", "--ascii", "--no-bell")
	remainingPattern := regexp.MustCompile(`([0-9]{2}:[0-9]{2}(:[0-9]{2})?) remaining · PAUSED`)
	pausedRemaining := func(output string) string {
		matches := remainingPattern.FindStringSubmatch(output)
		if len(matches) == 0 {
			t.Fatalf("paused output omits remaining value: %q", output)
		}
		return matches[1]
	}

	offset := process.outputLen()
	process.write([]byte{' '})
	process.waitForAfter(offset, "PAUSED", ptyInitialTimeout)
	initialPaused := process.outputString()[offset:]
	remaining := pausedRemaining(initialPaused)

	offset = process.outputLen()
	if err := pty.Setsize(process.master, &pty.Winsize{Rows: 5, Cols: 40}); err != nil {
		t.Fatalf("resize PTY to compact dimensions: %v", err)
	}
	process.waitForAfter(offset, "PAUSED\r\n", ptyInitialTimeout)
	compact := process.outputString()[offset:]
	if got := pausedRemaining(compact); got != remaining {
		t.Fatalf("compact remaining = %q, want unchanged %q; output=%q", got, remaining, compact)
	}
	if !strings.Contains(compact, "\x1b[H\x1b[2J") || strings.Contains(compact, "Space pause/resume") {
		t.Fatalf("resize did not switch from fullscreen to compact mode: %q", compact)
	}

	offset = process.outputLen()
	if err := pty.Setsize(process.master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("resize PTY to fullscreen dimensions: %v", err)
	}
	process.waitForAfter(offset, "Space pause/resume", ptyInitialTimeout)
	fullscreen := process.outputString()[offset:]
	if got := pausedRemaining(fullscreen); got != remaining {
		t.Fatalf("fullscreen remaining = %q, want unchanged %q; output=%q", got, remaining, fullscreen)
	}
	if !strings.Contains(fullscreen, "PAUSED") || !strings.Contains(fullscreen, "\x1b[H\x1b[2J") {
		t.Fatalf("resize did not restore paused fullscreen mode: %q", fullscreen)
	}

	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}

func TestPTYInputStream(t *testing.T) {
	process := startPTYProcess(t, "Stream", "2m", "--title", "Stream", "--no-bell")

	offset := process.outputLen()
	process.write([]byte(" +"))
	process.waitForAfter(offset, "03:00 remaining · PAUSED", ptyInitialTimeout)

	offset = process.outputLen()
	process.write([]byte("r-"))
	process.waitForAfter(offset, "02:00 remaining", ptyInitialTimeout)
	process.waitForAfter(offset, "01:00 remaining", ptyInitialTimeout)

	offset = process.outputLen()
	process.write([]byte{0x1b})
	process.write([]byte{'['})
	process.write([]byte{'A'})
	process.write([]byte{' '})
	process.waitForAfter(offset, "01:00 remaining · PAUSED", ptyInitialTimeout)

	offset = process.outputLen()
	process.write([]byte{0x1b})
	process.write([]byte{'O'})
	process.write([]byte{'P'})
	process.write([]byte{'+'})
	process.waitForAfter(offset, "02:00 remaining · PAUSED", ptyInitialTimeout)

	process.write([]byte{'Q'})
	exitCode, output := process.finish(ptyExitTimeout)
	assertPTYCanceled(t, exitCode, output)
}
