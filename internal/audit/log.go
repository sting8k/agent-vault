package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// now is the clock; tests replace it to let entries age.
var now = time.Now

const (
	// LogFile is the audit log inside the vault directory.
	LogFile  = "audit.log"
	lockFile = "audit.lock"
)

// Record is one audit entry, one JSON line in the log and the body of a json webhook.
// It has no field that could hold a value or an argument: Program is argv[0] only.
type Record struct {
	Time       time.Time `json:"time"` // when the action finished
	Action     string    `json:"action"`
	Secrets    []string  `json:"secrets"` // NAME.field for run, NAME for set and rm
	Program    string    `json:"program,omitempty"`
	Cwd        string    `json:"cwd,omitempty"`
	Exit       int       `json:"exit"`
	DurationMS int64     `json:"duration_ms"`
}

// JSON is the record on one line, without the trailing newline.
func (r Record) JSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(r) // a Record always encodes
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// Append adds rec to dir/audit.log (created 0600) and drops the entries older than retention.
//
// Writers hold an exclusive flock for the whole call, so pruning and appending never overlap:
// pruning replaces the file by rename, and an appender that had opened the old file would lose its
// line. When pruning fails the line is still appended; the error covers both steps.
func Append(dir string, retention time.Duration, rec Record) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	lockPath := filepath.Join(dir, lockFile)
	unlock, err := lock(lockPath)
	if err != nil {
		return fmt.Errorf("cannot lock %s: %w", lockPath, err)
	}
	defer unlock()

	path := filepath.Join(dir, LogFile)
	perr := prune(path, now().Add(-retention))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(append(rec.JSON(), '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		err = fmt.Errorf("cannot write %s: %w", path, err)
	}
	return errors.Join(perr, err)
}

// prune drops the entries older than cutoff. Lines are appended in time order, so the first line
// is the oldest: while it is still within the retention the log is left untouched and an append
// costs no rewrite. Once it has expired, every expired line goes, wherever it is in the file. A
// line it cannot read a time from is kept, because audit data is not destroyed on a guess. The
// kept lines go to a temp file that replaces the log by rename, so readers see the old file or the
// new one, never a half-written one.
func prune(path string, cutoff time.Time) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot prune %s: %w", path, err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	first, _ := r.ReadBytes('\n')
	if !expired(first, cutoff) {
		return nil
	}
	f.Seek(0, io.SeekStart)
	r.Reset(f)

	tmp, err := os.CreateTemp(filepath.Dir(path), LogFile+".tmp-*") // 0600
	if err != nil {
		return fmt.Errorf("cannot prune %s: %w", path, err)
	}
	w := bufio.NewWriter(tmp)
	for {
		line, rerr := r.ReadBytes('\n') // a last line without newline comes back with io.EOF
		if !expired(line, cutoff) {
			if _, err = w.Write(line); err != nil {
				break
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				err = rerr
			}
			break
		}
	}
	if err == nil {
		if err = w.Flush(); err == nil {
			err = tmp.Sync()
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("cannot prune %s: %w", path, err)
	}
	return nil
}

func expired(line []byte, cutoff time.Time) bool {
	var e struct {
		Time time.Time `json:"time"`
	}
	return json.Unmarshal(line, &e) == nil && !e.Time.IsZero() && e.Time.Before(cutoff)
}
