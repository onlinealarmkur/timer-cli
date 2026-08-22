package cliinfo

import (
	"strings"
	"testing"

	durationparse "github.com/onlinealarmkur/timer-cli/internal/duration"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

func TestMetadataIsUnique(t *testing.T) {
	t.Parallel()
	assertUnique := func(kind string, values []string) {
		t.Helper()
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			if value == "" {
				t.Fatalf("%s contains an empty value", kind)
			}
			if seen[value] {
				t.Fatalf("%s contains duplicate %q", kind, value)
			}
			seen[value] = true
		}
	}

	commands := Commands()
	commandNames := make([]string, 0, len(commands))
	for _, command := range commands {
		commandNames = append(commandNames, command.Name)
	}
	assertUnique("commands", commandNames)

	options := Options()
	longFlags := make([]string, 0, len(options))
	shortFlags := make([]string, 0, len(options))
	conflicts := make(map[string]map[string]bool, len(options))
	for _, option := range options {
		longFlags = append(longFlags, option.Long)
		conflicts[option.Long] = make(map[string]bool, len(option.Conflicts))
		for _, conflict := range option.Conflicts {
			if conflict == option.Long {
				t.Fatalf("option --%s conflicts with itself", option.Long)
			}
			conflicts[option.Long][conflict] = true
		}
		if option.Short != "" {
			shortFlags = append(shortFlags, option.Short)
		}
		if option.TakesValue != (option.ValueLabel != "") {
			t.Fatalf("option --%s has inconsistent value metadata", option.Long)
		}
		if option.Description == "" {
			t.Fatalf("option --%s has no description", option.Long)
		}
		if option.Long == "lang" && (option.ValueLabel != "LANG" || !option.TakesValue) {
			t.Fatalf("--lang metadata = %+v", option)
		}
	}
	assertUnique("long flags", longFlags)
	assertUnique("short flags", shortFlags)
	assertUnique("shells", Shells())
	assertUnique("duration examples", DurationExamples())
	for option, optionConflicts := range conflicts {
		for conflict := range optionConflicts {
			if _, ok := conflicts[conflict]; !ok {
				t.Fatalf("option --%s names unknown conflict --%s", option, conflict)
			}
			if !conflicts[conflict][option] {
				t.Fatalf("option conflict --%s/--%s is not symmetric", option, conflict)
			}
		}
	}
}

func TestSpanishMetadataIsComplete(t *testing.T) {
	t.Parallel()
	englishCommands := CommandsFor(localize.English)
	spanishCommands := CommandsFor(localize.Spanish)
	if len(spanishCommands) != len(englishCommands) {
		t.Fatalf("Spanish commands=%d, English commands=%d", len(spanishCommands), len(englishCommands))
	}
	for i, command := range spanishCommands {
		if command.Name != englishCommands[i].Name || command.Description == "" || command.Description == englishCommands[i].Description {
			t.Errorf("Spanish command %d = %+v, English = %+v", i, command, englishCommands[i])
		}
	}

	englishOptions := OptionsFor(localize.English)
	spanishOptions := OptionsFor(localize.Spanish)
	if len(spanishOptions) != len(englishOptions) {
		t.Fatalf("Spanish options=%d, English options=%d", len(spanishOptions), len(englishOptions))
	}
	for i, option := range spanishOptions {
		if option.Long != englishOptions[i].Long || option.Description == "" || option.Description == englishOptions[i].Description {
			t.Errorf("Spanish option %d = %+v, English = %+v", i, option, englishOptions[i])
		}
		if option.Long == "lang" && option.ValueLabel != "IDIOMA" {
			t.Errorf("Spanish --lang value label = %q", option.ValueLabel)
		}
		if (option.Long == "title" || option.Long == "message") && option.ValueLabel != "TEXTO" {
			t.Errorf("Spanish --%s value label = %q", option.Long, option.ValueLabel)
		}
	}
}

func TestMetadataAccessorsReturnCopies(t *testing.T) {
	t.Parallel()

	commands := Commands()
	commands[0].Name = "changed"
	if Commands()[0].Name == "changed" {
		t.Fatal("Commands returned mutable package storage")
	}

	shells := Shells()
	shells[0] = "changed"
	if Shells()[0] == "changed" {
		t.Fatal("Shells returned mutable package storage")
	}

	examples := DurationExamples()
	examples[0] = "changed"
	if DurationExamples()[0] == "changed" {
		t.Fatal("DurationExamples returned mutable package storage")
	}

	options := Options()
	options[0].Long = "changed"
	if Options()[0].Long == "changed" {
		t.Fatal("Options returned mutable package storage")
	}
	for i, option := range options {
		if len(option.Conflicts) == 0 {
			continue
		}
		options[i].Conflicts[0] = "changed"
		if Options()[i].Conflicts[0] == "changed" {
			t.Fatal("Options returned mutable conflict storage")
		}
		break
	}
}

func TestDurationExamplesFollowParserContract(t *testing.T) {
	t.Parallel()
	for _, example := range DurationExamples() {
		if _, err := durationparse.Parse(example); err != nil {
			t.Errorf("completion duration example %q is not accepted by the parser: %v", example, err)
		}
	}
}

func TestOutputModeDescriptionsStateExclusivity(t *testing.T) {
	t.Parallel()
	wantModes := map[string]bool{"quiet": true, "final-only": true, "json": true}
	for _, option := range Options() {
		if !wantModes[option.Long] {
			continue
		}
		if !strings.Contains(option.Description, "Exclusive output mode") {
			t.Errorf("--%s description does not state exclusivity: %q", option.Long, option.Description)
		}
		delete(wantModes, option.Long)
	}
	if len(wantModes) != 0 {
		t.Fatalf("missing output-mode metadata: %v", wantModes)
	}
}
