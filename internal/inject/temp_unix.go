//go:build unix

package inject

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// pidAlive reports whether a process with this pid exists. EPERM means it
// exists but belongs to someone else.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ensureRoot makes sure root is a 0700 directory owned by the current user.
// A path owned by someone else, or a symlink, is refused: anyone could have
// planted it in a shared temp directory.
func ensureRoot(root string) error {
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("exists and is not a directory")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("owned by uid %d, not by you", st.Uid)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(root, 0o700)
	}
	return nil
}
