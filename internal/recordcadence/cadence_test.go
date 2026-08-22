package recordcadence

import (
	"testing"
	"time"
)

func TestDurationExactThresholds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		total time.Duration
		want  time.Duration
	}{
		{name: "59 minutes", total: 59 * time.Minute, want: time.Second},
		{name: "one hour", total: time.Hour, want: time.Second},
		{name: "over one hour", total: time.Hour + time.Nanosecond, want: time.Minute},
		{name: "24 hours", total: 24 * time.Hour, want: time.Minute},
		{name: "over 24 hours", total: 24*time.Hour + time.Nanosecond, want: 10 * time.Minute},
		{name: "30 days", total: 30 * 24 * time.Hour, want: 10 * time.Minute},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Duration(test.total); got != test.want {
				t.Fatalf("Duration(%v) = %v, want %v", test.total, got, test.want)
			}
		})
	}
}

func TestBucketAndNextBoundaryAgree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		total     time.Duration
		remaining time.Duration
		wantDelay time.Duration
	}{
		{name: "second boundary", total: 59 * time.Minute, remaining: 42 * time.Second, wantDelay: time.Second},
		{name: "fractional second", total: time.Hour, remaining: 42*time.Second + time.Nanosecond, wantDelay: time.Nanosecond},
		{name: "minute boundary", total: 2 * time.Hour, remaining: 90 * time.Minute, wantDelay: time.Minute},
		{name: "just before minute boundary", total: 24 * time.Hour, remaining: 90*time.Minute + time.Nanosecond, wantDelay: time.Nanosecond},
		{name: "just after minute boundary", total: 24 * time.Hour, remaining: 90*time.Minute - time.Nanosecond, wantDelay: time.Minute - time.Nanosecond},
		{name: "ten-minute boundary", total: 30 * 24 * time.Hour, remaining: 29 * 24 * time.Hour, wantDelay: 10 * time.Minute},
		{name: "fractional ten-minute bucket", total: 30 * 24 * time.Hour, remaining: 29*24*time.Hour + time.Minute, wantDelay: time.Minute},
		{name: "completion before cadence", total: 30 * 24 * time.Hour, remaining: time.Nanosecond, wantDelay: time.Nanosecond},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			before := Bucket(test.total, test.remaining)
			if got := NextBoundary(test.total, test.remaining); got != test.wantDelay {
				t.Fatalf("NextBoundary(%v, %v) = %v, want %v", test.total, test.remaining, got, test.wantDelay)
			}
			after := Bucket(test.total, test.remaining-test.wantDelay)
			if after >= before {
				t.Fatalf("bucket did not advance: before=%d after=%d", before, after)
			}
		})
	}
}

func TestNextBoundaryAtCompletion(t *testing.T) {
	t.Parallel()
	for _, remaining := range []time.Duration{0, -time.Nanosecond} {
		if got := NextBoundary(30*24*time.Hour, remaining); got != 0 {
			t.Fatalf("NextBoundary(_, %v) = %v, want 0", remaining, got)
		}
	}
}

func TestAdaptiveWakeCounts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		total time.Duration
		want  int
	}{
		{name: "59 minutes", total: 59 * time.Minute, want: 3540},
		{name: "one hour", total: time.Hour, want: 3600},
		{name: "24 hours", total: 24 * time.Hour, want: 1440},
		{name: "30 days", total: 30 * 24 * time.Hour, want: 4320},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			remaining := test.total
			wakes := 0
			for remaining > 0 {
				delay := NextBoundary(test.total, remaining)
				if delay <= 0 || delay > remaining {
					t.Fatalf("invalid delay %v at remaining %v", delay, remaining)
				}
				remaining -= delay
				wakes++
			}
			if wakes != test.want {
				t.Fatalf("wakes = %d, want %d", wakes, test.want)
			}
		})
	}
}
