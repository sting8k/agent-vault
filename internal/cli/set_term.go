package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"
)

// terminal is how set and rm talk to a person. Both calls print the prompt first.
// A real one comes from terminalOf; tests pass a fake as stdin that implements it.
type terminal interface {
	line(prompt string) (string, error)   // echoed answer, without the newline
	secret(prompt string) ([]byte, error) // hidden answer, never trimmed
}

// errCancelled means the person ended input (Ctrl-D) or declined.
var errCancelled = errors.New("cancelled; nothing was changed")

// terminalOf returns a terminal when stdin is one, else nil: no harness gives agv a TTY, and
// then nothing may prompt. Prompts are written to prompts (stderr).
func terminalOf(stdin io.Reader, prompts io.Writer) terminal {
	if t, ok := stdin.(terminal); ok {
		return t
	}
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return &ttyTerminal{f: f, out: prompts}
	}
	return nil
}

type ttyTerminal struct {
	f   *os.File
	out io.Writer
}

func (t *ttyTerminal) line(prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	// One byte at a time and no buffering: a buffered reader would swallow input that the
	// next hidden prompt (term.ReadPassword reads the descriptor directly) has to see.
	var b []byte
	one := make([]byte, 1)
	for {
		n, err := t.f.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				return strings.TrimSuffix(string(b), "\r"), nil
			}
			b = append(b, one[0])
		}
		if err != nil {
			if err == io.EOF && len(b) > 0 {
				return string(b), nil
			}
			return "", err
		}
	}
}

func (t *ttyTerminal) secret(prompt string) ([]byte, error) {
	fmt.Fprint(t.out, prompt)
	fd := int(t.f.Fd())
	// ReadPassword turns echo off. Without this guard, Ctrl-C would end agv first and leave the
	// terminal silent for shells that do not reset it.
	if saved, err := term.GetState(fd); err == nil {
		sig := make(chan os.Signal, 1)
		done := make(chan struct{})
		signal.Notify(sig, os.Interrupt)
		defer func() { signal.Stop(sig); close(done) }()
		go func() {
			select {
			case <-sig:
				term.Restore(fd, saved)
				fmt.Fprintf(t.out, "\nagv: %v\n", errCancelled)
				os.Exit(130)
			case <-done:
			}
		}()
	}
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(t.out) // the person's Enter was not echoed
	return b, err
}
