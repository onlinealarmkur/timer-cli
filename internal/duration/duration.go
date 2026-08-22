// Package duration parses the deliberately small timer duration grammar.
package duration

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/onlinealarmkur/timer-cli/internal/timerlimit"
)

// ErrorCode identifies a stable duration validation failure so the CLI can
// localize it without matching error text.
type ErrorCode uint8

const (
	ErrorRequired ErrorCode = iota + 1
	ErrorNegative
	ErrorZero
	ErrorAmbiguousColon
	ErrorBelowMinimum
	ErrorAboveMaximum
	ErrorInvalidUnits
	ErrorTooLarge
	ErrorInvalidMMSS
	ErrorMMSSSeconds
	ErrorInvalidHHMMSS
	ErrorHHMMSSFields
	// ErrorCodeCount is a sentinel for exhaustiveness tests; not a valid code.
	ErrorCodeCount
)

// ParseError is returned for invalid user-supplied durations. Its wording is
// intentionally generic: every user-facing path resolves the real message
// through cli.localizedDurationError -> localize, keyed on Code, so this
// string is unreachable for users and exists only as a non-localized
// fallback (e.g. for callers that print err.Error() directly).
type ParseError struct{ Code ErrorCode }

func (e *ParseError) Error() string { return "invalid duration" }

func parseError(code ErrorCode) error { return &ParseError{Code: code} }

// ErrorCodeOf extracts a duration validation code.
func ErrorCodeOf(err error) (ErrorCode, bool) {
	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		return 0, false
	}
	return parseErr.Code, true
}

var (
	unitsPattern  = regexp.MustCompile(`^(?:([0-9]+)h)?(?:[[:space:]]*([0-9]+)m)?(?:[[:space:]]*([0-9]+)s)?$`)
	mmssPattern   = regexp.MustCompile(`^([0-9]{2}):([0-9]{2})$`)
	hhmmssPattern = regexp.MustCompile(`^([0-9]{2,3}):([0-9]{2}):([0-9]{2})$`)
	// Keep aliases to simple unit forms that normalize without locale detection,
	// grammatical number rules, or a translated-number parser.
	wordHoursPattern       = regexp.MustCompile(`(?i)([0-9]+)[[:space:]]*(?:hours?|horas?|heures?|stunden?|ora|ore|uur|uren)\b`)
	wordMinutesPattern     = regexp.MustCompile(`(?i)([0-9]+)[[:space:]]*(?:minutos?|minuten?|minutes?|minuut|minut[oi])\b`)
	wordSecondsPattern     = regexp.MustCompile(`(?i)([0-9]+)[[:space:]]*(?:seconds?|segundos?|secondes?|seconden?|sekunden?|second[oi])\b`)
	wordConjunctionPattern = regexp.MustCompile(`(?i)[[:space:]]+(?:and|y|et|und|e|en)[[:space:]]+`)
)

// Parse accepts ordered whole-number h/m/s units; equivalent English, Spanish,
// Portuguese, French, German, Italian, and Dutch unit words; MM:SS; or
// HH:MM:SS.
func Parse(input string) (time.Duration, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return 0, parseError(ErrorRequired)
	}
	if strings.HasPrefix(s, "-") {
		return 0, parseError(ErrorNegative)
	}
	if s == "0" {
		return 0, parseError(ErrorZero)
	}
	s = normalizeWhitespace(s)
	s = normalizeUnitWords(s)

	var d time.Duration
	var err error
	switch strings.Count(s, ":") {
	case 0:
		d, err = parseUnits(s)
	case 1:
		d, err = parseMMSS(s)
	case 2:
		d, err = parseHHMMSS(s)
	default:
		err = parseError(ErrorAmbiguousColon)
	}
	if err != nil {
		if parsed, parseErr := time.ParseDuration(strings.ReplaceAll(s, " ", "")); parseErr == nil && parsed > 0 && parsed < time.Second {
			return 0, parseError(ErrorBelowMinimum)
		}
		return 0, err
	}
	if d == 0 {
		return 0, parseError(ErrorZero)
	}
	if d < time.Second {
		return 0, parseError(ErrorBelowMinimum)
	}
	if d > timerlimit.MaxDuration {
		return 0, parseError(ErrorAboveMaximum)
	}
	return d, nil
}

func normalizeWhitespace(s string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsSpace(char) {
			return ' '
		}
		return char
	}, s)
}

func parseUnits(s string) (time.Duration, error) {
	matches := unitsPattern.FindStringSubmatch(s)
	if matches == nil || (matches[1] == "" && matches[2] == "" && matches[3] == "") {
		return 0, parseError(ErrorInvalidUnits)
	}
	units := []time.Duration{time.Hour, time.Minute, time.Second}
	var total time.Duration
	for i, raw := range matches[1:] {
		if raw == "" {
			continue
		}
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value > uint64(timerlimit.MaxDuration/units[i])+1 {
			return 0, parseError(ErrorTooLarge)
		}
		part := time.Duration(value) * units[i]
		if part > timerlimit.MaxDuration || total > timerlimit.MaxDuration-part {
			return 0, parseError(ErrorAboveMaximum)
		}
		total += part
	}
	return total, nil
}

func normalizeUnitWords(s string) string {
	s = wordHoursPattern.ReplaceAllString(s, "${1}h")
	s = wordMinutesPattern.ReplaceAllString(s, "${1}m")
	s = wordSecondsPattern.ReplaceAllString(s, "${1}s")
	return wordConjunctionPattern.ReplaceAllString(s, " ")
}

func parseMMSS(s string) (time.Duration, error) {
	m := mmssPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, parseError(ErrorInvalidMMSS)
	}
	minutes, _ := strconv.Atoi(m[1])
	seconds, _ := strconv.Atoi(m[2])
	if seconds > 59 {
		return 0, parseError(ErrorMMSSSeconds)
	}
	return time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
}

func parseHHMMSS(s string) (time.Duration, error) {
	m := hhmmssPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, parseError(ErrorInvalidHHMMSS)
	}
	hours, _ := strconv.Atoi(m[1])
	minutes, _ := strconv.Atoi(m[2])
	seconds, _ := strconv.Atoi(m[3])
	if minutes > 59 || seconds > 59 {
		return 0, parseError(ErrorHHMMSSFields)
	}
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
}
