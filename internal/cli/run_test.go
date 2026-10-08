//go:build unix

package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// runMain runs `agv run ARGS` in this process against the test vault.
func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	useTestVault(t)
	var out, errb bytes.Buffer
	code = Main(append([]string{"run"}, args...), nil, &out, &errb, helperEnv(t))
	return code, out.String(), errb.String()
}

func TestRunNeverPrintsASecretValue(t *testing.T) {
	code, out, errs := runMain(t, "--env", "TOKEN={{TOK}}", "--", os.Args[0], "-test.run=^TestRunHelperProcess$", "--", "leak")
	if code != 3 {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
	for _, s := range []string{out, errs} {
		if strings.Contains(s, tokValue) || strings.Contains(s, b64(tokValue)) {
			t.Fatalf("a secret value reached agv's output: %q", s)
		}
	}
	want := "raw:[REDACTED:TOK.value]\nb64:[REDACTED:TOK.value]\n"
	if out != want || errs != "err:[REDACTED:TOK.value]\n" {
		t.Fatalf("stdout %q stderr %q", out, errs)
	}
}

func TestCommandKeepsItsOwnFlagsAndBraces(t *testing.T) {
	code, out, errs := runMain(t, "--timeout", "30s", "--", os.Args[0], "-test.run=^TestRunHelperProcess$", "--", "args", "--env", "X={{TOK}}", "--timeout=1ms", "{{.ID}}", "--", "{{file:KEY.key}}")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// The command's own --env/--timeout and a second "--" are passed through, not parsed.
	if len(lines) != 6 || lines[0] != "--env" || lines[1] != "X=[REDACTED:TOK.value]" || lines[2] != "--timeout=1ms" || lines[3] != "{{.ID}}" || lines[4] != "--" {
		t.Fatalf("args reached the command as %q", lines)
	}
}

func TestRunRefusesWithoutSeparator(t *testing.T) {
	code, out, errs := runMain(t, "--env", "A=b", os.Args[0])
	if code != 125 || out != "" || !strings.HasPrefix(errs, "agv: ") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errs)
	}
}

// `agv run -- psql ... < file.sql` must work: the child reads agv's own stdin.
func TestChildInheritsStdin(t *testing.T) {
	useTestVault(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() { w.WriteString("select 1;\n"); w.Close() }()
	var out, errb bytes.Buffer
	code := Main([]string{"run", "--", os.Args[0], "-test.run=^TestRunHelperProcess$", "--", "stdin"}, r, &out, &errb, helperEnv(t))
	if code != 0 || out.String() != "select 1;\n" {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
}
