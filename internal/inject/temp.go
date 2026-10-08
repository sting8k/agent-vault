package inject

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tempRoot returns the agv-owned directory path for run directories:
// $XDG_RUNTIME_DIR/agv if set, else $TMPDIR/agv-<uid> (or the OS temp dir).
func tempRoot(environ []string) (string, error) {
	get := func(key string) string {
		for i := len(environ) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(environ[i], key+"="); ok {
				return v
			}
		}
		return ""
	}
	root := ""
	if x := get("XDG_RUNTIME_DIR"); x != "" {
		root = filepath.Join(x, "agv")
	} else {
		tmp := get("TMPDIR")
		if tmp == "" {
			tmp = os.TempDir()
		}
		root = filepath.Join(tmp, "agv-"+strconv.Itoa(os.Getuid()))
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("temp directory %s is not an absolute path; set TMPDIR or XDG_RUNTIME_DIR", root)
	}
	return root, nil
}

// sweepStale removes run directories whose pid is no longer alive. It is
// best effort: a missing root or a failed removal is not an error.
func (b *builder) sweepStale() {
	root, err := tempRoot(b.environ)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() || pid <= 0 || strconv.Itoa(pid) != e.Name() || pid == self || pidAlive(pid) {
			continue
		}
		os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// file writes value to a 0600 file in the per-run directory, once per label.
func (b *builder) file(label string, value []byte) (string, error) {
	if p, ok := b.files[label]; ok {
		return p, nil
	}
	// A valid label is NAME.field with both parts in the name grammar, so it is a safe file name.
	name, field, _ := strings.Cut(label, ".")
	if !entryName.MatchString(name) || !fieldName.MatchString(field) {
		return "", fmt.Errorf("%s is not a valid secret label", label)
	}
	if b.dir == "" {
		if err := b.makeDir(); err != nil {
			return "", err
		}
	}
	path := filepath.Join(b.dir, label)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("write temp file for %s: %w", label, err)
	}
	b.files[label] = path // registered before writing so cleanup sees it
	if _, err := f.Write(value); err != nil {
		f.Close()
		return "", fmt.Errorf("write temp file for %s: %w", label, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write temp file for %s: %w", label, err)
	}
	return path, nil
}

// makeDir creates the temp root (if needed) and an empty per-run directory named after our pid.
func (b *builder) makeDir() error {
	root, err := tempRoot(b.environ)
	if err != nil {
		return err
	}
	if err := ensureRoot(root); err != nil {
		return fmt.Errorf("temp directory %s: %w", root, err)
	}
	dir := filepath.Join(root, strconv.Itoa(os.Getpid()))
	// Anything already here belongs to an earlier process that had our pid.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("temp directory %s: %w", dir, err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("temp directory %s: %w", dir, err)
	}
	b.dir = dir
	return nil
}

// cleanup removes the per-run directory and everything in it. It is safe to call more than once.
func (b *builder) cleanup() error {
	if b.dir == "" {
		return nil
	}
	err := os.RemoveAll(b.dir)
	b.dir = ""
	return err
}
