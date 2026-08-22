// Package cli parses commands and wires the foreground timer runtime.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/onlinealarmkur/timer-cli/internal/alert"
	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
	"github.com/onlinealarmkur/timer-cli/internal/clock"
	"github.com/onlinealarmkur/timer-cli/internal/completion"
	"github.com/onlinealarmkur/timer-cli/internal/countdown"
	durationparse "github.com/onlinealarmkur/timer-cli/internal/duration"
	"github.com/onlinealarmkur/timer-cli/internal/keyboard"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
	"github.com/onlinealarmkur/timer-cli/internal/runner"
	terminalui "github.com/onlinealarmkur/timer-cli/internal/terminal"
	"github.com/onlinealarmkur/timer-cli/internal/version"
)

const (
	exitOK       = 0
	exitRuntime  = 1
	exitUsage    = 2
	exitCanceled = 130
)

var durationErrorMessages = map[durationparse.ErrorCode]localize.Message{
	durationparse.ErrorRequired:       localize.ErrorDurationRequired,
	durationparse.ErrorNegative:       localize.ErrorNegativeDuration,
	durationparse.ErrorZero:           localize.ErrorZeroDuration,
	durationparse.ErrorAmbiguousColon: localize.ErrorAmbiguousColonDuration,
	durationparse.ErrorBelowMinimum:   localize.ErrorMinimumDuration,
	durationparse.ErrorAboveMaximum:   localize.ErrorMaximumDuration,
	durationparse.ErrorInvalidUnits:   localize.ErrorInvalidUnitDuration,
	durationparse.ErrorTooLarge:       localize.ErrorDurationTooLarge,
	durationparse.ErrorInvalidMMSS:    localize.ErrorInvalidMMSS,
	durationparse.ErrorMMSSSeconds:    localize.ErrorMMSSSeconds,
	durationparse.ErrorInvalidHHMMSS:  localize.ErrorInvalidHHMMSS,
	durationparse.ErrorHHMMSSFields:   localize.ErrorHHMMSSFields,
}

type keyboardController interface {
	Actions() <-chan keyboard.Action
	Errors() <-chan error
	Close() error
}

type suspendableKeyboardController interface {
	Suspend() error
	Resume() error
}

type restartableRenderer struct {
	current runner.Renderer
	new     func() runner.Renderer
}

func newRestartableRenderer(newRenderer func() runner.Renderer) *restartableRenderer {
	return &restartableRenderer{current: newRenderer(), new: newRenderer}
}

func (r *restartableRenderer) Render(snapshot countdown.Snapshot) error {
	return r.current.Render(snapshot)
}

func (r *restartableRenderer) Finish(snapshot countdown.Snapshot, status, message string, at time.Time) error {
	return r.current.Finish(snapshot, status, message, at)
}

func (r *restartableRenderer) Close() error { return r.current.Close() }

func (r *restartableRenderer) Suspend() error { return r.current.Close() }

func (r *restartableRenderer) Resume() { r.current = r.new() }

type openKeyboardFunc func(*os.File) (keyboardController, error)

// Run executes the CLI and returns a process exit code.
func Run(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	return run(ctx, args, stdin, stdout, stderr, os.Getenv)
}

// RunWithSuspension executes the CLI with foreground job-control support.
// suspendProcess must stop the process until it receives SIGCONT.
func RunWithSuspension(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer, suspendSignals <-chan os.Signal, suspendProcess func() error) int {
	return runWithKeyboardAndSuspension(ctx, args, stdin, stdout, stderr, os.Getenv, openRealKeyboard, suspendSignals, suspendProcess)
}

func run(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer, getenv func(string) string) int {
	return runWithKeyboard(ctx, args, stdin, stdout, stderr, getenv, openRealKeyboard)
}

func openRealKeyboard(file *os.File) (keyboardController, error) {
	controller, err := keyboard.Open(file)
	if controller == nil || err != nil {
		return nil, err
	}
	return controller, nil
}

func runWithKeyboard(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer, getenv func(string) string, openKeyboard openKeyboardFunc) int {
	return runWithKeyboardAndSuspension(ctx, args, stdin, stdout, stderr, getenv, openKeyboard, nil, nil)
}

func runWithKeyboardAndSuspension(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer, getenv func(string) string, openKeyboard openKeyboardFunc, suspendSignals <-chan os.Signal, suspendProcess func() error) int {
	opts, err := parseOptions(args)
	language := localize.Resolve(opts.language, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "timer-cli: %s\n%s\n", localizedOptionError(language, err), localize.Text(language, localize.TryHelp))
		return exitUsage
	}

	switch opts.command {
	case commandHelp:
		return writeInformational(stdout, stderr, "help", helpText(language))
	case commandVersion:
		return writeInformational(stdout, stderr, "version", version.String()+"\n")
	case commandCompletion:
		script, scriptErr := completion.ScriptFor(opts.shell, language)
		if scriptErr != nil {
			fmt.Fprintf(stderr, "timer-cli: %v\n", scriptErr)
			return exitUsage
		}
		return writeInformational(stdout, stderr, "completion", script)
	}

	d, err := durationparse.Parse(opts.duration)
	if err != nil {
		fmt.Fprintf(stderr, "timer-cli: %s\n", localizedDurationError(language, err))
		return exitUsage
	}

	stdoutFile, stdoutTTY := terminalFile(stdout)
	stdinTTY := stdin != nil && term.IsTerminal(int(stdin.Fd()))
	displayTTY := terminalDisplayEnabled(stdoutTTY, getenv)
	ascii := asciiOutput(opts.ascii, getenv)
	var controller keyboardController
	var actions <-chan keyboard.Action
	var inputErrors <-chan error
	rawTerminal := false
	if stdoutTTY && stdinTTY {
		controller, err = openKeyboard(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "timer-cli: %v\n", err)
			return exitRuntime
		}
		if controller != nil {
			actions = controller.Actions()
			inputErrors = controller.Errors()
			rawTerminal = sameTerminalDevice(stdin, stdoutFile)
		}
	}
	rendererOptions := terminalui.Options{
		TTY: displayTTY, RawTerminal: rawTerminal, Fullscreen: opts.fullscreen, ASCII: ascii,
		Quiet: opts.quiet, FinalOnly: opts.finalOnly, JSON: opts.json,
		Loop: opts.loop, Controls: opts.controls, Title: opts.title, Language: language,
		Size: func() (int, int) {
			if !stdoutTTY {
				return 80, 24
			}
			width, height, sizeErr := term.GetSize(int(stdoutFile.Fd()))
			if sizeErr != nil {
				return 80, 24
			}
			return width, height
		},
	}
	renderer := newRestartableRenderer(func() runner.Renderer {
		return terminalui.New(stdout, rendererOptions)
	})
	var suspendRuntime func() error
	if suspendProcess != nil {
		suspendRuntime = func() error {
			return suspendForeground(renderer, controller, suspendProcess)
		}
	}

	message := opts.message
	if message == "" {
		message = localize.Text(language, localize.TimeUp)
	}
	status, runErr := runner.Run(ctx, runner.Options{
		Countdown: countdown.New(clock.System{}, d), Renderer: renderer,
		Alert: alert.Bell{Writer: stdout, Disabled: opts.noBell || opts.json || !stdoutTTY, Count: opts.bellCount},
		Loop:  opts.loop, Actions: actions, InputErrors: inputErrors,
		SuspendSignals: suspendSignals, Suspend: suspendRuntime,
		Interval: runInterval(displayTTY, opts), RecordCadence: usesRedirectedRecords(displayTTY, opts),
		Message: message,
	})
	if controller != nil {
		runErr = joinRuntimeAndCleanupErrors(runErr, controller.Close())
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "timer-cli: %v\n", runErr)
		return exitRuntime
	}
	if status == runner.Canceled {
		return exitCanceled
	}
	return exitOK
}

func suspendForeground(renderer *restartableRenderer, controller keyboardController, suspendProcess func() error) error {
	var keyboard suspendableKeyboardController
	if candidate, ok := controller.(suspendableKeyboardController); ok {
		keyboard = candidate
	}

	renderErr := renderer.Suspend()
	var keyboardSuspendErr error
	if keyboard != nil {
		keyboardSuspendErr = keyboard.Suspend()
	}
	if err := errors.Join(
		wrapError(renderErr, "prepare display for suspension"),
		wrapError(keyboardSuspendErr, "prepare keyboard for suspension"),
	); err != nil {
		return err
	}

	processErr := suspendProcess()
	var keyboardResumeErr error
	if keyboard != nil {
		keyboardResumeErr = keyboard.Resume()
	}
	renderer.Resume()
	return errors.Join(
		wrapError(processErr, "stop process for suspension"),
		wrapError(keyboardResumeErr, "resume keyboard after suspension"),
	)
}

func wrapError(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

func runInterval(stdoutTTY bool, opts options) time.Duration {
	if stdoutTTY && !opts.quiet && !opts.finalOnly && !opts.json {
		return 100 * time.Millisecond
	}
	return time.Second
}

func usesRedirectedRecords(displayTTY bool, opts options) bool {
	return !displayTTY && !opts.quiet && !opts.finalOnly && !opts.json
}

func writeInformational(stdout, stderr io.Writer, kind, payload string) int {
	n, err := io.WriteString(stdout, payload)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	if err != nil {
		fmt.Fprintf(stderr, "timer-cli: write %s: %v\n", kind, err)
		return exitRuntime
	}
	return exitOK
}

func joinRuntimeAndCleanupErrors(runErr, closeErr error) error {
	if closeErr != nil {
		closeErr = fmt.Errorf("close keyboard controller: %w", closeErr)
	}
	return errors.Join(runErr, closeErr)
}

func terminalFile(w io.Writer) (*os.File, bool) {
	f, ok := w.(*os.File)
	if !ok {
		return nil, false
	}
	return f, term.IsTerminal(int(f.Fd()))
}

func sameTerminalDevice(first, second *os.File) bool {
	if first == nil || second == nil {
		return false
	}
	firstInfo, err := first.Stat()
	if err != nil {
		return false
	}
	secondInfo, err := second.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(firstInfo, secondInfo)
}

func asciiOutput(force bool, getenv func(string) string) bool {
	return force || !unicodeCapableWith(getenv)
}

func terminalDisplayEnabled(stdoutTTY bool, getenv func(string) string) bool {
	return stdoutTTY && !dumbTerminalWith(getenv)
}

func dumbTerminalWith(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(getenv("TERM")), "dumb")
}

func unicodeCapableWith(getenv func(string) string) bool {
	if dumbTerminalWith(getenv) {
		return false
	}
	if getenv == nil {
		return true
	}
	locale := getenv("LC_ALL")
	if locale == "" {
		locale = getenv("LC_CTYPE")
	}
	if locale == "" {
		locale = getenv("LANG")
	}
	locale = strings.ToUpper(locale)
	return locale != "C" && locale != "POSIX"
}

func helpText(language localize.Language) string {
	var text strings.Builder
	fmt.Fprintf(&text, `%s - %s

%s:
  %s <%s> [%s]
`, cliinfo.ProgramName, localize.Text(language, localize.HelpTagline),
		localize.Text(language, localize.UsageHeading), cliinfo.ProgramName,
		localize.Text(language, localize.DurationValueLabel), localize.Text(language, localize.OptionsValueLabel))
	for _, command := range cliinfo.CommandsFor(language) {
		fmt.Fprintf(&text, "  %s %s", cliinfo.ProgramName, command.Name)
		if command.Name == cliinfo.CommandCompletion {
			fmt.Fprintf(&text, " <%s>", strings.Join(cliinfo.Shells(), "|"))
		}
		text.WriteByte('\n')
	}
	fmt.Fprintf(&text, "\n%s:\n%s\n\n%s:\n",
		localize.Text(language, localize.DurationsHeading),
		localize.Text(language, localize.DurationHelp),
		localize.Text(language, localize.OptionsHeading))
	for _, option := range cliinfo.OptionsFor(language) {
		usage := "--" + option.Long
		if option.Short != "" {
			usage += ", -" + option.Short
		}
		if option.TakesValue {
			usage += " " + option.ValueLabel
		}
		fmt.Fprintf(&text, "  %-19s%s\n", usage, option.Description)
	}
	fmt.Fprintf(&text, "\n%s:\n%s\n\n%s:\n%s\n\n%s\n",
		localize.Text(language, localize.InterfaceLanguageHeading),
		localize.Text(language, localize.InterfaceLanguageHelp),
		localize.Text(language, localize.InteractiveControlsHeading),
		localize.Text(language, localize.InteractiveControlsHelp),
		localize.Text(language, localize.RedirectedOutputHelp))
	return text.String()
}

func localizedOptionError(language localize.Language, err error) string {
	var parseErr *optionError
	if errors.As(err, &parseErr) {
		return parseErr.localized(language)
	}
	return err.Error()
}

func localizedDurationError(language localize.Language, err error) string {
	code, ok := durationparse.ErrorCodeOf(err)
	if !ok {
		return err.Error()
	}
	message, ok := durationErrorMessages[code]
	if !ok {
		return err.Error()
	}
	return localize.Text(language, message)
}
