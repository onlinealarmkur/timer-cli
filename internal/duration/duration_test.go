package duration

import (
	"errors"
	"testing"
	"time"

	"github.com/onlinealarmkur/timer-cli/internal/timerlimit"
)

func TestParseSupportedFormats(t *testing.T) {
	t.Parallel()
	tests := map[string]time.Duration{
		"90s":                           90 * time.Second,
		"10m":                           10 * time.Minute,
		"1h":                            time.Hour,
		"1h30m":                         90 * time.Minute,
		"1h 30m":                        90 * time.Minute,
		"1m30s":                         90 * time.Second,
		"01:30":                         90 * time.Second,
		"01:30:00":                      90 * time.Minute,
		"720h":                          timerlimit.MaxDuration,
		"5 minutos":                     5 * time.Minute,
		"1 minuto":                      time.Minute,
		"1 hora y 30 minutos":           90 * time.Minute,
		"2 HORAS 5 MINUTOS 10 SEGUNDOS": 2*time.Hour + 5*time.Minute + 10*time.Second,
		"1h y 30 minutos":               90 * time.Minute,
		"5minutos":                      5 * time.Minute,
		"1 hora 30m":                    90 * time.Minute,
		"1 hour and 30 minutes":         90 * time.Minute,
		"2 HOURS 5 MINUTES 10 SECONDS":  2*time.Hour + 5*time.Minute + 10*time.Second,
		"1 heure et 30 minutes":         90 * time.Minute,
		"2heures":                       2 * time.Hour,
		"1 Stunde und 30 Minuten":       90 * time.Minute,
		"5Minuten":                      5 * time.Minute,
		"1 ora e 30 minuti":             90 * time.Minute,
		"1 secondo":                     time.Second,
		"1 hora e 30 minutos":           90 * time.Minute,
		"1 uur en 30 minuten":           90 * time.Minute,
		"2 uren 5 minuten 10 seconden":  2*time.Hour + 5*time.Minute + 10*time.Second,
		"1h und 30 minutes":             90 * time.Minute,
	}
	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", input, err)
			}
			if got != want {
				t.Fatalf("Parse(%q) = %v, want %v", input, got, want)
			}
		})
	}
}

func TestParseMaximumBoundary(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"720h", "720:00:00"} {
		input := input
		t.Run("accept "+input, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(input)
			if err != nil || got != timerlimit.MaxDuration {
				t.Fatalf("Parse(%q) = %v, %v; want %v", input, got, err, timerlimit.MaxDuration)
			}
		})
	}
	for _, input := range []string{"720h1s", "720:00:01"} {
		input := input
		t.Run("reject "+input, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(input); err == nil {
				t.Fatalf("Parse(%q) accepted one second above the maximum", input)
			} else if code, ok := ErrorCodeOf(err); !ok || code != ErrorAboveMaximum {
				t.Fatalf("Parse(%q) error = %v, code=%d, ok=%v", input, err, code, ok)
			}
		})
	}
}

func TestParseErrorsExposeStableCodes(t *testing.T) {
	t.Parallel()
	tests := map[string]ErrorCode{
		"":                          ErrorRequired,
		"-1m":                       ErrorNegative,
		"0":                         ErrorZero,
		"01:02:03:04":               ErrorAmbiguousColon,
		"500ms":                     ErrorBelowMinimum,
		"721h":                      ErrorAboveMaximum,
		"coffee":                    ErrorInvalidUnits,
		"999999999999999999999999h": ErrorTooLarge,
		"1:30":                      ErrorInvalidMMSS,
		"00:60":                     ErrorMMSSSeconds,
		"1:02:03":                   ErrorInvalidHHMMSS,
		"01:60:00":                  ErrorHHMMSSFields,
	}
	for input, want := range tests {
		_, err := Parse(input)
		got, ok := ErrorCodeOf(err)
		if !ok || got != want {
			t.Errorf("Parse(%q) error = %v, code=%d, ok=%v; want code=%d", input, err, got, ok, want)
		}
	}
	if code, ok := ErrorCodeOf(errors.New("unrelated")); ok || code != 0 {
		t.Fatalf("unrelated error code=%d ok=%v", code, ok)
	}
	if got := (&ParseError{}).Error(); got != "invalid duration" {
		t.Fatalf("zero ParseError = %q", got)
	}
}

func TestLocalizedUnitVocabulary(t *testing.T) {
	t.Parallel()
	languages := []struct {
		name            string
		hour, hours     string
		minute, minutes string
		second, seconds string
	}{
		{name: "English", hour: "hour", hours: "hours", minute: "minute", minutes: "minutes", second: "second", seconds: "seconds"},
		{name: "Spanish", hour: "hora", hours: "horas", minute: "minuto", minutes: "minutos", second: "segundo", seconds: "segundos"},
		{name: "Portuguese", hour: "hora", hours: "horas", minute: "minuto", minutes: "minutos", second: "segundo", seconds: "segundos"},
		{name: "French", hour: "heure", hours: "heures", minute: "minute", minutes: "minutes", second: "seconde", seconds: "secondes"},
		{name: "German", hour: "Stunde", hours: "Stunden", minute: "Minute", minutes: "Minuten", second: "Sekunde", seconds: "Sekunden"},
		{name: "Italian", hour: "ora", hours: "ore", minute: "minuto", minutes: "minuti", second: "secondo", seconds: "secondi"},
		{name: "Dutch", hour: "uur", hours: "uren", minute: "minuut", minutes: "minuten", second: "seconde", seconds: "seconden"},
	}
	for _, language := range languages {
		language := language
		t.Run(language.name, func(t *testing.T) {
			t.Parallel()
			tests := []struct {
				input string
				want  time.Duration
			}{
				{input: "1 " + language.hour, want: time.Hour},
				{input: "2 " + language.hours, want: 2 * time.Hour},
				{input: "1 " + language.minute, want: time.Minute},
				{input: "2 " + language.minutes, want: 2 * time.Minute},
				{input: "1 " + language.second, want: time.Second},
				{input: "2 " + language.seconds, want: 2 * time.Second},
			}
			for _, test := range tests {
				got, err := Parse(test.input)
				if err != nil || got != test.want {
					t.Errorf("Parse(%q) = %v, %v; want %v", test.input, got, err, test.want)
				}
			}
		})
	}
}

func TestParseUnicodeWhitespaceAndFormatBoundaries(t *testing.T) {
	t.Parallel()
	accepted := map[string]time.Duration{
		"1\u00a0heure\u00a0et\u00a030\u00a0minutes": 90 * time.Minute,
		"1\u202fheure\u202fet\u202f30\u202fminutes": 90 * time.Minute,
	}
	for input, want := range accepted {
		input, want := input, want
		t.Run("accept "+input, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(input)
			if err != nil || got != want {
				t.Fatalf("Parse(%q) = %v, %v; want %v", input, got, err, want)
			}
		})
	}

	rejected := map[string]string{
		"zero-width space":     "5\u200bminutos",
		"combining alteration": "5 minu\u0301tos",
		"malformed UTF-8":      string([]byte{'5', ' ', 0xff, 'm', 'i', 'n', 'u', 't', 'e', 's'}),
	}
	for name, input := range rejected {
		name, input := name, input
		t.Run("reject "+name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(input); err == nil {
				t.Fatalf("Parse(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	// ParseError.Error() is a single generic message for every code (the
	// user-facing wording is resolved by cli.localizedDurationError via
	// ErrorCodeOf, keyed on Code -- see localizedDurationError and
	// durationErrorMessages in internal/cli). This test proves Parse still
	// rejects each of these inputs; exact code-per-input mapping is covered
	// by TestParseErrorsExposeStableCodes.
	const want = "invalid duration"
	inputs := []string{
		"", "coffee", "cinco minutos", "1 minuto extra", "30 minutos 1 hora",
		"1 hora y", "y 1 hora", "1.5 horas", "1 día", "-1m", "0", "0s",
		"0 minutos", "500ms", "721h", "721 horas", "999999999999999999999999h",
		"1:30", "00:60", "1:02:03", "01:60:00", "01:02:03:04", "30m1h",
		"five minutes", "1 hour plus 30 minutes", "1 Stundes", "1 oras", "1 uurs",
	}
	for _, input := range inputs {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(input)
			if err == nil || err.Error() != want {
				t.Fatalf("Parse(%q) error = %v, want %q", input, err, want)
			}
		})
	}
}

func FuzzParseNeverReturnsOutOfRangeDuration(f *testing.F) {
	for _, seed := range []string{
		"10m", "01:30", "1 hora y 30 minutos", "1 hour and 30 minutes",
		"1 Stunde und 30 Minuten", "1 heure et 30 minutes", "1 ora e 30 minuti",
		"1 uur en 30 minuten", "5minutos", "-1s", "coffee", "721 horas",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		d, err := Parse(input)
		if err == nil && (d < time.Second || d > timerlimit.MaxDuration) {
			t.Fatalf("Parse(%q) = %v without error", input, d)
		}
	})
}
