package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const day = 24 * time.Hour

func line(program string, age time.Duration) string {
	return string(Record{Time: time.Now().Add(-age), Action: ActionRun, Program: program}.JSON()) + "\n"
}

// programs lists the Program of every line of the log; a line that is not a JSON record shows as "?".
func programs(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n")) {
		var r Record
		if json.Unmarshal(l, &r) != nil {
			r.Program = "?"
		}
		out = append(out, r.Program)
	}
	return out
}

func TestPruneDropsExpiredEntriesAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	// A line with no readable time is kept: pruning does not guess.
	seed := line("old-1", 30*day) + line("old-2", 8*day) + line("kept-6d", 6*day) + "not a record\n" + line("kept-now", 0)
	if err := os.WriteFile(filepath.Join(dir, LogFile), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, 7*day, Record{Time: time.Now(), Action: ActionRm, Program: "new"}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(programs(t, dir), ","), "kept-6d,?,kept-now,new"; got != want {
		t.Fatalf("log holds %q, want %q", got, want)
	}
	// One file only: no rotated copies and no temp file left over from the rewrite.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != LogFile && e.Name() != lockFile {
			t.Errorf("unexpected file %s", e.Name())
		}
	}
	if fi, _ := os.Stat(filepath.Join(dir, LogFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("audit.log mode %v", fi.Mode().Perm())
	}
}

// Every writer is its own flock holder, like separate agv processes. A fake clock moves one
// minute per append and the retention is 30 minutes, so after the first 30 appends nearly every
// append rewrites the file while the other writers append.
func TestConcurrentAppendsLoseNothingWhilePruning(t *testing.T) {
	var tick atomic.Int64
	base := time.Now()
	now = func() time.Time { return base.Add(time.Duration(tick.Load()) * time.Minute) }
	t.Cleanup(func() { now = time.Now })

	dir := filepath.Join(t.TempDir(), "home") // does not exist yet
	const writers, each, window = 8, 25, 30
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				n := tick.Add(1)
				rec := Record{Time: base.Add(time.Duration(n) * time.Minute), Action: ActionRun, Program: fmt.Sprintf("t%03d", n)}
				if err := Append(dir, window*time.Minute, rec); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	total := tick.Load()
	seen := map[string]int{}
	for _, p := range programs(t, dir) {
		seen[p]++
	}
	// The entries still inside the window are all there, exactly once.
	for n := total - window + 1; n <= total; n++ {
		if c := seen[fmt.Sprintf("t%03d", n)]; c != 1 {
			t.Errorf("t%03d appears %d times", n, c)
		}
	}
	for p, c := range seen {
		if c != 1 {
			t.Errorf("%q appears %d times", p, c)
		}
	}
	if int64(len(seen)) >= total {
		t.Errorf("nothing was pruned: %d entries after %d appends", len(seen), total)
	}
}

func TestWebhookFilters(t *testing.T) {
	rec := Record{Action: ActionRun, Secrets: []string{"AWS_PROD.region", "GITHUB.value"}}
	cases := []struct {
		name string
		w    Webhook
		want bool
	}{
		{"no filters match everything", Webhook{}, true},
		{"action listed", Webhook{Actions: []string{"rm", "run"}}, true},
		{"action not listed", Webhook{Actions: []string{"rm"}}, false},
		{"NAME matches its fields", Webhook{Secrets: []string{"AWS_PROD"}}, true},
		{"NAME.field matches that field", Webhook{Secrets: []string{"GITHUB.value"}}, true},
		{"another field of the entry", Webhook{Secrets: []string{"AWS_PROD.key"}}, false},
		{"a name that only starts the same", Webhook{Secrets: []string{"AWS"}}, false},
		{"both filters must match: secret wrong", Webhook{Actions: []string{"run"}, Secrets: []string{"OTHER"}}, false},
		{"both filters must match: action wrong", Webhook{Actions: []string{"rm"}, Secrets: []string{"GITHUB"}}, false},
		{"both filters match", Webhook{Actions: []string{"run"}, Secrets: []string{"GITHUB"}}, true},
	}
	for _, c := range cases {
		if got := c.w.matches(rec); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	if (Webhook{Secrets: []string{"GITHUB"}}).matches(Record{Action: ActionRun, Secrets: []string{}}) {
		t.Error("a secrets filter matched a run that used no secret")
	}
}

func TestConfig(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir) // no file: defaults
	if err != nil || !cfg.Enabled || cfg.Retention != 7*day || len(cfg.Webhooks) != 0 {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}

	path := filepath.Join(dir, ConfigFile)
	write := func(s string) { os.WriteFile(path, []byte(s), 0o600) }
	write(`{"audit": {"enabled": false, "retention": "36h"},
	        "webhooks": [{"url": "{{NTFY_TOPIC}}", "format": "ntfy", "actions": ["run"], "secrets": ["AWS_PROD.region"]}]}`)
	cfg, err = LoadConfig(dir)
	if err != nil || cfg.Enabled || cfg.Retention != 36*time.Hour || len(cfg.Webhooks) != 1 || cfg.Webhooks[0].Format != "ntfy" {
		t.Fatalf("valid file: %+v, %v", cfg, err)
	}

	// An invalid file is an error that names the file and never repeats a URL (it may be a secret).
	for _, bad := range []string{
		`{`,
		`{"auditt": {}}`,
		`{"audit": {"retention": "soon"}}`,
		`{"audit": {"retention": "0d"}}`,
		`{"webhooks": [{"url": "https://hooks.test/TOKEN-9", "actions": ["runn"]}]}`,
		`{"webhooks": [{"url": "https://hooks.test/TOKEN-9", "format": "xml"}]}`,
		`{"webhooks": [{"url": "ftp://hooks.test/TOKEN-9"}]}`,
		`{"webhooks": [{"url": "https://hooks.test/TOKEN-9", "secrets": ["aws"]}]}`,
		`{} {}`,
	} {
		write(bad)
		_, err := LoadConfig(dir)
		if err == nil || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "TOKEN-9") {
			t.Errorf("%s: error %v", bad, err)
		}
	}
}
