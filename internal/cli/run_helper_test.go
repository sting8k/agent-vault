//go:build unix

package cli

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sting8k/agent-vault/internal/inject"
)

const (
	tokValue = "tok-s3cret-value-1234"
	keyValue = "-----BEGIN KEY-----\nabcdefgh12345678\n-----END KEY-----\n"
)

type fakeVault map[string]map[string]inject.Field

func (v fakeVault) Entry(name string) (map[string]inject.Field, error) {
	if e, ok := v[name]; ok {
		return e, nil
	}
	return nil, errors.New("no such secret " + name)
}

var testVault = fakeVault{
	"TOK": {"value": {Value: []byte(tokValue)}},
	"KEY": {"key": {Value: []byte(keyValue), Env: "KEY_FILE", File: true}},
}

func useTestVault(t *testing.T) {
	old := openSource
	openSource = func(IO) (inject.Source, error) { return testVault, nil }
	t.Cleanup(func() { openSource = old })
}

// The test binary doubles as agv and as the child programs agv runs: a
// process started with AGV_RUNTEST_HELPER=1 and "-- ROLE ARGS..." behaves as
// ROLE instead of running tests.
func TestRunHelperProcess(t *testing.T) {
	if os.Getenv("AGV_RUNTEST_HELPER") != "1" {
		return
	}
	i := slices.Index(os.Args, "--")
	role, args := os.Args[i+1], os.Args[i+2:]
	switch role {
	case "agv": // the real command, on the test vault
		useTestVault(t)
		os.Exit(Main(args, os.Stdin, os.Stdout, os.Stderr, os.Environ()))
	case "args": // one line per argument
		for _, a := range args {
			fmt.Println(a)
		}
	case "stdin": // copies its stdin to stdout
		io.Copy(os.Stdout, os.Stdin)
	case "leak": // prints the secret as the child got it, then transformed
		v := os.Getenv("TOKEN")
		fmt.Println("raw:" + v)
		fmt.Println("b64:" + b64(v))
		fmt.Fprintln(os.Stderr, "err:"+v)
		os.Exit(3)
	case "report-and-wait": // prints its pid, then waits
		fmt.Println(os.Getpid())
		time.Sleep(time.Minute)
	case "count-sigint": // counts SIGINTs for a while
		ch := make(chan os.Signal, 8)
		signal.Notify(ch, syscall.SIGINT)
		fmt.Println("ready")
		time.Sleep(700 * time.Millisecond)
		fmt.Printf("sigint=%d\n", len(ch))
	case "stubborn-parent": // ignores SIGTERM and leaves a grandchild that keeps our stdout open
		signal.Ignore(syscall.SIGTERM)
		gc := exec.Command(os.Args[0], "-test.run=^TestRunHelperProcess$", "--", "sleep")
		gc.Stdout = os.Stdout
		if err := gc.Start(); err != nil {
			os.Exit(99)
		}
		fmt.Println("grandchild", gc.Process.Pid)
		time.Sleep(time.Minute)
	case "sleep":
		time.Sleep(time.Minute)
	case "flood": // prints its first argument, then output without end
		fmt.Println(args[0])
		for i := 0; ; i++ {
			fmt.Println("line", i)
		}
	}
	os.Exit(0)
}

func selfCmd(role string, args ...string) []string {
	return append([]string{os.Args[0], "-test.run=^TestRunHelperProcess$", "--", role}, args...)
}

func helperEnv(t *testing.T) []string {
	// Every run writes audit.log into AGV_HOME: never the real vault directory.
	return append(os.Environ(), "AGV_RUNTEST_HELPER=1", "XDG_RUNTIME_DIR="+t.TempDir(), "AGV_HOME="+t.TempDir(),
		"GORACE=atexit_sleep_ms=0") // the race runtime otherwise delays every exit by 1 s
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// syncBuf collects a process's stderr while the test reads it.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// agvProcess starts the helper as a real agv process in its own process group
// and kills the whole group when the test ends.
type agvProcess struct {
	cmd    *exec.Cmd
	pipe   io.ReadCloser // read end of agv's stdout
	stdout *bufio.Reader
	stderr *syncBuf
	home   string // its AGV_HOME
}

func startAgv(t *testing.T, args ...string) *agvProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestRunHelperProcess$", "--", "agv"}, args...)...)
	cmd.Env = helperEnv(t)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &agvProcess{cmd: cmd, pipe: out, stdout: bufio.NewReader(out), stderr: &syncBuf{}, home: envOf(cmd.Env, "AGV_HOME")}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() })
	return p
}

func (p *agvProcess) group() int { return p.cmd.Process.Pid } // Setpgid makes agv the group leader

// line reads one line of agv's stdout, failing the test if none arrives.
func (p *agvProcess) line(t *testing.T) string {
	t.Helper()
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() { s, err := p.stdout.ReadString('\n'); ch <- res{strings.TrimSuffix(s, "\n"), err} }()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading agv output: %v (stderr: %s)", r.err, p.stderr)
		}
		return r.s
	case <-time.After(15 * time.Second):
		t.Fatalf("no output from agv within 15s (stderr: %s)", p.stderr)
	}
	return ""
}

// wait returns agv's wait status, failing the test if agv is still running after d.
func (p *agvProcess) wait(t *testing.T, d time.Duration) syscall.WaitStatus {
	t.Helper()
	done := make(chan struct{})
	go func() { p.cmd.Wait(); close(done) }()
	select {
	case <-done:
		return p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	case <-time.After(d):
		t.Fatalf("agv still running after %v", d)
	}
	return 0
}

// pidOf parses a pid from a line such as "grandchild 123" or "123".
func pidOf(t *testing.T, s string) int {
	t.Helper()
	f := strings.Fields(s)
	pid, err := strconv.Atoi(f[len(f)-1])
	if err != nil {
		t.Fatalf("not a pid: %q", s)
	}
	return pid
}

// alive reports whether pid is a running process; a zombie that nobody has
// reaped yet counts as gone.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if st, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndexByte(string(st), ')'); i >= 0 && strings.HasPrefix(strings.TrimSpace(string(st[i+1:])), "Z") {
			return false
		}
	}
	return true
}

func waitGone(t *testing.T, pid int, d time.Duration) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !alive(pid) {
			return
		}
	}
	t.Fatalf("process %d is still running", pid)
}
