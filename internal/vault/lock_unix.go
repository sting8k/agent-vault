//go:build unix

package vault

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockDir takes an exclusive flock on dir/.lock and returns the function that releases it.
// Each call opens its own file, so writers in one process exclude each other too.
func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
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
