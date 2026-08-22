package cli

import (
	"errors"
	"testing"

	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
)

// valueForSurfaceOption returns a value that setOption/stripLanguageOptions
// accepts for the given long flag name, so the surface tests exercise real
// acceptance instead of tripping an unrelated validation error.
func valueForSurfaceOption(long string) string {
	switch long {
	case "bell-count":
		return "2"
	case "lang":
		return "es"
	default:
		return "x"
	}
}

// surfaceFlagArgs returns the argument(s) that turn on a single cliinfo
// option in its long form, including a value when the option takes one.
func surfaceFlagArgs(option cliinfo.Option) []string {
	if option.TakesValue {
		return []string{"--" + option.Long, valueForSurfaceOption(option.Long)}
	}
	return []string{"--" + option.Long}
}

// TestSurfaceOptionsAreAcceptedByParser proves every option cliinfo.Options()
// advertises (long form, "=" form, and short form) is recognized by
// parseOptions. Without this, an option added to the cliinfo table (driving
// help and all completion scripts) can ship documentation for a flag that
// parseOptions rejects with exit 2.
func TestSurfaceOptionsAreAcceptedByParser(t *testing.T) {
	t.Parallel()
	for _, option := range cliinfo.Options() {
		option := option
		t.Run(option.Long, func(t *testing.T) {
			t.Parallel()

			assertAccepted := func(t *testing.T, args []string) {
				t.Helper()
				opts, err := parseOptions(args)
				switch option.Long {
				case "help":
					if err != nil || opts.command != commandHelp {
						t.Fatalf("parseOptions(%v) = %+v, %v; want commandHelp, no error", args, opts, err)
					}
					return
				case "version":
					if err != nil || opts.command != commandVersion {
						t.Fatalf("parseOptions(%v) = %+v, %v; want commandVersion, no error", args, opts, err)
					}
					return
				}
				if err == nil {
					return
				}
				var optErr *optionError
				if errors.As(err, &optErr) && (optErr.code == optionErrorUnknownOption || optErr.code == optionErrorRequiresValue) {
					t.Fatalf("parseOptions(%v) = %v; cliinfo advertises --%s but parseOptions does not recognize it", args, err, option.Long)
				}
			}

			value := valueForSurfaceOption(option.Long)
			if option.TakesValue {
				assertAccepted(t, []string{"10m", "--" + option.Long, value})
				assertAccepted(t, []string{"10m", "--" + option.Long + "=" + value})
			} else {
				assertAccepted(t, []string{"10m", "--" + option.Long})
			}
			if option.Short != "" {
				assertAccepted(t, []string{"10m", "-" + option.Short})
			}
		})
	}
}

// TestSurfaceConflictsAreEnforced proves every conflict pair declared in
// cliinfo's option metadata is actually rejected by parseOptions, so the
// help text's stated exclusivity is not aspirational.
func TestSurfaceConflictsAreEnforced(t *testing.T) {
	t.Parallel()
	optionsByLong := make(map[string]cliinfo.Option)
	for _, option := range cliinfo.Options() {
		optionsByLong[option.Long] = option
	}
	for _, option := range cliinfo.Options() {
		option := option
		for _, conflictLong := range option.Conflicts {
			conflictLong := conflictLong
			t.Run(option.Long+"_vs_"+conflictLong, func(t *testing.T) {
				t.Parallel()
				args := []string{"10m"}
				args = append(args, surfaceFlagArgs(option)...)
				args = append(args, surfaceFlagArgs(optionsByLong[conflictLong])...)
				if _, err := parseOptions(args); err == nil {
					t.Fatalf("parseOptions(%v) = no error; cliinfo declares --%s conflicts with --%s", args, option.Long, conflictLong)
				}
			})
		}
	}
}
