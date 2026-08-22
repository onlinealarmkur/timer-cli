// Package localize selects and serves the deliberately small set of
// human-interface languages supported by timer-cli.
package localize

import (
	"fmt"
	"strings"
)

// Language identifies a supported interface language. Auto defers to the
// process locale and is resolved before rendering output.
type Language string

const (
	Auto    Language = "auto"
	English Language = "en"
	Spanish Language = "es"
)

var choices = []string{string(Auto), string(English), string(Spanish)}

// ParseChoice validates a value supplied to --lang.
func ParseChoice(value string) (Language, error) {
	switch Language(strings.ToLower(strings.TrimSpace(value))) {
	case Auto:
		return Auto, nil
	case English:
		return English, nil
	case Spanish:
		return Spanish, nil
	default:
		return Auto, fmt.Errorf("unsupported interface language %q", value)
	}
}

// Choices returns the accepted --lang values.
func Choices() []string { return append([]string(nil), choices...) }

// Resolve applies an explicit choice or the POSIX message-locale precedence.
// LC_CTYPE is intentionally not consulted: it controls character handling,
// while LC_MESSAGES controls the language of diagnostic and informative text.
func Resolve(choice Language, getenv func(string) string) Language {
	if choice == English || choice == Spanish {
		return choice
	}
	if getenv == nil {
		return English
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			return languageFromLocale(value)
		}
	}
	return English
}

func languageFromLocale(locale string) Language {
	locale = strings.ToLower(strings.TrimSpace(locale))
	if locale == "c" || locale == "posix" {
		return English
	}
	if end := strings.IndexAny(locale, "_-.@"); end >= 0 {
		locale = locale[:end]
	}
	if locale == string(Spanish) {
		return Spanish
	}
	return English
}

// Message identifies a translatable human-interface string. Command names,
// option names, JSON fields, statuses, and redirected record keys are not
// messages and intentionally remain stable.
type Message uint8

const (
	RemainingFormat Message = iota
	EndsAtFormat
	Paused
	Controls
	TimeUp
	TimerCanceled
	TryHelp
	HelpTagline
	UsageHeading
	DurationsHeading
	DurationHelp
	OptionsHeading
	InterfaceLanguageHeading
	InterfaceLanguageHelp
	InteractiveControlsHeading
	InteractiveControlsHelp
	RedirectedOutputHelp
	CommandVersionDescription
	CommandCompletionDescription
	OptionTitleDescription
	OptionMessageDescription
	OptionFullscreenDescription
	OptionControlsDescription
	OptionNoBellDescription
	OptionBellCountDescription
	OptionLoopDescription
	OptionQuietDescription
	OptionFinalOnlyDescription
	OptionJSONDescription
	OptionASCIIDescription
	OptionLanguageDescription
	OptionHelpDescription
	OptionVersionDescription
	TextValueLabel
	LanguageValueLabel
	DurationValueLabel
	OptionsValueLabel
	CompletionFirstArgumentDescription
	CompletionShellLabel
	CompletionExampleDurationDescription
	CompletionShellDescription
	ErrorDurationRequired
	ErrorVersionArguments
	CompletionUsageFormat
	ErrorRequiresValueFormat
	ErrorUnknownOptionFormat
	ErrorOutputModesExclusive
	ErrorLoopJSON
	ErrorBellCount
	ErrorLanguageChoice
	ErrorNegativeDuration
	ErrorZeroDuration
	ErrorAmbiguousColonDuration
	ErrorMinimumDuration
	ErrorMaximumDuration
	ErrorInvalidUnitDuration
	ErrorDurationTooLarge
	ErrorInvalidMMSS
	ErrorMMSSSeconds
	ErrorInvalidHHMMSS
	ErrorHHMMSSFields
	ErrorUnsupportedShellFormat
	messageCount
)

var english = [...]string{
	RemainingFormat:                      "%s remaining",
	EndsAtFormat:                         "ends %s · %s",
	Paused:                               "PAUSED",
	Controls:                             "Space pause/resume  R restart  +/- one minute  Q/Esc quit",
	TimeUp:                               "Time's up!",
	TimerCanceled:                        "Timer canceled.",
	TryHelp:                              "Try 'timer-cli --help' for usage.",
	HelpTagline:                          "a clear and reliable foreground countdown",
	UsageHeading:                         "Usage",
	DurationsHeading:                     "Durations",
	DurationHelp:                         "  Whole-number units in h, m, s order: 90s, 10m, 1h30m, 1h 30m\n  Unit words also work in English, Spanish, Portuguese, French, German,\n  Italian, and Dutch: 5 minutes, 5 minutos, 5 Minuten\n  Colon notation: MM:SS (01:30) or HH:MM:SS (01:30:00)\n  Each MM:SS field is exactly two digits. In HH:MM:SS, hours use 2-3\n  digits; minutes and seconds are 00-59. Range: 1 second through 30 days.",
	OptionsHeading:                       "Options",
	InterfaceLanguageHeading:             "Interface language",
	InterfaceLanguageHelp:                "  auto follows LC_ALL, LC_MESSAGES, then LANG. Duration words do not select it.",
	InteractiveControlsHeading:           "Interactive controls (TTY only)",
	InteractiveControlsHelp:              "  Space pause/resume   R restart   + add one minute   - subtract one minute\n  Q or Escape quit     Ctrl+C quit cleanly",
	RedirectedOutputHelp:                 "When output is redirected, timer-cli emits readable periodic lines without ANSI.\nClosing the process or terminal stops the timer.",
	CommandVersionDescription:            "Print version information",
	CommandCompletionDescription:         "Generate shell completion",
	OptionTitleDescription:               "Show an optional title",
	OptionMessageDescription:             "Set the completion message (default: Time's up!)",
	OptionFullscreenDescription:          "Use large terminal-friendly digits",
	OptionControlsDescription:            "Show keyboard help in fullscreen mode",
	OptionNoBellDescription:              "Disable the completion bell",
	OptionBellCountDescription:           "Ring 1-3 times (default: 1)",
	OptionLoopDescription:                "Restart after each completion until canceled",
	OptionQuietDescription:               "Exclusive output mode; suppress all regular output (the bell still applies on a TTY)",
	OptionFinalOnlyDescription:           "Exclusive output mode; suppress periodic output and print only the final result",
	OptionJSONDescription:                "Exclusive output mode; emit one machine-readable JSON result and disable bell",
	OptionASCIIDescription:               "Force ASCII progress characters",
	OptionLanguageDescription:            "Set interface language: auto, en, or es (default: auto)",
	OptionHelpDescription:                "Show this help",
	OptionVersionDescription:             "Show version information",
	TextValueLabel:                       "TEXT",
	LanguageValueLabel:                   "LANG",
	DurationValueLabel:                   "duration",
	OptionsValueLabel:                    "options",
	CompletionFirstArgumentDescription:   "duration, command, or option",
	CompletionShellLabel:                 "shell",
	CompletionExampleDurationDescription: "Example duration",
	CompletionShellDescription:           "Completion shell",
	ErrorDurationRequired:                "duration is required; try 10m, 90s, or 01:30",
	ErrorVersionArguments:                "version does not accept arguments",
	CompletionUsageFormat:                "usage: %s completion <%s>",
	ErrorRequiresValueFormat:             "%s requires a value",
	ErrorUnknownOptionFormat:             "unknown option %q",
	ErrorOutputModesExclusive:            "--quiet, --final-only, and --json are mutually exclusive",
	ErrorLoopJSON:                        "--loop cannot be combined with --json",
	ErrorBellCount:                       "--bell-count must be 1, 2, or 3",
	ErrorLanguageChoice:                  "--lang must be auto, en, or es",
	ErrorNegativeDuration:                "duration cannot be negative",
	ErrorZeroDuration:                    "duration must be greater than zero",
	ErrorAmbiguousColonDuration:          "ambiguous colon duration; use exactly MM:SS or HH:MM:SS",
	ErrorMinimumDuration:                 "duration must be at least one second",
	ErrorMaximumDuration:                 "duration cannot exceed 30 days",
	ErrorInvalidUnitDuration:             "invalid duration; use whole numbers such as 10m, 1h30m, or 5 minutes",
	ErrorDurationTooLarge:                "duration is too large; maximum is 30 days",
	ErrorInvalidMMSS:                     "invalid MM:SS duration; use two digits per field, for example 01:30",
	ErrorMMSSSeconds:                     "invalid MM:SS duration: seconds must be 00 through 59",
	ErrorInvalidHHMMSS:                   "invalid HH:MM:SS duration; use 2-3 hour digits and two digits for minutes and seconds",
	ErrorHHMMSSFields:                    "invalid HH:MM:SS duration: minutes and seconds must be 00 through 59",
	ErrorUnsupportedShellFormat:          "unsupported shell %q; choose %s",
}

var spanish = [...]string{
	RemainingFormat:                      "quedan %s",
	EndsAtFormat:                         "termina a las %s · %s",
	Paused:                               "EN PAUSA",
	Controls:                             "Espacio pausar/reanudar  R reiniciar  +/- un minuto  Q/Esc salir",
	TimeUp:                               "¡Se acabó el tiempo!",
	TimerCanceled:                        "Temporizador cancelado.",
	TryHelp:                              "Prueba 'timer-cli --help' para ver el modo de uso.",
	HelpTagline:                          "una cuenta atrás clara y fiable en primer plano",
	UsageHeading:                         "Uso",
	DurationsHeading:                     "Duraciones",
	DurationHelp:                         "  Unidades enteras en orden h, m, s: 90s, 10m, 1h30m, 1h 30m\n  También se admiten palabras en inglés, español, portugués, francés,\n  alemán, italiano y neerlandés: 5 minutes, 5 minutos, 5 Minuten\n  Notación con dos puntos: MM:SS (01:30) o HH:MM:SS (01:30:00)\n  Cada campo de MM:SS tiene dos dígitos. En HH:MM:SS, las horas tienen\n  2-3 dígitos; minutos y segundos van de 00 a 59. Intervalo: de 1 segundo a 30 días.",
	OptionsHeading:                       "Opciones",
	InterfaceLanguageHeading:             "Idioma de la interfaz",
	InterfaceLanguageHelp:                "  auto usa, por orden, LC_ALL, LC_MESSAGES y LANG. Las palabras de duración no lo seleccionan.",
	InteractiveControlsHeading:           "Controles interactivos (solo TTY)",
	InteractiveControlsHelp:              "  Espacio pausar/reanudar   R reiniciar   + añadir un minuto   - restar un minuto\n  Q o Escape salir              Ctrl+C salir limpiamente",
	RedirectedOutputHelp:                 "Al redirigir la salida, timer-cli emite líneas periódicas legibles sin ANSI.\nCerrar el proceso o el terminal detiene el temporizador.",
	CommandVersionDescription:            "Muestra información de la versión",
	CommandCompletionDescription:         "Genera autocompletado para el shell",
	OptionTitleDescription:               "Muestra un título opcional",
	OptionMessageDescription:             "Define el mensaje final (predeterminado: ¡Se acabó el tiempo!)",
	OptionFullscreenDescription:          "Usa dígitos grandes adaptados al terminal",
	OptionControlsDescription:            "Muestra la ayuda del teclado en pantalla completa",
	OptionNoBellDescription:              "Desactiva la campana al terminar",
	OptionBellCountDescription:           "Hace sonar la campana entre 1 y 3 veces (predeterminado: 1)",
	OptionLoopDescription:                "Reinicia después de cada finalización hasta que se cancele",
	OptionQuietDescription:               "Modo de salida exclusivo; no muestra la salida normal (la campana sigue activa si la salida es un TTY)",
	OptionFinalOnlyDescription:           "Modo de salida exclusivo; omite la salida periódica y muestra solo el resultado final",
	OptionJSONDescription:                "Modo de salida exclusivo; emite un resultado JSON legible por máquinas y desactiva la campana",
	OptionASCIIDescription:               "Fuerza caracteres de progreso ASCII",
	OptionLanguageDescription:            "Define el idioma de la interfaz: auto, en o es (predeterminado: auto)",
	OptionHelpDescription:                "Muestra esta ayuda",
	OptionVersionDescription:             "Muestra información de la versión",
	TextValueLabel:                       "TEXTO",
	LanguageValueLabel:                   "IDIOMA",
	DurationValueLabel:                   "duración",
	OptionsValueLabel:                    "opciones",
	CompletionFirstArgumentDescription:   "duración, comando u opción",
	CompletionShellLabel:                 "shell",
	CompletionExampleDurationDescription: "Duración de ejemplo",
	CompletionShellDescription:           "Shell de autocompletado",
	ErrorDurationRequired:                "se requiere una duración; prueba 10m, 90s o 01:30",
	ErrorVersionArguments:                "version no acepta argumentos",
	CompletionUsageFormat:                "uso: %s completion <%s>",
	ErrorRequiresValueFormat:             "%s requiere un valor",
	ErrorUnknownOptionFormat:             "opción desconocida %q",
	ErrorOutputModesExclusive:            "--quiet, --final-only y --json son mutuamente excluyentes",
	ErrorLoopJSON:                        "--loop no se puede combinar con --json",
	ErrorBellCount:                       "--bell-count debe ser 1, 2 o 3",
	ErrorLanguageChoice:                  "--lang debe ser auto, en o es",
	ErrorNegativeDuration:                "la duración no puede ser negativa",
	ErrorZeroDuration:                    "la duración debe ser mayor que cero",
	ErrorAmbiguousColonDuration:          "duración con dos puntos ambigua; usa exactamente MM:SS o HH:MM:SS",
	ErrorMinimumDuration:                 "la duración debe ser de al menos un segundo",
	ErrorMaximumDuration:                 "la duración no puede superar 30 días",
	ErrorInvalidUnitDuration:             "duración no válida; usa números enteros como 10m, 1h30m o 5 minutos",
	ErrorDurationTooLarge:                "la duración es demasiado grande; el máximo es 30 días",
	ErrorInvalidMMSS:                     "duración MM:SS no válida; usa dos dígitos por campo, por ejemplo 01:30",
	ErrorMMSSSeconds:                     "duración MM:SS no válida: los segundos deben estar entre 00 y 59",
	ErrorInvalidHHMMSS:                   "duración HH:MM:SS no válida; usa 2-3 dígitos para las horas y dos para minutos y segundos",
	ErrorHHMMSSFields:                    "duración HH:MM:SS no válida: minutos y segundos deben estar entre 00 y 59",
	ErrorUnsupportedShellFormat:          "shell no compatible %q; elige %s",
}

// Text returns a localized message, falling back to English for an unresolved
// language or an accidentally missing translation.
func Text(language Language, message Message) string {
	if message >= messageCount {
		return ""
	}
	if language == Spanish && spanish[message] != "" {
		return spanish[message]
	}
	return english[message]
}

// Format formats a localized message with fmt.Sprintf semantics.
func Format(language Language, message Message, args ...any) string {
	return fmt.Sprintf(Text(language, message), args...)
}
