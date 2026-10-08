//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests run agv as a real process in its own process group, the way a
// harness would, and act on the group.

func TestKillingTheGroupLeavesNoChild(t *testing.T) {
	p := startAgv(t, append([]string{"run", "--"}, selfCmd("report-and-wait")...)...)
	child := pidOf(t, p.line(t)) // also shows that output is not held until the child exits
	if !alive(child) {
		t.Fatal("child is not running")
	}
	if err := syscall.Kill(-p.group(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	p.wait(t, 5*time.Second)
	waitGone(t, child, 5*time.Second)
}

func TestSingleSIGINTToTheGroupReachesTheChildOnce(t *testing.T) {
	p := startAgv(t, append([]string{"run", "--"}, selfCmd("count-sigint")...)...)
	if l := p.line(t); l != "ready" {
		t.Fatalf("got %q", l)
	}
	if err := syscall.Kill(-p.group(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if l := p.line(t); l != "sigint=1" {
		t.Fatalf("child saw %q, want exactly one SIGINT", l)
	}
	if ws := p.wait(t, 5*time.Second); !ws.Exited() || ws.ExitStatus() != 0 {
		t.Fatalf("agv did not outlive SIGINT and return the child's status: %v", ws)
	}
}

func TestTimeoutBoundsAChildThatIgnoresTermAndLeavesAGrandchild(t *testing.T) {
	start := time.Now()
	p := startAgv(t, append([]string{"run", "--timeout", "300ms", "--"}, selfCmd("stubborn-parent")...)...)
	grandchild := pidOf(t, p.line(t))
	if !alive(grandchild) {
		t.Fatal("grandchild is not running")
	}
	ws := p.wait(t, 15*time.Second) // the grandchild would keep the pipes open for a minute
	if !ws.Exited() || ws.ExitStatus() != 124 {
		t.Fatalf("agv status %v, want exit 124", ws)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
	if !strings.Contains(p.stderr.String(), "agv: ") {
		t.Fatalf("timeout not reported by agv itself: %q", p.stderr)
	}
}

func TestClosedOutputReaderStopsTheRunAndRemovesTempFiles(t *testing.T) {
	p := startAgv(t, append([]string{"run", "--"}, selfCmd("flood", "{{file:KEY.key}}")...)...)
	path := p.line(t)
	if got, err := os.ReadFile(path); err != nil || string(got) != keyValue {
		t.Fatalf("temp file during the run: %q, %v", got, err)
	}
	// What `| head -1` does once it has its line.
	p.pipe.Close()

	ws := p.wait(t, 15*time.Second)
	if ws.Signaled() {
		t.Fatalf("agv was killed by %v instead of finishing", ws.Signal())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("run directory still exists: %v", err)
	}
}
