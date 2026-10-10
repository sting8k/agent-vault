//go:build unix

package audit

import (
	"os"
	"syscall"
)

// lock takes an exclusive flock on the file at path and returns the function that releases it.
// Each call opens its own file, so writers in one process exclude each other too. The vault keeps
// its own lock: pruning a large log must not make `agv set` wait.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR { // Go's own signals can interrupt a blocked flock
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
