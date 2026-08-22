package localize

import (
	"slices"
	"strings"
	"testing"
)

func TestParseChoice(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]Language{
		"auto":   Auto,
		" AUTO ": Auto,
		"en":     English,
		"EN":     English,
		"es":     Spanish,
		"Es":     Spanish,
	} {
		got, err := ParseChoice(input)
		if err != nil || got != want {
			t.Errorf("ParseChoice(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "fr", "es-ES", "spanish"} {
		if got, err := ParseChoice(input); err == nil || got != Auto {
			t.Errorf("ParseChoice(%q) = %q, %v; want Auto and an error", input, got, err)
		}
	}
}

func TestFormattedMessagesAcceptTheirArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		message Message
		args    []any
	}{
		{message: RemainingFormat, args: []any{"00:42"}},
		{message: EndsAtFormat, args: []any{"22:53", "00:42"}},
		{message: CompletionUsageFormat, args: []any{"timer-cli", "bash|zsh|fish"}},
		{message: ErrorRequiresValueFormat, args: []any{"--title"}},
		{message: ErrorUnknownOptionFormat, args: []any{"--unknown"}},
		{message: ErrorUnsupportedShellFormat, args: []any{"powershell", "bash, zsh, fish"}},
	}
	for _, language := range []Language{English, Spanish} {
		for _, test := range tests {
			if got := Format(language, test.message, test.args...); strings.Contains(got, "%!") {
				t.Errorf("Format(%q, %d) = %q", language, test.message, got)
			}
		}
	}
}

func TestResolvePrecedenceAndLocaleForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		choice Language
		env    map[string]string
		want   Language
	}{
		{name: "empty", want: English},
		{name: "explicit English", choice: English, env: map[string]string{"LC_ALL": "es_ES.UTF-8"}, want: English},
		{name: "explicit Spanish", choice: Spanish, env: map[string]string{"LC_ALL": "C"}, want: Spanish},
		{name: "LC ALL", env: map[string]string{"LC_ALL": "es_ES.UTF-8", "LC_MESSAGES": "en_US.UTF-8"}, want: Spanish},
		{name: "LC messages", env: map[string]string{"LC_MESSAGES": "es-MX", "LANG": "en_US.UTF-8"}, want: Spanish},
		{name: "LANG", env: map[string]string{"LANG": "ES_es.utf8@traditional"}, want: Spanish},
		{name: "empty variables ignored", env: map[string]string{"LC_ALL": " ", "LC_MESSAGES": "", "LANG": "es"}, want: Spanish},
		{name: "LC CTYPE is unrelated", env: map[string]string{"LC_CTYPE": "es_ES.UTF-8", "LANG": "en_US.UTF-8"}, want: English},
		{name: "C locale", env: map[string]string{"LC_ALL": "C"}, want: English},
		{name: "POSIX locale", env: map[string]string{"LC_ALL": "POSIX"}, want: English},
		{name: "C UTF-8", env: map[string]string{"LC_ALL": "C.UTF-8"}, want: English},
		{name: "unsupported locale", env: map[string]string{"LANG": "de_DE.UTF-8"}, want: English},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(name string) string { return test.env[name] }
			if got := Resolve(test.choice, getenv); got != test.want {
				t.Fatalf("Resolve(%q) = %q, want %q", test.choice, got, test.want)
			}
		})
	}
	if got := Resolve(Auto, nil); got != English {
		t.Fatalf("Resolve(Auto, nil) = %q, want English", got)
	}
}

func TestCatalogsAreComplete(t *testing.T) {
	t.Parallel()
	for message := Message(0); message < messageCount; message++ {
		if english[message] == "" {
			t.Errorf("English message %d is empty", message)
		}
		if spanish[message] == "" {
			t.Errorf("Spanish message %d is empty", message)
		}
	}
	if got := Text(Auto, TimeUp); got != "Time's up!" {
		t.Fatalf("unresolved language fallback = %q", got)
	}
	if got := Text(Spanish, TimeUp); got != "¡Se acabó el tiempo!" {
		t.Fatalf("Spanish completion = %q", got)
	}
	if got := Text(English, messageCount); got != "" {
		t.Fatalf("out-of-range message = %q", got)
	}
}

func TestChoicesReturnsCopy(t *testing.T) {
	t.Parallel()
	want := []string{"auto", "en", "es"}
	got := Choices()
	if !slices.Equal(got, want) {
		t.Fatalf("Choices() = %v, want %v", got, want)
	}
	got[0] = "changed"
	if !slices.Equal(Choices(), want) {
		t.Fatal("Choices returned mutable package storage")
	}
}
