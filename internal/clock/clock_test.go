package clock

import (
	"testing"
	"time"
)

func TestSystemNowUsesCurrentWallClock(t *testing.T) {
	t.Parallel()
	before := time.Now()
	got := (System{}).Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("System.Now() = %v, want between %v and %v", got, before, after)
	}
}
