//go:build unix

package runner

import (
	"os"
	"os/signal"
	"syscall"
)

// notify subscribes sigs to the signals agv handles itself. A signal that was
// ignored when agv started stays ignored, so the child inherits that too.
//
//   - SIGTERM, SIGHUP: forwarded to the child.
//   - SIGINT: caught so agv outlives it and cleans up, but not forwarded; a
//     terminal Ctrl-C already reaches the whole foreground group.
//   - SIGPIPE: caught so a write to a closed stdout or stderr returns EPIPE
//     instead of killing agv (Go kills the process on EPIPE for fds 1 and 2
//     unless SIGPIPE is handled). Handled, not ignored, so the child still gets
//     the default action: exec resets caught signals but keeps ignored ones.
func notify(sigs chan<- os.Signal) {
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE} {
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}
}

func forwarded(s os.Signal) bool { return s == syscall.SIGTERM || s == syscall.SIGHUP }

// exitCode is the child's exit status, or 128+N if signal N killed it.
func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return ExitAgv
	}
	ws := ps.Sys().(syscall.WaitStatus)
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}
