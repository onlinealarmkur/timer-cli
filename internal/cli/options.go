package cli

import (
	"strconv"
	"strings"

	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

type command int

const (
	commandTimer command = iota
	commandHelp
	commandVersion
	commandCompletion
)

type options struct {
	command    command
	duration   string
	shell      string
	title      string
	message    string
	fullscreen bool
	noBell     bool
	bellCount  int
	loop       bool
	quiet      bool
	finalOnly  bool
	json       bool
	ascii      bool
	controls   bool
	language   localize.Language
}

type optionErrorCode uint8

const (
	optionErrorDurationRequired optionErrorCode = iota + 1
	optionErrorVersionArguments
	optionErrorCompletionUsage
	optionErrorRequiresValue
	optionErrorUnknownOption
	optionErrorOutputModes
	optionErrorLoopJSON
	optionErrorBellCount
	optionErrorLanguageChoice
	// optionErrorCodeCount is a sentinel for exhaustiveness tests; not a valid code.
	optionErrorCodeCount
)

type optionError struct {
	code  optionErrorCode
	value string
}

func (e *optionError) Error() string { return e.localized(localize.English) }

func (e *optionError) localized(language localize.Language) string {
	switch e.code {
	case optionErrorDurationRequired:
		return localize.Text(language, localize.ErrorDurationRequired)
	case optionErrorVersionArguments:
		return localize.Text(language, localize.ErrorVersionArguments)
	case optionErrorCompletionUsage:
		return localize.Format(language, localize.CompletionUsageFormat, cliinfo.ProgramName, strings.Join(cliinfo.Shells(), "|"))
	case optionErrorRequiresValue:
		return localize.Format(language, localize.ErrorRequiresValueFormat, e.value)
	case optionErrorUnknownOption:
		return localize.Format(language, localize.ErrorUnknownOptionFormat, e.value)
	case optionErrorOutputModes:
		return localize.Text(language, localize.ErrorOutputModesExclusive)
	case optionErrorLoopJSON:
		return localize.Text(language, localize.ErrorLoopJSON)
	case optionErrorBellCount:
		return localize.Text(language, localize.ErrorBellCount)
	case optionErrorLanguageChoice:
		return localize.Text(language, localize.ErrorLanguageChoice)
	default:
		return "invalid options"
	}
}

func newOptionError(code optionErrorCode, value ...string) error {
	err := &optionError{code: code}
	if len(value) > 0 {
		err.value = value[0]
	}
	return err
}

func parseOptions(args []string) (options, error) {
	opts := options{bellCount: 1, language: localize.Auto}
	var err error
	args, opts.language, err = stripLanguageOptions(args, opts.language)
	if err != nil {
		return opts, err
	}
	var durationParts []string
	if len(args) == 0 {
		return opts, newOptionError(optionErrorDurationRequired)
	}
	if args[0] == cliinfo.CommandVersion {
		if len(args) != 1 {
			return opts, newOptionError(optionErrorVersionArguments)
		}
		opts.command = commandVersion
		return opts, nil
	}
	if args[0] == cliinfo.CommandCompletion {
		if len(args) != 2 {
			return opts, newOptionError(optionErrorCompletionUsage)
		}
		opts.command = commandCompletion
		opts.shell = args[1]
		return opts, nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			opts.command = commandHelp
			return opts, nil
		case arg == "--version":
			opts.command = commandVersion
			return opts, nil
		case arg == "--fullscreen":
			opts.fullscreen = true
		case arg == "--no-bell":
			opts.noBell = true
		case arg == "--loop":
			opts.loop = true
		case arg == "--quiet" || arg == "-q":
			opts.quiet = true
		case arg == "--final-only":
			opts.finalOnly = true
		case arg == "--json":
			opts.json = true
		case arg == "--ascii":
			opts.ascii = true
		case arg == "--controls":
			opts.controls = true
		case arg == "--title" || arg == "--message" || arg == "--bell-count":
			if i+1 >= len(args) {
				return opts, newOptionError(optionErrorRequiresValue, arg)
			}
			i++
			if err := setOption(&opts, strings.TrimPrefix(arg, "--"), args[i]); err != nil {
				return opts, err
			}
		case strings.HasPrefix(arg, "--title=") || strings.HasPrefix(arg, "--message=") || strings.HasPrefix(arg, "--bell-count="):
			name, value, _ := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			if err := setOption(&opts, name, value); err != nil {
				return opts, err
			}
		case len(arg) > 1 && arg[0] == '-' && arg[1] >= '0' && arg[1] <= '9':
			durationParts = append(durationParts, arg)
		case strings.HasPrefix(arg, "-"):
			return opts, newOptionError(optionErrorUnknownOption, arg)
		default:
			durationParts = append(durationParts, arg)
		}
	}
	opts.duration = strings.Join(durationParts, " ")
	outputModes := 0
	for _, enabled := range []bool{opts.quiet, opts.finalOnly, opts.json} {
		if enabled {
			outputModes++
		}
	}
	if outputModes > 1 {
		return opts, newOptionError(optionErrorOutputModes)
	}
	if opts.loop && opts.json {
		return opts, newOptionError(optionErrorLoopJSON)
	}
	if opts.duration == "" {
		return opts, newOptionError(optionErrorDurationRequired)
	}
	return opts, nil
}

func setOption(opts *options, name, value string) error {
	switch name {
	case "title":
		opts.title = value
	case "message":
		opts.message = value
	case "bell-count":
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 || count > 3 {
			return newOptionError(optionErrorBellCount)
		}
		opts.bellCount = count
	}
	return nil
}

// stripLanguageOptions handles --lang before command dispatch so it behaves as
// a true global option and can select the language used for any later error.
func stripLanguageOptions(args []string, language localize.Language) ([]string, localize.Language, error) {
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var value string
		switch {
		case arg == "--lang":
			if i+1 >= len(args) {
				return remaining, language, newOptionError(optionErrorRequiresValue, arg)
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, "--lang="):
			value = strings.TrimPrefix(arg, "--lang=")
		case arg == "--title" || arg == "--message" || arg == "--bell-count":
			remaining = append(remaining, arg)
			if i+1 < len(args) {
				i++
				remaining = append(remaining, args[i])
			}
			continue
		default:
			remaining = append(remaining, arg)
			continue
		}

		parsed, err := localize.ParseChoice(value)
		if err != nil {
			return remaining, language, newOptionError(optionErrorLanguageChoice)
		}
		language = parsed
	}
	return remaining, language, nil
}
