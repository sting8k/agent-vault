//go:build unix

package runner

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sting8k/agent-vault/internal/inject"
)

// The test binary doubles as the child process: with AGV_RUNNER_HELPER set it
// behaves as the named program instead of running tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("AGV_RUNNER_HELPER"); mode != "" {
		helper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(mode string) {
	switch mode {
	case "print": // prints the secret on both streams, reports whether agv's own env leaked
		fmt.Printf("out:%s leak=%v\n", os.Getenv("SECRET"), os.Getenv("AGV_LEAK") != "")
		fmt.Fprintf(os.Stderr, "err:%s\n", os.Getenv("SECRET"))
		os.Exit(7)
	case "kill-self":
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
	case "quiet-exit":
		fmt.Println("done")
	case "term-handler": // exits cleanly when asked to stop; the signal name tells which one arrived
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)
		fmt.Println("ready")
		s := <-ch
		fmt.Println("got", s)
		os.Exit(42)
	case "sleep":
		time.Sleep(time.Minute)
	}
}

type recorder struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.b.Write(p)
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.b.String()
}

// plan returns a Plan that runs this test binary as the helper program.
func plan(mode string, secrets map[string]string, cleaned *int) *inject.Plan {
	p := &inject.Plan{
		Argv: []string{os.Args[0]},
		Env:  []string{"AGV_RUNNER_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"}, // the race runtime otherwise delays a child's exit by 1 s
		Cleanup: func() error {
			if cleaned != nil {
				*cleaned++
			}
			return nil
		},
	}
	for label, v := range secrets {
		p.Secrets = append(p.Secrets, inject.Secret{Label: label, Value: []byte(v)})
		p.Env = append(p.Env, "SECRET="+v)
	}
	return p
}

func TestRunRedactsEachStreamAndKeepsChildCodeAndEnv(t *testing.T) {
	t.Setenv("AGV_LEAK", "1")
	var out, errb recorder
	code, err := Run(plan("print", map[string]string{"TOK.value": "hunter2-hunter2"}, nil), Options{Stdout: &out, Stderr: &errb})
	if err != nil || code != 7 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if out.String() != "out:[REDACTED:TOK.value] leak=false\n" || errb.String() != "err:[REDACTED:TOK.value]\n" {
		t.Fatalf("stdout %q stderr %q", out.String(), errb.String())
	}
}

// An empty Plan.Env means an empty environment, not "inherit agv's".
func TestEmptyPlanEnvIsNotInherited(t *testing.T) {
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env command:", err)
	}
	t.Setenv("AGV_LEAK", "inherited-from-agv")
	var out recorder
	code, err := Run(&inject.Plan{Argv: []string{envBin}}, Options{Stdout: &out, Stderr: &recorder{}})
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if strings.Contains(out.String(), "AGV_LEAK") {
		t.Fatalf("child inherited agv's environment: %q", out.String())
	}
}

// Finished is where agv writes the audit line. It runs after the temp files are gone, sees the
// code Run returns, and runs while agv still catches signals. If Run had released them first, the
// SIGTERM below would kill the test binary.
func TestFinishedRunsAfterCleanupAndShieldedFromSignals(t *testing.T) {
	cleaned, cleanedAtFinish, finishedCode := 0, -1, -1
	code, err := Run(plan("print", nil, &cleaned), Options{Stdout: &recorder{}, Stderr: &recorder{}, Finished: func(code int) {
		cleanedAtFinish, finishedCode = cleaned, code
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond) // time for the signal to arrive
	}})
	if err != nil || code != 7 || finishedCode != 7 || cleanedAtFinish != 1 {
		t.Fatalf("code=%d err=%v; Finished saw code %d after %d cleanups", code, err, finishedCode, cleanedAtFinish)
	}
}

func TestExitCodes(t *testing.T) {
	missing := &inject.Plan{Argv: []string{"agv-no-such-command-xyz"}}
	notExec := t.TempDir() + "/script"
	os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644)
	cases := []struct {
		name string
		plan *inject.Plan
		want int
	}{
		{"not found", missing, ExitNotFound},
		{"not executable", &inject.Plan{Argv: []string{notExec}}, ExitNotExecutable},
		{"killed by signal 9", plan("kill-self", nil, nil), 128 + 9},
	}
	for _, c := range cases {
		code, err := Run(c.plan, Options{Stdout: &recorder{}, Stderr: &recorder{}})
		if code != c.want {
			t.Errorf("%s: code %d, want %d (err %v)", c.name, code, c.want, err)
		}
		if c.want != 128+9 && err == nil {
			t.Errorf("%s: no message for the user", c.name)
		}
	}
}

func TestShellIsRejectedUnlessAllowed(t *testing.T) {
	cleaned := 0
	mk := func() *inject.Plan {
		return &inject.Plan{Argv: []string{"/bin/sh", "-c", "exit 3"}, Cleanup: func() error { cleaned++; return nil }}
	}
	code, err := Run(mk(), Options{Stdout: &recorder{}, Stderr: &recorder{}})
	if code != ExitAgv || err == nil {
		t.Fatalf("shell ran: code=%d err=%v", code, err)
	}
	code, err = Run(mk(), Options{Stdout: &recorder{}, Stderr: &recorder{}, AllowShell: true})
	if code != 3 || err != nil {
		t.Fatalf("--allow-shell: code=%d err=%v", code, err)
	}
	if cleaned != 2 {
		t.Fatalf("cleanup ran %d times for 2 runs", cleaned)
	}
}

func TestCleanupRunsWhenChildCannotStart(t *testing.T) {
	cleaned := 0
	p := &inject.Plan{Argv: []string{"agv-no-such-command-xyz"}, Cleanup: func() error { cleaned++; return nil }}
	Run(p, Options{Stdout: &recorder{}, Stderr: &recorder{}})
	if cleaned != 1 {
		t.Fatalf("cleanup ran %d times", cleaned)
	}
}

func TestTimeoutAsksBeforeItKills(t *testing.T) {
	var out recorder
	start := time.Now()
	code, err := Run(plan("term-handler", nil, nil), Options{Stdout: &out, Stderr: &recorder{}, Timeout: 300 * time.Millisecond})
	if code != ExitTimeout || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), "got terminated") {
		t.Fatalf("child was not given a chance to stop: %q", out.String())
	}
	if d := time.Since(start); d > killGrace {
		t.Fatalf("took %v: waited for the grace period although the child had stopped", d)
	}
}

func TestSIGTERMAndSIGHUPReachTheChild(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		out := &recorder{}
		go func() {
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(out.String(), "ready") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			// Run holds the subscription, so this signal does not kill the test process.
			syscall.Kill(os.Getpid(), sig)
		}()
		code, err := Run(plan("term-handler", nil, nil), Options{Stdout: out, Stderr: &recorder{}})
		if code != 42 || err != nil || !strings.Contains(out.String(), "got "+fmt.Sprint(sig)) {
			t.Fatalf("%v: code=%d err=%v out=%q", sig, code, err, out.String())
		}
	}
}

// A write error other than a closed pipe must not turn into a silent success.
type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestLostOutputIsNotReportedAsSuccess(t *testing.T) {
	code, err := Run(plan("quiet-exit", nil, nil), Options{Stdout: failingWriter{errors.New("disk full")}, Stderr: &recorder{}})
	if code != ExitAgv || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	// A reader that went away is the caller's choice, like `| head`: not an error.
	code, err = Run(plan("quiet-exit", nil, nil), Options{Stdout: failingWriter{syscall.EPIPE}, Stderr: &recorder{}})
	if code != 0 || err != nil {
		t.Fatalf("EPIPE: code=%d err=%v", code, err)
	}
}
