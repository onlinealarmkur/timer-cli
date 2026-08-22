// Package cliinfo describes the public timer-cli command surface.
package cliinfo

import "github.com/onlinealarmkur/timer-cli/internal/localize"

const (
	// ProgramName is the public executable name.
	ProgramName = "timer-cli"

	// CommandVersion prints build version information.
	CommandVersion = "version"
	// CommandCompletion generates a shell completion script.
	CommandCompletion = "completion"
)

// Command describes a top-level command.
type Command struct {
	Name        string
	Description string
}

// Option describes a command-line option and its presentation metadata.
type Option struct {
	Long        string
	Short       string
	TakesValue  bool
	ValueLabel  string
	Description string
	Conflicts   []string
}

var commands = []struct {
	name        string
	description localize.Message
}{
	{name: CommandVersion, description: localize.CommandVersionDescription},
	{name: CommandCompletion, description: localize.CommandCompletionDescription},
}

var shells = []string{"bash", "zsh", "fish"}

var durationExamples = []string{
	"10m", "90s", "01:30", "5minutes", "5minutos", "2heures", "5Minuten", "5minuti", "2uren",
}

var options = []struct {
	long, short, valueLabel string
	takesValue              bool
	description             localize.Message
	conflicts               []string
}{
	{long: "title", takesValue: true, valueLabel: "TEXT", description: localize.OptionTitleDescription},
	{long: "message", takesValue: true, valueLabel: "TEXT", description: localize.OptionMessageDescription},
	{long: "fullscreen", description: localize.OptionFullscreenDescription},
	{long: "controls", description: localize.OptionControlsDescription},
	{long: "no-bell", description: localize.OptionNoBellDescription},
	{long: "bell-count", takesValue: true, valueLabel: "N", description: localize.OptionBellCountDescription},
	{long: "loop", description: localize.OptionLoopDescription, conflicts: []string{"json"}},
	{long: "quiet", short: "q", description: localize.OptionQuietDescription, conflicts: []string{"final-only", "json"}},
	{long: "final-only", description: localize.OptionFinalOnlyDescription, conflicts: []string{"quiet", "json"}},
	{long: "json", description: localize.OptionJSONDescription, conflicts: []string{"loop", "quiet", "final-only"}},
	{long: "ascii", description: localize.OptionASCIIDescription},
	{long: "lang", takesValue: true, valueLabel: "LANG", description: localize.OptionLanguageDescription},
	{long: "help", short: "h", description: localize.OptionHelpDescription},
	{long: "version", description: localize.OptionVersionDescription},
}

// Commands returns English top-level command descriptions.
func Commands() []Command { return CommandsFor(localize.English) }

// CommandsFor returns localized top-level command descriptions.
func CommandsFor(language localize.Language) []Command {
	result := make([]Command, len(commands))
	for i, command := range commands {
		result[i] = Command{Name: command.name, Description: localize.Text(language, command.description)}
	}
	return result
}

// Shells returns a copy of the supported completion shell names.
func Shells() []string { return append([]string(nil), shells...) }

// DurationExamples returns a copy of the canonical duration examples.
func DurationExamples() []string { return append([]string(nil), durationExamples...) }

// Options returns English public option descriptions.
func Options() []Option { return OptionsFor(localize.English) }

// OptionsFor returns localized public option descriptions.
func OptionsFor(language localize.Language) []Option {
	result := make([]Option, len(options))
	for i, option := range options {
		valueLabel := option.valueLabel
		switch valueLabel {
		case "TEXT":
			valueLabel = localize.Text(language, localize.TextValueLabel)
		case "LANG":
			valueLabel = localize.Text(language, localize.LanguageValueLabel)
		}
		result[i] = Option{
			Long: option.long, Short: option.short, TakesValue: option.takesValue,
			ValueLabel: valueLabel, Description: localize.Text(language, option.description),
			Conflicts: append([]string(nil), option.conflicts...),
		}
	}
	return result
}
