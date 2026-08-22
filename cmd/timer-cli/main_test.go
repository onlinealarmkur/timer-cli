//go:build darwin || linux

package main

import (
	"errors"
	"syscall"
	"testing"
)

func TestForegroundStopSignalForSessions(t *testing.T) {
	t.Parallel()
	lookupErr := errors.New("session lookup failed")
	tests := []struct {
		name               string
		self, parent       int
		selfErr, parentErr error
		want               syscall.Signal
	}{
		{name: "shell job in same session", self: 42, parent: 42, want: syscall.SIGTSTP},
		{name: "orphaned process group", self: 42, parent: 7, want: syscall.SIGSTOP},
		{name: "self lookup failure keeps standard semantics", selfErr: lookupErr, want: syscall.SIGTSTP},
		{name: "parent lookup failure keeps standard semantics", parentErr: lookupErr, want: syscall.SIGTSTP},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := foregroundStopSignalForSessions(test.self, test.parent, test.selfErr, test.parentErr); got != test.want {
				t.Fatalf("foregroundStopSignalForSessions() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestForegroundStopSignalUsesSupportedStopSemantics(t *testing.T) {
	got := foregroundStopSignal()
	if got != syscall.SIGTSTP && got != syscall.SIGSTOP {
		t.Fatalf("foregroundStopSignal() = %v, want SIGTSTP or SIGSTOP", got)
	}
}
