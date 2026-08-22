package cli

import (
	"strings"
	"testing"

	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

func TestParseOptionsAfterDuration(t *testing.T) {
	t.Parallel()
	opts, err := parseOptions([]string{"10m", "--title", "Tea", "--message=Ready", "--fullscreen", "--loop", "--bell-count", "2", "--lang", "es"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.duration != "10m" || opts.title != "Tea" || opts.message != "Ready" ||
		!opts.fullscreen || !opts.loop || opts.bellCount != 2 || opts.language != localize.Spanish {
		t.Fatalf("options = %+v", opts)
	}
}

func TestParseOptionsJoinsNaturalDurationWords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		wantDuration string
		wantTitle    string
	}{
		{name: "short units", args: []string{"1h", "30m"}, wantDuration: "1h 30m"},
		{name: "Spanish minutes", args: []string{"5", "minutos"}, wantDuration: "5 minutos"},
		{
			name: "Spanish phrase", args: []string{"1", "hora", "y", "30", "minutos"},
			wantDuration: "1 hora y 30 minutos",
		},
		{
			name: "option inside phrase", args: []string{"1", "hora", "--title", "Té", "y", "30", "minutos"},
			wantDuration: "1 hora y 30 minutos", wantTitle: "Té",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			opts, err := parseOptions(test.args)
			if err != nil {
				t.Fatalf("parseOptions(%q): %v", test.args, err)
			}
			if opts.duration != test.wantDuration || opts.title != test.wantTitle {
				t.Fatalf("parseOptions(%q) = %+v, want duration %q title %q", test.args, opts, test.wantDuration, test.wantTitle)
			}
		})
	}
}

func TestParseSubcommands(t *testing.T) {
	t.Parallel()
	versionOpts, err := parseOptions([]string{"version"})
	if err != nil || versionOpts.command != commandVersion {
		t.Fatalf("version options=%+v err=%v", versionOpts, err)
	}
	completionOpts, err := parseOptions([]string{"completion", "fish"})
	if err != nil || completionOpts.command != commandCompletion || completionOpts.shell != "fish" {
		t.Fatalf("completion options=%+v err=%v", completionOpts, err)
	}
}

func TestParseOptionsErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "version arguments", args: []string{"version", "extra"}, want: "version does not accept arguments"},
		{name: "completion missing shell", args: []string{"completion"}, want: "usage: timer-cli completion"},
		{name: "completion extra argument", args: []string{"completion", "bash", "extra"}, want: "usage: timer-cli completion"},
		{name: "title missing value", args: []string{"10m", "--title"}, want: "--title requires a value"},
		{name: "message missing value", args: []string{"10m", "--message"}, want: "--message requires a value"},
		{name: "bell count missing value", args: []string{"10m", "--bell-count"}, want: "--bell-count requires a value"},
		{name: "language missing value", args: []string{"10m", "--lang"}, want: "--lang requires a value"},
		{name: "language empty", args: []string{"10m", "--lang="}, want: "--lang must be auto, en, or es"},
		{name: "language unsupported", args: []string{"10m", "--lang", "fr"}, want: "--lang must be auto, en, or es"},
		{name: "bell count zero", args: []string{"10m", "--bell-count=0"}, want: "--bell-count must be 1, 2, or 3"},
		{name: "bell count high", args: []string{"10m", "--bell-count", "4"}, want: "--bell-count must be 1, 2, or 3"},
		{name: "bell count text", args: []string{"10m", "--bell-count=many"}, want: "--bell-count must be 1, 2, or 3"},
		{name: "unknown option", args: []string{"10m", "--unknown"}, want: "unknown option"},
		{name: "duration missing after options", args: []string{"--quiet", "--no-bell"}, want: "duration is required"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseOptions(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseOptions(%q) error = %v, want substring %q", test.args, err, test.want)
			}
		})
	}
}

func TestParseEqualsValueOptions(t *testing.T) {
	t.Parallel()
	opts, err := parseOptions([]string{"--title=Té", "--message=Listo", "--bell-count=3", "--lang=es", "5", "minutos"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.title != "Té" || opts.message != "Listo" || opts.bellCount != 3 || opts.duration != "5 minutos" || opts.language != localize.Spanish {
		t.Fatalf("options = %+v", opts)
	}
}

func TestCompletionUsageUsesPublicCommandName(t *testing.T) {
	t.Parallel()
	_, err := parseOptions([]string{"completion"})
	if err == nil || err.Error() != "usage: timer-cli completion <bash|zsh|fish>" {
		t.Fatalf("completion usage error = %v", err)
	}
	legacyCommand := "timer"
	if strings.Contains(err.Error(), "usage: "+legacyCommand+" completion") {
		t.Fatalf("completion usage contains legacy command: %q", err)
	}
}

func TestMetadataOptionsAreAcceptedBeforeDuration(t *testing.T) {
	t.Parallel()
	valueFor := map[string]string{
		"title":      "Tea",
		"message":    "Ready",
		"bell-count": "2",
		"lang":       "en",
	}

	for _, option := range cliinfo.Options() {
		option := option
		t.Run("--"+option.Long, func(t *testing.T) {
			t.Parallel()
			args := []string{"--" + option.Long}
			if option.TakesValue {
				args = append(args, valueFor[option.Long])
			}
			if option.Long != "help" && option.Long != "version" {
				args = append(args, "10m")
			}
			if _, err := parseOptions(args); err != nil {
				t.Fatalf("parseOptions(%q) failed: %v", args, err)
			}
		})

		if option.Short != "" {
			t.Run("-"+option.Short, func(t *testing.T) {
				t.Parallel()
				args := []string{"-" + option.Short}
				if option.Long != "help" {
					args = append(args, "10m")
				}
				if _, err := parseOptions(args); err != nil {
					t.Fatalf("parseOptions(%q) failed: %v", args, err)
				}
			})
		}
	}
}

func TestLanguageIsGlobalAndLastChoiceWins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args    []string
		command command
		want    localize.Language
	}{
		{args: []string{"--lang", "es", "version"}, command: commandVersion, want: localize.Spanish},
		{args: []string{"version", "--lang=es"}, command: commandVersion, want: localize.Spanish},
		{args: []string{"--lang=es", "completion", "fish"}, command: commandCompletion, want: localize.Spanish},
		{args: []string{"completion", "fish", "--lang", "es"}, command: commandCompletion, want: localize.Spanish},
		{args: []string{"10m", "--lang", "en", "--lang=es"}, command: commandTimer, want: localize.Spanish},
		{args: []string{"--lang", "auto", "10m"}, command: commandTimer, want: localize.Auto},
	}
	for _, test := range tests {
		opts, err := parseOptions(test.args)
		if err != nil || opts.command != test.command || opts.language != test.want {
			t.Errorf("parseOptions(%q) = %+v, %v; want command=%v language=%q", test.args, opts, err, test.command, test.want)
		}
	}
}

func TestLanguageScannerPreservesOtherOptionValues(t *testing.T) {
	t.Parallel()
	opts, err := parseOptions([]string{"10m", "--title", "--lang=es", "--message", "--lang", "--lang", "es"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.title != "--lang=es" || opts.message != "--lang" || opts.language != localize.Spanish {
		t.Fatalf("options = %+v", opts)
	}
}

func TestOptionErrorsHaveSpanishTranslations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  *optionError
		want string
	}{
		{err: &optionError{code: optionErrorDurationRequired}, want: "se requiere una duración"},
		{err: &optionError{code: optionErrorVersionArguments}, want: "version no acepta argumentos"},
		{err: &optionError{code: optionErrorCompletionUsage}, want: "uso: timer-cli completion"},
		{err: &optionError{code: optionErrorRequiresValue, value: "--title"}, want: "--title requiere un valor"},
		{err: &optionError{code: optionErrorUnknownOption, value: "--wat"}, want: "opción desconocida \"--wat\""},
		{err: &optionError{code: optionErrorOutputModes}, want: "mutuamente excluyentes"},
		{err: &optionError{code: optionErrorLoopJSON}, want: "--loop no se puede combinar con --json"},
		{err: &optionError{code: optionErrorBellCount}, want: "--bell-count debe ser 1, 2 o 3"},
		{err: &optionError{code: optionErrorLanguageChoice}, want: "--lang debe ser auto, en o es"},
	}
	for _, test := range tests {
		if got := test.err.localized(localize.Spanish); !strings.Contains(got, test.want) {
			t.Errorf("localized(%d) = %q, want substring %q", test.err.code, got, test.want)
		}
	}
	if got := (&optionError{}).localized(localize.Spanish); got != "invalid options" {
		t.Fatalf("unknown option error = %q", got)
	}
	// Exhaustiveness: every valid optionErrorCode must render Spanish text and
	// must not silently fall through to the generic "invalid options" default.
	for code := optionErrorCode(1); code < optionErrorCodeCount; code++ {
		got := (&optionError{code: code}).localized(localize.Spanish)
		if got == "" {
			t.Errorf("option error code %d has an empty Spanish translation", code)
		}
		if got == "invalid options" {
			t.Errorf("option error code %d falls back to the generic \"invalid options\" message", code)
		}
	}
}

func TestOutputModesAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "quiet and final only", args: []string{"10m", "--quiet", "--final-only"}},
		{name: "final only and quiet", args: []string{"10m", "--final-only", "--quiet"}},
		{name: "quiet and json", args: []string{"10m", "--quiet", "--json"}},
		{name: "json and quiet", args: []string{"10m", "--json", "--quiet"}},
		{name: "final only and json", args: []string{"10m", "--final-only", "--json"}},
		{name: "json and final only", args: []string{"10m", "--json", "--final-only"}},
		{name: "all three", args: []string{"10m", "--quiet", "--final-only", "--json"}},
		{name: "reversed all three", args: []string{"--json", "--final-only", "--quiet", "10m"}},
		{name: "before duration", args: []string{"--quiet", "--json", "10m"}},
		{name: "around duration", args: []string{"--final-only", "10m", "--quiet"}},
		{name: "short quiet alias", args: []string{"-q", "10m", "--json"}},
	}
	const want = "--quiet, --final-only, and --json are mutually exclusive"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseOptions(tt.args); err == nil || err.Error() != want {
				t.Fatalf("parseOptions(%q) error = %v, want %q", tt.args, err, want)
			}
		})
	}
}

func TestSingleOutputModesRemainAccepted(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--quiet", "10m"},
		{"10m", "--final-only"},
		{"--json", "10m"},
		{"--loop", "--quiet", "10m"},
		{"10m", "--loop", "--final-only"},
	} {
		if _, err := parseOptions(args); err != nil {
			t.Errorf("parseOptions(%q) failed: %v", args, err)
		}
	}
}

func TestLoopAndJSONAreIncompatible(t *testing.T) {
	t.Parallel()
	const want = "--loop cannot be combined with --json"
	for _, args := range [][]string{
		{"10m", "--loop", "--json"},
		{"--json", "--loop", "10m"},
	} {
		if _, err := parseOptions(args); err == nil || err.Error() != want {
			t.Errorf("parseOptions(%q) error = %v, want %q", args, err, want)
		}
	}
}

func FuzzParseOptionsMaintainsInvariants(f *testing.F) {
	for _, seed := range []string{
		"",
		"10m",
		"--lang\x00es\x00version",
		"1\x00hora\x00y\x0030\x00minutos\x00--title\x00Té",
		"--title\x00--lang=es\x00--message\x00--lang\x00--lang\x00es\x001s",
		"--quiet\x00--final-only\x001s",
		"completion\x00zsh\x00--lang=es",
		"-999999999999999999999999h",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded string) {
		var args []string
		if encoded != "" {
			args = strings.Split(encoded, "\x00")
			if len(args) > 64 {
				args = args[:64]
			}
		}

		opts, err := parseOptions(args)
		if err != nil {
			return
		}
		if opts.bellCount < 1 || opts.bellCount > 3 {
			t.Fatalf("parseOptions(%q) returned bell count %d", args, opts.bellCount)
		}
		switch opts.language {
		case localize.Auto, localize.English, localize.Spanish:
		default:
			t.Fatalf("parseOptions(%q) returned language %q", args, opts.language)
		}
		switch opts.command {
		case commandHelp, commandVersion, commandCompletion:
		case commandTimer:
			if opts.duration == "" {
				t.Fatalf("parseOptions(%q) returned an empty timer duration", args)
			}
			outputModes := 0
			for _, enabled := range []bool{opts.quiet, opts.finalOnly, opts.json} {
				if enabled {
					outputModes++
				}
			}
			if outputModes > 1 {
				t.Fatalf("parseOptions(%q) accepted %d exclusive output modes", args, outputModes)
			}
			if opts.loop && opts.json {
				t.Fatalf("parseOptions(%q) accepted --loop with --json", args)
			}
		default:
			t.Fatalf("parseOptions(%q) returned unknown command %d", args, opts.command)
		}
	})
}
