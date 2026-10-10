// Package runner starts the child, redacts its output, and owns signals,
// timeout, exit codes and temp-file cleanup.
package runner

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sting8k/agent-vault/internal/inject"
	"github.com/sting8k/agent-vault/internal/redact"
)

// Exit codes chosen by agv. Anything else is the child's own (or 128+N when
// a signal killed it).
const (
	ExitTimeout       = 124
	ExitAgv           = 125
	ExitNotExecutable = 126
	ExitNotFound      = 127
)

const (
	// killGrace is how long a timed-out child gets between SIGTERM and SIGKILL.
	killGrace = 2 * time.Second
	// waitDelay bounds how long Wait waits for output after the child is gone,
	// so a grandchild that keeps the pipes open cannot hang agv.
	waitDelay = 2 * time.Second
)

// Options are the parts of a run that do not come from the Plan.
type Options struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Timeout        time.Duration // 0: no timeout
	AllowShell     bool
	// Finished, if set, is called with the exit code Run is about to return, on every path after
	// Run is entered. The child is gone and the temp files are removed, but agv still holds its
	// signal handlers, so SIGINT/SIGTERM/SIGHUP cannot kill agv while it runs. Keep it short.
	Finished func(code int)
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true, "mksh": true, "tcsh": true, "csh": true}

// Run runs plan.Argv with plan.Env, redacting the child's stdout and stderr,
// and always calls plan.Cleanup before returning. It returns the exit code
// agv should use. A non-nil error is a message for the user (cli adds the
// "agv: " prefix); it is built without secret values and passed through the
// redactor.
func Run(plan *inject.Plan, opts Options) (code int, err error) {
	// Subscribe first and unsubscribe last (defers run in reverse order), so a
	// signal can never kill agv between the child's exit and temp-file cleanup.
	sigs := make(chan os.Signal, 16)
	notify(sigs)
	defer signal.Stop(sigs)
	defer func() {
		if opts.Finished != nil {
			opts.Finished(code)
		}
	}()
	defer func() {
		if plan.Cleanup == nil {
			return
		}
		if cerr := plan.Cleanup(); cerr != nil && err == nil {
			err = fmt.Errorf("could not remove temp files: %w", cerr)
		}
	}()

	if !opts.AllowShell && shells[filepath.Base(plan.Argv[0])] {
		return ExitAgv, fmt.Errorf("%s is a shell; a shell can route secrets around redaction (pass --allow-shell to run it anyway)", plan.Argv[0])
	}
	secrets := make([]redact.Secret, len(plan.Secrets))
	for i, s := range plan.Secrets {
		secrets[i] = redact.Secret(s)
	}
	red, err := redact.New(secrets)
	if err != nil {
		return ExitAgv, err
	}

	cmd := exec.Command(plan.Argv[0], plan.Argv[1:]...)
	cmd.Env = append([]string{}, plan.Env...) // never nil: nil would inherit agv's environment
	cmd.Stdin = opts.Stdin
	stdout, stderr := red.NewWriter(opts.Stdout), red.NewWriter(opts.Stderr)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		return startCode(err), fmt.Errorf("cannot run %s: %s", plan.Argv[0], red.String(startReason(err)))
	}

	done := make(chan struct{})
	go func() {
		cmd.Wait() // outcome is read from ProcessState; copy errors are handled below
		close(done)
	}()
	timedOut := supervise(cmd, sigs, done, opts.Timeout)

	code = exitCode(cmd.ProcessState)
	var werr error
	for _, w := range []*redact.Writer{stdout, stderr} {
		if e := w.Close(); e != nil && !errors.Is(e, syscall.EPIPE) {
			werr = e
		}
	}
	switch {
	case timedOut:
		return ExitTimeout, fmt.Errorf("command timed out after %s", opts.Timeout)
	case werr != nil:
		if code == 0 {
			code = ExitAgv
		}
		return code, fmt.Errorf("writing output: %s", red.String(werr.Error()))
	}
	return code, nil
}

// supervise waits for done, forwarding signals and enforcing the timeout. It
// reports whether the timeout expired.
func supervise(cmd *exec.Cmd, sigs <-chan os.Signal, done <-chan struct{}, timeout time.Duration) (timedOut bool) {
	var expire, grace <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expire = t.C
	}
	for {
		select {
		case <-done:
			return timedOut
		case s := <-sigs:
			if forwarded(s) {
				cmd.Process.Signal(s)
			}
		case <-expire:
			timedOut, expire = true, nil
			cmd.Process.Signal(syscall.SIGTERM)
			g := time.NewTimer(killGrace)
			defer g.Stop()
			grace = g.C
		case <-grace:
			grace = nil
			cmd.Process.Kill()
		}
	}
}

// startCode maps a failure to start the child to an exit code.
func startCode(err error) int {
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, exec.ErrDot), errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return ExitNotFound
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.ENOEXEC), errors.Is(err, syscall.E2BIG):
		return ExitNotExecutable
	}
	return ExitAgv
}

// startReason is the OS part of a start error, without the command line.
func startReason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var ee *exec.Error
	if errors.As(err, &ee) {
		return ee.Err.Error()
	}
	return err.Error()
}
