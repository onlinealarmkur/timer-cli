package version

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name   string
		commit string
		date   string
		want   string
	}{
		{name: "no build metadata", commit: "unknown", date: "unknown", want: "timer-cli 1.0.0"},
		{name: "commit only", commit: "abc1234", date: "unknown", want: "timer-cli 1.0.0 (commit abc1234)"},
		{name: "date only", commit: "unknown", date: "2026-07-16T12:00:00Z", want: "timer-cli 1.0.0 (built 2026-07-16T12:00:00Z)"},
		{name: "full build metadata", commit: "abc1234", date: "2026-07-16T12:00:00Z", want: "timer-cli 1.0.0 (commit abc1234, built 2026-07-16T12:00:00Z)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVersion, oldCommit, oldDate := Version, Commit, Date
			Version, Commit, Date = "1.0.0", tt.commit, tt.date
			t.Cleanup(func() {
				Version, Commit, Date = oldVersion, oldCommit, oldDate
			})

			if got := String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
