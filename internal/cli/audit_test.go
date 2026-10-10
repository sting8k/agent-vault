//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sting8k/agent-vault/internal/audit"
	"github.com/sting8k/agent-vault/internal/inject"
)

// auditEnv is a run environment whose AGV_HOME holds the given config.json.
func auditEnv(t *testing.T, config string) (env []string, home string) {
	t.Helper()
	env = helperEnv(t)
	home = envOf(env, "AGV_HOME")
	if config != "" {
		if err := os.WriteFile(filepath.Join(home, audit.ConfigFile), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return env, home
}

func auditLog(t *testing.T, home string) (raw string, recs []audit.Record) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, audit.LogFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var r audit.Record
		if err := json.Unmarshal(l, &r); err != nil {
			t.Fatalf("audit.log line %q: %v", l, err)
		}
		recs = append(recs, r)
	}
	return string(b), recs
}

func runIn(t *testing.T, env []string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Main(append([]string{"run"}, args...), nil, &out, &errb, env)
	return code, out.String(), errb.String()
}

// The record is built from names and argv[0] only, so neither the log nor a webhook body (json or
// ntfy) can carry a value or another argument.
func TestRecordsCarryNamesAndProgramOnly(t *testing.T) {
	var mu sync.Mutex
	posts := map[string]string{} // path -> headers and body
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		posts[r.URL.Path] = fmt.Sprint(r.Header) + string(b)
	}))
	defer srv.Close()
	env, home := auditEnv(t, fmt.Sprintf(`{"webhooks": [{"url": %q}, {"url": %q, "format": "ntfy"}]}`, srv.URL+"/json", srv.URL+"/ntfy"))
	useTestVault(t)

	cmd := selfCmd("args", "PRIVATE-ARG", "{{TOK}}")
	if code, _, errs := runIn(t, env, append([]string{"--env", "TOKEN={{TOK}}", "--"}, cmd...)...); code != 0 || errs != "" {
		t.Fatalf("code %d, stderr %q", code, errs)
	}

	raw, recs := auditLog(t, home)
	if len(recs) != 1 {
		t.Fatalf("%d audit lines, want 1", len(recs))
	}
	r := recs[0]
	if r.Action != "run" || r.Program != os.Args[0] || r.Exit != 0 || len(r.Secrets) != 1 || r.Secrets[0] != "TOK.value" || r.Cwd == "" || r.Time.IsZero() {
		t.Fatalf("record %+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 2 || !strings.HasSuffix(posts["/json"], strings.TrimSpace(raw)) {
		t.Fatalf("json webhook should post the audit line; got %v", posts)
	}
	for sink, text := range map[string]string{"audit.log": raw, "json webhook": posts["/json"], "ntfy webhook": posts["/ntfy"]} {
		for _, bad := range []string{tokValue, b64(tokValue), "PRIVATE-ARG", "-test.run", "AGV_RUNTEST"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s contains %q", sink, bad)
			}
		}
	}
	if !strings.Contains(posts["/ntfy"], "TOK.value") {
		t.Errorf("ntfy message does not name the secret: %q", posts["/ntfy"])
	}
}

// The record is written after the child is gone, so how it ended is in the log: a timeout, and a
// SIGTERM sent to agv (which it forwards).
func TestTimeoutAndSignalExitsAreLogged(t *testing.T) {
	env, home := auditEnv(t, "")
	if code, _, _ := runIn(t, env, append([]string{"--timeout", "200ms", "--"}, selfCmd("sleep")...)...); code != 124 {
		t.Fatalf("code %d", code)
	}
	if _, recs := auditLog(t, home); len(recs) != 1 || recs[0].Exit != 124 {
		t.Fatalf("after a timeout: %+v", recs)
	}

	p := startAgv(t, append([]string{"run", "--"}, selfCmd("report-and-wait")...)...)
	p.line(t)
	if err := syscall.Kill(p.cmd.Process.Pid, syscall.SIGTERM); err != nil { // agv only, not its group
		t.Fatal(err)
	}
	if ws := p.wait(t, 10*time.Second); !ws.Exited() || ws.ExitStatus() != 128+int(syscall.SIGTERM) {
		t.Fatalf("agv status %v", ws)
	}
	if _, recs := auditLog(t, p.home); len(recs) != 1 || recs[0].Exit != 128+int(syscall.SIGTERM) {
		t.Fatalf("after SIGTERM: %+v", recs)
	}
}

// A webhook that hangs, fails, cannot connect or names a missing secret costs a warning each and
// nothing else. The URLs come from the vault because they are secrets: no warning may show them.
func TestWebhookTroubleKeepsTheExitCodeAndHidesTheURL(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // the server notices a closed connection only once the body is read
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer hang.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) }))
	defer broken.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL + "/SECRET-PATH-dead"
	dead.Close()

	v := fakeVault{
		"TOK":       testVault["TOK"],
		"HOOK_HANG": {"value": {Value: []byte(hang.URL + "/SECRET-PATH-hang")}},
		"HOOK_500":  {"value": {Value: []byte(broken.URL + "/SECRET-PATH-500")}},
		"HOOK_DEAD": {"value": {Value: []byte(deadURL)}},
	}
	old := openSource
	openSource = func(IO) (inject.Source, error) { return v, nil }
	defer func() { openSource = old }()
	oldTimeout := audit.Timeout
	audit.Timeout = 300 * time.Millisecond
	defer func() { audit.Timeout = oldTimeout }()

	env, _ := auditEnv(t, `{"webhooks": [{"url": "{{HOOK_HANG}}"}, {"url": "{{HOOK_500}}"}, {"url": "{{HOOK_DEAD}}"}, {"url": "{{HOOK_MISSING}}"}]}`)
	start := time.Now()
	code, _, errs := runIn(t, env, append([]string{"--env", "TOKEN={{TOK}}", "--"}, selfCmd("leak")...)...)
	if code != 3 {
		t.Fatalf("exit code %d, want the child's 3", code)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a hanging webhook held agv for %v", d)
	}
	warnings := 0
	for _, l := range strings.Split(errs, "\n") {
		if strings.HasPrefix(l, "agv: ") {
			warnings++
		}
	}
	if warnings != 4 {
		t.Errorf("%d warnings, want one per webhook:\n%s", warnings, errs)
	}
	for _, secret := range []string{"SECRET-PATH", "127.0.0.1", "localhost", tokValue} {
		if strings.Contains(errs, secret) {
			t.Errorf("a warning contains %q:\n%s", secret, errs)
		}
	}
}

func TestAuditWriteFailureOnlyWarns(t *testing.T) {
	env, home := auditEnv(t, "")
	if err := os.Mkdir(filepath.Join(home, audit.LogFile), 0o700); err != nil { // cannot be appended to
		t.Fatal(err)
	}
	useTestVault(t)
	code, out, errs := runIn(t, env, append([]string{"--env", "TOKEN={{TOK}}", "--"}, selfCmd("leak")...)...)
	if code != 3 || !strings.Contains(out, "raw:") {
		t.Fatalf("the command did not run normally: code %d, stdout %q", code, out)
	}
	if !strings.Contains(errs, "agv: audit log: ") {
		t.Fatalf("no warning: %q", errs)
	}
}

// A config file that cannot be understood stops run, set and rm before they do anything, and the
// error names it.
func TestInvalidConfigStopsTheCommand(t *testing.T) {
	env, home := auditEnv(t, `{"audit": {"retention": "soon"}}`)
	code, out, errs := runIn(t, env, append([]string{"--"}, selfCmd("args", "ran")...)...)
	if code != 125 || out != "" || !strings.Contains(errs, audit.ConfigFile) {
		t.Fatalf("run: code %d, stdout %q, stderr %q", code, out, errs)
	}
	r := agv(t, home, strings.NewReader(sentinel+"\n"), "set", "NAME", "--type", "api-token", "--desc", "d", "--field", "value=-")
	if r.code != 125 || !strings.Contains(r.err, audit.ConfigFile) {
		t.Fatalf("set: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, "vault.json")); !os.IsNotExist(err) {
		t.Errorf("set stored a secret despite the invalid config: %v", err)
	}
}

func TestSetAndRmAreLoggedByNameAndListIsNot(t *testing.T) {
	home := newHome(t)
	if r := agv(t, home, strings.NewReader(sentinel+"\n"), "set", "NAME", "--type", "api-token", "--desc", "d", "--field", "value=-"); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := agv(t, home, nil, "list"); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := agv(t, home, person("Remove", "y"), "rm", "NAME"); r.code != 0 {
		t.Fatal(r.err)
	}
	raw, recs := auditLog(t, home)
	if len(recs) != 2 || recs[0].Action != "set" || recs[1].Action != "rm" {
		t.Fatalf("records %+v", recs)
	}
	for _, r := range recs {
		if len(r.Secrets) != 1 || r.Secrets[0] != "NAME" || r.Program != "" {
			t.Errorf("record %+v", r)
		}
	}
	if strings.Contains(raw, sentinel) {
		t.Errorf("audit.log contains the value")
	}
}
