// Package recordcadence defines the observable cadence of redirected timer
// records. Keeping the bucket and wake calculations together prevents the
// renderer and scheduler from drifting apart.
package recordcadence

import "time"

// Duration returns the redirected record cadence for an effective timer total.
func Duration(total time.Duration) time.Duration {
	switch {
	case total > 24*time.Hour:
		return 10 * time.Minute
	case total > time.Hour:
		return time.Minute
	default:
		return time.Second
	}
}

// Bucket returns the rounded-up record bucket for remaining time.
func Bucket(total, remaining time.Duration) int64 {
	if remaining <= 0 {
		return 0
	}
	cadence := Duration(total)
	return int64((remaining-1)/cadence) + 1
}

// NextBoundary returns the delay until the next observable record boundary,
// clamped to completion. A remaining value exactly on a boundary advances by
// one full cadence; a partial bucket advances only to its lower boundary.
func NextBoundary(total, remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return 0
	}
	cadence := Duration(total)
	delay := remaining % cadence
	if delay == 0 {
		delay = cadence
	}
	return min(delay, remaining)
}
