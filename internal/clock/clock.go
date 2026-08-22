// Package clock provides injectable time for deterministic countdown tests.
package clock

import "time"

// Clock supplies the current wall-clock time.
type Clock interface {
	Now() time.Time
}

// System is the production clock.
type System struct{}

// Now returns the current time.
func (System) Now() time.Time { return time.Now() }
