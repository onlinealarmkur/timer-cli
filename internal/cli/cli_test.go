package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
	"github.com/onlinealarmkur/timer-cli/internal/countdown"
	durationparse "github.com/onlinealarmkur/timer-cli/internal/duration"
	"github.com/onlinealarmkur/timer-cli/internal/keyboard"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
	"github.com/onlinealarmkur/timer-cli/internal/runner"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

type lifecycleRenderer struct {
	events   *[]string
	closeErr error
}

func (*lifecycleRenderer) Render(countdown.Snapshot) error { return nil }
func (*lifecycleRenderer) Finish(countdown.Snapshot, string, string, time.Time) error {
	return nil
}
func (r *lifecycleRenderer) Close() error {
	*r.events = append(*r.events, "display-suspend")
	return r.closeErr
}

type lifecycleKeyboard struct {
	events     *[]string
	suspendErr error
	resumeErr  error
}

func (*lifecycleKeyboard) Actions() <-chan keyboard.Action { return nil }
func (*lifecycleKeyboard) Errors() <-chan error            { return nil }
func (*lifecycleKeyboard) Close() error                    { return nil }
func (k *lifecycleKeyboard) Suspend() error {
	*k.events = append(*k.events, "keyboard-suspend")
	return k.suspendErr
}
func (k *lifecycleKeyboard) Resume() error {
	*k.events = append(*k.events, "keyboard-resume")
	return k.resumeErr
}

func TestSuspendForegroundRestoresAndReconstructsRuntime(t *testing.T) {
	t.Parallel()
	var events []string
	created := 0
	renderer := newRestartableRenderer(func() runner.Renderer {
		created++
		return &lifecycleRenderer{events: &events}
	})
	controller := &lifecycleKeyboard{events: &events}
	err := suspendForeground(renderer, controller, func() error {
		events = append(events, "process-stop")
		return nil
	})
	if err != nil {
		t.Fatalf("suspendForeground(): %v", err)
	}
	if got, want := strings.Join(events, ","), "display-suspend,keyboard-suspend,process-stop,keyboard-resume"; got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
	if created != 2 {
		t.Fatalf("renderer instances = %d, want 2", created)
	}
}

func TestSuspendForegroundJoinsPreparationFailuresWithoutStopping(t *testing.T) {
	t.Parallel()
	displayErr := errors.New("display cleanup failed")
	keyboardErr := errors.New("keyboard restore failed")
	var events []string
	renderer := newRestartableRenderer(func() runner.Renderer {
		return &lifecycleRenderer{events: &events, closeErr: displayErr}
	})
	controller := &lifecycleKeyboard{events: &events, suspendErr: keyboardErr}
	processCalled := false
	err := suspendForeground(renderer, controller, func() error {
		processCalled = true
		return nil
	})
	if !errors.Is(err, displayErr) || !errors.Is(err, keyboardErr) || processCalled {
		t.Fatalf("suspendForeground() = %v processCalled=%v", err, processCalled)
	}
	if got, want := strings.Join(events, ","), "display-suspend,keyboard-suspend"; got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
}

func TestSuspendForegroundResumesAfterProcessFailure(t *testing.T) {
	t.Parallel()
	processErr := errors.New("stop failed")
	resumeErr := errors.New("raw mode failed")
	var events []string
	created := 0
	renderer := newRestartableRenderer(func() runner.Renderer {
		created++
		return &lifecycleRenderer{events: &events}
	})
	controller := &lifecycleKeyboard{events: &events, resumeErr: resumeErr}
	err := suspendForeground(renderer, controller, func() error {
		events = append(events, "process-stop")
		return processErr
	})
	if !errors.Is(err, processErr) || !errors.Is(err, resumeErr) {
		t.Fatalf("suspendForeground() = %v", err)
	}
	if got, want := strings.Join(events, ","), "display-suspend,keyboard-suspend,process-stop,keyboard-resume"; got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
	if created != 2 {
		t.Fatalf("renderer instances = %d, want 2", created)
	}
}

type cancelOnTextWriter struct {
	bytes.Buffer
	cancel  context.CancelFunc
	trigger string
}

func (w *cancelOnTextWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	if err == nil && strings.Contains(w.Buffer.String(), w.trigger) {
		w.cancel()
	}
	return n, err
}

func TestRunIntervalMatchesOutputMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		stdoutTTY bool
		opts      options
		want      time.Duration
	}{
		{name: "visible TTY", stdoutTTY: true, want: 100 * time.Millisecond},
		{name: "redirected output", want: time.Second},
		{name: "quiet TTY", stdoutTTY: true, opts: options{quiet: true}, want: time.Second},
		{name: "final-only TTY", stdoutTTY: true, opts: options{finalOnly: true}, want: time.Second},
		{name: "JSON TTY", stdoutTTY: true, opts: options{json: true}, want: time.Second},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := runInterval(test.stdoutTTY, test.opts); got != test.want {
				t.Fatalf("runInterval() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRedirectedRecordSchedulingMatchesOutputMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		displayTTY bool
		opts       options
		want       bool
	}{
		{name: "interactive display", displayTTY: true},
		{name: "redirected records", want: true},
		{name: "dumb terminal records", want: true},
		{name: "quiet", opts: options{quiet: true}},
		{name: "final only", opts: options{finalOnly: true}},
		{name: "JSON", opts: options{json: true}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := usesRedirectedRecords(test.displayTTY, test.opts); got != test.want {
				t.Fatalf("usesRedirectedRecords() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestVersionExitCode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version"}, os.Stdin, &stdout, &stderr)
	if code != exitOK || stdout.String() != "timer-cli 1.0.0\n" || strings.Contains(stdout.String(), "unknown") || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunWithSuspensionCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := RunWithSuspension(ctx, []string{"1m", "--quiet", "--no-bell"}, nil, &stdout, &stderr, nil, func() error { return nil })
	if code != exitCanceled || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunWithSuspensionHandlesExternalRequest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	suspendSignals := make(chan os.Signal, 1)
	suspendSignals <- os.Interrupt
	var stdout, stderr bytes.Buffer
	suspendCalls := 0
	code := RunWithSuspension(ctx, []string{"1m", "--quiet", "--no-bell"}, nil, &stdout, &stderr, suspendSignals, func() error {
		suspendCalls++
		cancel()
		return nil
	})
	if code != exitCanceled || suspendCalls != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d suspendCalls=%d stdout=%q stderr=%q", code, suspendCalls, stdout.String(), stderr.String())
	}
}

func TestInvalidDurationExitCode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"0"}, os.Stdin, &stdout, &stderr)
	if code != exitUsage || !strings.HasPrefix(stderr.String(), "timer-cli:") || !strings.Contains(stderr.String(), "greater than zero") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestNegativeDurationUsesDurationError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"-1", "minuto"}, nil, &stdout, &stderr)
	if code != exitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "duration cannot be negative") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLocalizedDurationWordsReachRuntime(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"English":    {"1", "hour", "and", "30", "minutes"},
		"Spanish":    {"1", "hora", "y", "30", "minutos"},
		"Portuguese": {"1", "hora", "e", "30", "minutos"},
		"French":     {"1", "heure", "et", "30", "minutes"},
		"German":     {"1", "Stunde", "und", "30", "Minuten"},
		"Italian":    {"1", "ora", "e", "30", "minuti"},
		"Dutch":      {"1", "uur", "en", "30", "minuten"},
	}
	for name, args := range tests {
		name, args := name, args
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var stdout, stderr bytes.Buffer
			args = append(append([]string(nil), args...), "--quiet", "--no-bell")
			code := Run(ctx, args, nil, &stdout, &stderr)
			if code != exitCanceled || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestOutputModeConflictExitCode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"1m", "--quiet", "--json"}, os.Stdin, &stdout, &stderr)
	if code != exitUsage || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "--quiet, --final-only, and --json are mutually exclusive") ||
		!strings.Contains(stderr.String(), "Try 'timer-cli --help' for usage.") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLoopJSONConflictExitCodeIsLocalized(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"1m", "--loop", "--json", "--lang", "es"}, nil, &stdout, &stderr)
	if code != exitUsage || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "--loop no se puede combinar con --json") ||
		!strings.Contains(stderr.String(), "Prueba 'timer-cli --help'") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLoopRestartsAndCancellationStopsWithoutAnotherAlert(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &cancelOnTextWriter{cancel: cancel, trigger: "Time's up!\n"}
	var stderr bytes.Buffer

	code := Run(ctx, []string{"1s", "--loop", "--final-only", "--no-bell"}, nil, stdout, &stderr)
	if code != exitCanceled || stdout.String() != "Time's up!\nTimer canceled.\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCompletionExitCodes(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"completion", "bash"}, os.Stdin, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "complete -F") {
		t.Fatalf("valid completion code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"completion", "powershell"}, os.Stdin, &stdout, &stderr); code != exitUsage || !strings.HasPrefix(stderr.String(), "timer-cli:") {
		t.Fatalf("invalid completion code=%d stderr=%q", code, stderr.String())
	}
}

func TestInformationalWriterFailureExitCode(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("output unavailable")
	tests := []struct {
		name string
		args []string
		kind string
	}{
		{name: "help", args: []string{"--help"}, kind: "help"},
		{name: "version", args: []string{"version"}, kind: "version"},
		{name: "completion", args: []string{"completion", "bash"}, kind: "completion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := Run(context.Background(), tt.args, os.Stdin, failingWriter{err: writeErr}, &stderr)
			want := "timer-cli: write " + tt.kind + ": " + writeErr.Error() + "\n"
			if code != exitRuntime || stderr.String() != want {
				t.Fatalf("code=%d stderr=%q, want code=%d stderr=%q", code, stderr.String(), exitRuntime, want)
			}
		})
	}
}

func TestInformationalWriterFailuresStillReturnRuntime(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("output unavailable")
	code := Run(context.Background(), []string{"version"}, os.Stdin, failingWriter{err: writeErr}, failingWriter{err: writeErr})
	if code != exitRuntime {
		t.Fatalf("code=%d, want %d", code, exitRuntime)
	}
}

func TestInformationalShortWriteReturnsRuntime(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"version"}, nil, shortWriter{}, &stderr)
	if code != exitRuntime || stderr.String() != "timer-cli: write version: short write\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestJoinRuntimeAndCleanupErrors(t *testing.T) {
	t.Parallel()
	runErr := errors.New("render failed")
	closeErr := errors.New("restore failed")
	tests := []struct {
		name      string
		runErr    error
		closeErr  error
		wantRun   bool
		wantClose bool
	}{
		{name: "no errors"},
		{name: "runtime only", runErr: runErr, wantRun: true},
		{name: "cleanup only", closeErr: closeErr, wantClose: true},
		{name: "both errors", runErr: runErr, closeErr: closeErr, wantRun: true, wantClose: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := joinRuntimeAndCleanupErrors(tt.runErr, tt.closeErr)
			if (got != nil) != (tt.wantRun || tt.wantClose) {
				t.Fatalf("error = %v, want error=%v", got, tt.wantRun || tt.wantClose)
			}
			if errors.Is(got, runErr) != tt.wantRun {
				t.Fatalf("errors.Is(error, runErr) = %v, want %v", errors.Is(got, runErr), tt.wantRun)
			}
			if errors.Is(got, closeErr) != tt.wantClose {
				t.Fatalf("errors.Is(error, closeErr) = %v, want %v", errors.Is(got, closeErr), tt.wantClose)
			}
			if tt.wantClose && !strings.Contains(got.Error(), "close keyboard controller") {
				t.Fatalf("error %q lacks cleanup context", got)
			}
		})
	}
}

func TestCanceledExitCode(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"1m", "--quiet", "--no-bell"}, os.Stdin, &stdout, &stderr)
	if code != exitCanceled || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRuntimeWriterFailureExitCode(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	writeErr := errors.New("output unavailable")
	code := Run(context.Background(), []string{"1s", "--no-bell"}, os.Stdin, failingWriter{err: writeErr}, &stderr)
	if code != exitRuntime || !strings.HasPrefix(stderr.String(), "timer-cli:") || !strings.Contains(stderr.String(), writeErr.Error()) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRuntimeShortWriteExitCode(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"1s", "--no-bell"}, nil, shortWriter{}, &stderr)
	if code != exitRuntime || !strings.Contains(stderr.String(), io.ErrShortWrite.Error()) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestHelpUsesPublicCommandName(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--help"}, os.Stdin, &stdout, &stderr)
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"timer-cli - a clear and reliable foreground countdown",
		"timer-cli <duration> [options]",
		"timer-cli version",
		"timer-cli completion <bash|zsh|fish>",
		"Durations:",
		"Options:",
		"Interactive controls (TTY only):",
		"Unit words also work in English, Spanish, Portuguese, French, German",
		"Italian, and Dutch",
		"Exclusive output mode; suppress all regular output (the bell still applies on a TTY)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help missing %q: %q", want, stdout.String())
		}
	}
	for _, option := range cliinfo.Options() {
		if !strings.Contains(stdout.String(), "--"+option.Long) || !strings.Contains(stdout.String(), option.Description) {
			t.Errorf("help omits metadata for --%s: %q", option.Long, stdout.String())
		}
		if option.Short != "" && !strings.Contains(stdout.String(), "-"+option.Short) {
			t.Errorf("help omits short option -%s: %q", option.Short, stdout.String())
		}
	}
}

func TestSpanishHelpUsesLocalizedMetadata(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	englishLocale := func(name string) string {
		if name == "LANG" {
			return "en_US.UTF-8"
		}
		return ""
	}
	code := run(context.Background(), []string{"--help", "--lang", "es"}, os.Stdin, &stdout, &stderr, englishLocale)
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"timer-cli - una cuenta atrás clara y fiable en primer plano",
		"Uso:",
		"timer-cli <duración> [opciones]",
		"Duraciones:",
		"Opciones:",
		"Idioma de la interfaz:",
		"auto usa, por orden, LC_ALL, LC_MESSAGES y LANG",
		"Controles interactivos (solo TTY):",
		"--title TEXTO",
		"--lang IDIOMA",
		"Define el idioma de la interfaz",
		"Modo de salida exclusivo; no muestra la salida normal (la campana sigue activa si la salida es un TTY)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("Spanish help omits %q: %q", want, stdout.String())
		}
	}
	for _, option := range cliinfo.OptionsFor(localize.Spanish) {
		if !strings.Contains(stdout.String(), "--"+option.Long) || !strings.Contains(stdout.String(), option.Description) {
			t.Errorf("Spanish help omits metadata for --%s", option.Long)
		}
	}
	if strings.Contains(stdout.String(), "\nUsage:\n") || strings.Contains(stdout.String(), "Time's up!") {
		t.Fatalf("Spanish help leaked English interface text: %q", stdout.String())
	}
}

func TestAutomaticSpanishLocaleAndExplicitOverride(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		want       string
		wantAbsent string
	}{
		{
			name: "LC messages selects Spanish", args: []string{"0"},
			env:  map[string]string{"LC_MESSAGES": "es_ES.UTF-8", "LANG": "en_US.UTF-8"},
			want: "la duración debe ser mayor que cero",
		},
		{
			name: "explicit English overrides Spanish", args: []string{"0", "--lang", "en"},
			env: map[string]string{"LC_ALL": "es_ES.UTF-8"}, want: "duration must be greater than zero",
			wantAbsent: "la duración",
		},
		{
			name: "LC ALL overrides LC messages", args: []string{"0"},
			env:  map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "es_ES.UTF-8"},
			want: "duration must be greater than zero", wantAbsent: "la duración",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(name string) string { return test.env[name] }
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.args, nil, &stdout, &stderr, getenv)
			if code != exitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want %q", code, stdout.String(), stderr.String(), test.want)
			}
			if test.wantAbsent != "" && strings.Contains(stderr.String(), test.wantAbsent) {
				t.Errorf("stderr unexpectedly contains %q: %q", test.wantAbsent, stderr.String())
			}
		})
	}
}

func TestSpanishDefaultMessageAndStableJSONContract(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"1m", "--json", "--lang=es"}, nil, &stdout, &stderr, func(string) string { return "" })
	if code != exitCanceled || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), err)
	}
	if result.Status != "canceled" || result.Message != "¡Se acabó el tiempo!" {
		t.Fatalf("result = %+v", result)
	}
}

func TestDurationInputDoesNotSelectInterfaceLanguage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"5", "minutos", "--final-only", "--lang=en"}, nil, &stdout, &stderr, func(string) string { return "" })
	if code != exitCanceled || stdout.String() != "Timer canceled.\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestEveryDurationErrorHasSpanishTranslation proves durationErrorMessages
// covers every durationparse.ErrorCode and that each code's Spanish
// rendering is present and actually translated (not an English string
// pasted into the Spanish catalog). It is derived from the ErrorCode enum
// itself via the ErrorCodeCount sentinel, so a new duration error code ships
// English text to Spanish users only if this test is also skipped.
func TestEveryDurationErrorHasSpanishTranslation(t *testing.T) {
	t.Parallel()
	for code := durationparse.ErrorCode(1); code < durationparse.ErrorCodeCount; code++ {
		message, ok := durationErrorMessages[code]
		if !ok {
			t.Errorf("duration error code %d has no entry in durationErrorMessages", code)
			continue
		}
		spanish := localize.Text(localize.Spanish, message)
		english := localize.Text(localize.English, message)
		if spanish == "" {
			t.Errorf("duration error code %d has an empty Spanish translation", code)
		}
		if spanish == english {
			t.Errorf("duration error code %d Spanish translation matches English: %q", code, spanish)
		}
	}
}

func TestLocalizationFallsBackForUnclassifiedErrors(t *testing.T) {
	t.Parallel()
	unrelated := errors.New("unclassified failure")
	if got := localizedOptionError(localize.Spanish, unrelated); got != unrelated.Error() {
		t.Fatalf("localizedOptionError fallback = %q", got)
	}
	if got := localizedDurationError(localize.Spanish, unrelated); got != unrelated.Error() {
		t.Fatalf("localizedDurationError fallback = %q", got)
	}
	unknownDuration := &durationparse.ParseError{Code: 255}
	if got := localizedDurationError(localize.Spanish, unknownDuration); got != "invalid duration" {
		t.Fatalf("unknown duration code fallback = %q", got)
	}
}

func TestUsageErrorUsesPublicCommandName(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), nil, os.Stdin, &stdout, &stderr)
	want := "timer-cli: duration is required; try 10m, 90s, or 01:30\nTry 'timer-cli --help' for usage.\n"
	if code != exitUsage || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("code=%d stdout=%q stderr=%q, want stderr=%q", code, stdout.String(), stderr.String(), want)
	}
}

func TestSpanishUsageErrorsIncludeLocalizedGuidance(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"1m", "--quiet", "--json", "--lang", "es"}, nil, &stdout, &stderr, func(string) string { return "" })
	want := "timer-cli: --quiet, --final-only y --json son mutuamente excluyentes\n" +
		"Prueba 'timer-cli --help' para ver el modo de uso.\n"
	if code != exitUsage || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("code=%d stdout=%q stderr=%q, want stderr=%q", code, stdout.String(), stderr.String(), want)
	}
}

func TestUnicodeLocalePrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "empty defaults to Unicode", env: map[string]string{}, want: true},
		{name: "LANG C", env: map[string]string{"LANG": "C"}, want: false},
		{name: "LANG POSIX", env: map[string]string{"LANG": "POSIX"}, want: false},
		{name: "C UTF-8 is Unicode", env: map[string]string{"LANG": "C.UTF-8"}, want: true},
		{name: "LC_CTYPE precedes LANG", env: map[string]string{"LC_CTYPE": "POSIX", "LANG": "en_US.UTF-8"}, want: false},
		{name: "LC_ALL precedes LC_CTYPE", env: map[string]string{"LC_ALL": "C", "LC_CTYPE": "en_US.UTF-8", "LANG": "en_US.UTF-8"}, want: false},
		{name: "TERM dumb is authoritative", env: map[string]string{"TERM": "dumb", "LC_ALL": "en_US.UTF-8"}, want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string { return test.env[key] }
			if got := unicodeCapableWith(getenv); got != test.want {
				t.Fatalf("unicodeCapableWith() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestASCIIFlagIsAuthoritative(t *testing.T) {
	t.Parallel()
	unicodeLocale := func(key string) string {
		if key == "LANG" {
			return "en_US.UTF-8"
		}
		return ""
	}
	if !asciiOutput(true, unicodeLocale) {
		t.Fatal("forced ASCII was not selected")
	}
	if asciiOutput(false, unicodeLocale) {
		t.Fatal("Unicode locale unexpectedly selected ASCII")
	}
}

func TestTerminalDisplayCapability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		stdoutTTY bool
		term      string
		want      bool
	}{
		{name: "ordinary TTY", stdoutTTY: true, term: "xterm-256color", want: true},
		{name: "redirected output", term: "xterm-256color"},
		{name: "dumb terminal", stdoutTTY: true, term: "dumb"},
		{name: "case insensitive dumb terminal", stdoutTTY: true, term: " DUMB "},
		{name: "missing environment reader", stdoutTTY: true, want: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var getenv func(string) string
			if test.name != "missing environment reader" {
				getenv = func(name string) string {
					if name == "TERM" {
						return test.term
					}
					return ""
				}
			}
			if got := terminalDisplayEnabled(test.stdoutTTY, getenv); got != test.want {
				t.Fatalf("terminalDisplayEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSameTerminalDevice(t *testing.T) {
	t.Parallel()
	first, err := os.CreateTemp(t.TempDir(), "terminal-device-first")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	same, err := os.Open(first.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = same.Close() })
	second, err := os.CreateTemp(t.TempDir(), "terminal-device-second")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if !sameTerminalDevice(first, same) {
		t.Fatal("two handles for the same device were not recognized")
	}
	if sameTerminalDevice(first, second) {
		t.Fatal("different devices were recognized as the same device")
	}
	if sameTerminalDevice(nil, first) || sameTerminalDevice(first, nil) {
		t.Fatal("a nil file was recognized as the same device")
	}
}

func TestRedirectedFinalOutputHasNoANSIOrBell(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"1s", "--final-only"}, nil, &stdout, &stderr)
	if code != exitOK || stdout.String() != "Time's up!\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.ContainsAny(stdout.String(), "\x1b\a") {
		t.Fatalf("redirected output contains terminal control bytes: %q", stdout.String())
	}
}

func TestOpenRealKeyboardReturnsGenuineNilForNonTerminal(t *testing.T) {
	t.Parallel()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = read.Close()
		_ = write.Close()
	})

	controller, err := openRealKeyboard(read)
	if err != nil {
		t.Fatalf("openRealKeyboard(pipe) error = %v, want nil", err)
	}
	if controller != nil {
		t.Fatalf("openRealKeyboard(pipe) = %+v, want a nil keyboardController interface", controller)
	}
}
