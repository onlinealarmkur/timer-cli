// Command timer-cli runs a foreground terminal countdown timer.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/onlinealarmkur/timer-cli/internal/cli"
	"golang.org/x/sys/unix"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	suspendSignals := make(chan os.Signal, 1)
	signal.Notify(suspendSignals, syscall.SIGTSTP)

	code := cli.RunWithSuspension(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, suspendSignals, func() error {
		signal.Stop(suspendSignals)
		signal.Reset(syscall.SIGTSTP)
		err := syscall.Kill(os.Getpid(), foregroundStopSignal())
		signal.Notify(suspendSignals, syscall.SIGTSTP)
		return err
	})
	signal.Stop(suspendSignals)
	stop()
	os.Exit(code)
}

func foregroundStopSignal() syscall.Signal {
	selfSession, selfErr := unix.Getsid(0)
	parentSession, parentErr := unix.Getsid(os.Getppid())
	return foregroundStopSignalForSessions(selfSession, parentSession, selfErr, parentErr)
}

func foregroundStopSignalForSessions(selfSession, parentSession int, selfErr, parentErr error) syscall.Signal {
	if selfErr == nil && parentErr == nil && selfSession != parentSession {
		// POSIX discards terminal stop signals for orphaned process groups.
		// SIGSTOP preserves suspension for PTY, daemon, and direct launches.
		return syscall.SIGSTOP
	}
	return syscall.SIGTSTP
}
