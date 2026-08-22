// Package version holds build metadata set with -ldflags at release time.
package version

import "fmt"

var (
	Version = "1.0.0"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns stable, human-readable version information.
func String() string {
	result := fmt.Sprintf("timer-cli %s", Version)
	metadata := ""
	if Commit != "unknown" {
		metadata = fmt.Sprintf("commit %s", Commit)
	}
	if Date != "unknown" {
		if metadata != "" {
			metadata += ", "
		}
		metadata += fmt.Sprintf("built %s", Date)
	}
	if metadata != "" {
		result += " (" + metadata + ")"
	}
	return result
}
