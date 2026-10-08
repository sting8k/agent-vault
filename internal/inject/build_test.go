package inject

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type fakeSource map[string]map[string]Field

func (s fakeSource) Entry(name string) (map[string]Field, error) {
	if e, ok := s[name]; ok {
		return e, nil
	}
	return nil, errors.New("no secret " + name)
}

func one(v string) map[string]Field { return map[string]Field{"value": {Value: []byte(v)}} }

// build runs Build with a private temp root and returns the root's agv directory.
func build(t *testing.T, src Source, req Request) (*Plan, string, error) {
	t.Helper()
	xdg := t.TempDir()
	req.Environ = append([]string{"XDG_RUNTIME_DIR=" + xdg}, req.Environ...)
	p, err := Build(req, src)
	if p != nil {
		t.Cleanup(func() { p.Cleanup() })
	}
	return p, filepath.Join(xdg, "agv"), err
}

func TestGrammarLeavesForeignBracesAlone(t *testing.T) {
	src := fakeSource{"A": one("VAL"), "DB": {"password": {Value: []byte("PW")}}}
	untouched := []string{"{{.ID}}", "{{.Names}}", "{{a}}", "{{A.Field}}", "{{ A }}", "{{file:}}", "{{A.}}", "{{A", "{A}"}
	argv := append([]string{"cmd"}, untouched...)
	argv = append(argv, "x{{A}}y", "{{DB.password}}", "{{{A}}}")
	p, _, err := build(t, src, Request{Argv: argv})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{"cmd"}, untouched...)
	want = append(want, "xVALy", "PW", "{VAL}")
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv = %q, want %q", p.Argv, want)
	}
}

func TestSubstitutionIsOnePass(t *testing.T) {
	src := fakeSource{
		"A": one("{{B}} {{file:B}}"),
		"B": one("SECOND"),
	}
	p, root, err := build(t, src, Request{Argv: []string{"cmd", "{{A}}"}, Env: []string{"V={{A}}"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Argv[1] != "{{B}} {{file:B}}" || !contains(p.Env, "V={{B}} {{file:B}}") {
		t.Fatalf("value was expanded again: argv=%q", p.Argv)
	}
	if _, err := os.Stat(root); err == nil {
		t.Fatal("re-expansion created temp files")
	}
}

func TestNoPlaceholderInCommand(t *testing.T) {
	_, _, err := build(t, fakeSource{"A": one("v")}, Request{Argv: []string{"{{A}}", "x"}})
	if err == nil {
		t.Fatal("placeholder in argv[0] accepted")
	}
}

func TestNULByteNamesLabelNotValue(t *testing.T) {
	src := fakeSource{"BIN": one("sec\x00ret-bytes"), "OK": one("fine")}
	_, _, err := build(t, src, Request{Argv: []string{"cmd", "{{BIN}}"}})
	if err == nil || !strings.Contains(err.Error(), "BIN.value") || strings.Contains(err.Error(), "sec") {
		t.Fatalf("want an error naming BIN.value only, got %v", err)
	}
	_, _, err = build(t, src, Request{Argv: []string{"cmd"}, Env: []string{"V={{BIN}}"}})
	if err == nil {
		t.Fatal("NUL byte accepted in env value")
	}
	// A file can hold any bytes.
	p, _, err := build(t, src, Request{Argv: []string{"cmd", "{{file:BIN}}"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p.Argv[1]); string(got) != "sec\x00ret-bytes" {
		t.Fatal("file does not hold the exact bytes")
	}
}

func TestEnvOverridesInheritedAndRejectsDuplicates(t *testing.T) {
	src := fakeSource{
		"TOK": one("t0k"),
		"AWS": {
			"key":    {Value: []byte("K"), Env: "AWS_KEY"},
			"secret": {Value: []byte("S"), Env: "AWS_SECRET"},
			"region": {Value: []byte("r"), Env: "TOK_ENV"},
			"note":   {Value: []byte("n")},
		},
		"NOENV": one("x"),
	}
	inherited := []string{"PATH=/bin", "AWS_KEY=old", "OTHER_CLOUD_TOKEN=keep", "TOK_ENV=old"}
	p, _, err := build(t, src, Request{
		Argv: []string{"cmd"}, Environ: inherited,
		Env: []string{"MY=a{{TOK}}b"}, EnvFrom: []string{"AWS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, kv := range p.Env {
		got[kv]++
	}
	for _, kv := range []string{"PATH=/bin", "OTHER_CLOUD_TOKEN=keep", "MY=at0kb", "AWS_KEY=K", "AWS_SECRET=S", "TOK_ENV=r"} {
		if got[kv] != 1 {
			t.Errorf("%s appears %d times in %q", kv, got[kv], p.Env)
		}
	}
	if got["AWS_KEY=old"] != 0 || got["TOK_ENV=old"] != 0 || got["note="] != 0 {
		t.Errorf("overridden or non-env entries present: %q", p.Env)
	}

	for name, req := range map[string]Request{
		"env twice":         {Env: []string{"X=1", "X=2"}},
		"env and env-from":  {Env: []string{"AWS_KEY=1"}, EnvFrom: []string{"AWS"}},
		"env-from twice":    {EnvFrom: []string{"AWS", "AWS"}},
		"entry without env": {EnvFrom: []string{"NOENV"}},
	} {
		req.Argv = []string{"cmd"}
		if _, _, err := build(t, src, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFileFieldsBecomeTempFiles(t *testing.T) {
	src := fakeSource{
		"KEY": {"key": {Value: []byte("PEM-DATA"), Env: "KEY_PATH", File: true}, "pass": {Value: []byte("pw"), Env: "KEY_PASS"}},
	}
	p, root, err := build(t, src, Request{
		Argv: []string{"cmd", "-i", "{{file:KEY.key}}", "{{file:KEY.key}}"},
		Env:  []string{"SSH=ssh -i {{file:KEY.key}}"}, EnvFrom: []string{"KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := p.Argv[2]
	if p.Argv[3] != path || !contains(p.Env, "SSH=ssh -i "+path) || !contains(p.Env, "KEY_PATH="+path) || !contains(p.Env, "KEY_PASS=pw") {
		t.Fatalf("one file per field expected, argv=%q env=%q", p.Argv, p.Env)
	}
	dir := filepath.Dir(path)
	if dir != filepath.Join(root, strconv.Itoa(os.Getpid())) {
		t.Fatalf("file not in the per-run directory: %s", path)
	}
	fi, _ := os.Stat(path)
	di, _ := os.Stat(dir)
	ri, _ := os.Stat(root)
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 || ri.Mode().Perm() != 0o700 {
		t.Fatalf("modes: file %v dir %v root %v", fi.Mode().Perm(), di.Mode().Perm(), ri.Mode().Perm())
	}
	if got, _ := os.ReadFile(path); string(got) != "PEM-DATA" {
		t.Fatal("wrong file content")
	}
	labels := map[string]bool{}
	for _, s := range p.Secrets {
		if labels[s.Label] {
			t.Fatalf("duplicate secret %s", s.Label)
		}
		labels[s.Label] = true
	}
	if !labels["KEY.key"] || !labels["KEY.pass"] {
		t.Fatalf("secrets = %v", labels)
	}
	if err := p.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup left the run directory")
	}
}

func TestFailureLeavesNoTempFiles(t *testing.T) {
	src := fakeSource{"KEY": one("pem"), "B": one("b")}
	for name, req := range map[string]Request{
		"later arg unresolved": {Argv: []string{"cmd", "{{file:KEY}}", "{{B.nofield}}"}},
		"later env unresolved": {Argv: []string{"cmd", "{{file:KEY}}"}, Env: []string{"X={{MISSING}}"}},
		"later env duplicate":  {Argv: []string{"cmd", "{{file:KEY}}"}, Env: []string{"X=1", "X=2"}},
	} {
		_, root, err := build(t, src, req)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		left, _ := filepath.Glob(filepath.Join(root, "*", "*"))
		dirs, _ := filepath.Glob(filepath.Join(root, "*"))
		if len(left) != 0 || len(dirs) != 0 {
			t.Errorf("%s: left %v %v", name, dirs, left)
		}
	}
}

func TestSweepRemovesOnlyDeadRunDirs(t *testing.T) {
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Skip("cannot run true:", err)
	}
	dead := c.Process.Pid
	xdg := t.TempDir()
	root := filepath.Join(xdg, "agv")
	mk := func(name string) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "KEY.value"), []byte("old"), 0o600)
		return d
	}
	deadDir := mk(strconv.Itoa(dead))
	liveDir := mk(strconv.Itoa(os.Getppid()))
	otherDir := mk("not-a-pid")

	p, err := Build(Request{Argv: []string{"cmd"}, Environ: []string{"XDG_RUNTIME_DIR=" + xdg}}, fakeSource{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()
	if _, err := os.Stat(deadDir); !os.IsNotExist(err) {
		t.Error("stale run directory not removed")
	}
	for _, d := range []string{liveDir, otherDir} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed", d)
		}
	}
}

func TestTempRootMustBeOurs(t *testing.T) {
	src := fakeSource{"KEY": one("pem")}
	req := Request{Argv: []string{"cmd", "{{file:KEY}}"}}

	// A symlink planted at the root path is refused, and nothing is written through it.
	xdg := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(xdg, "agv")); err != nil {
		t.Fatal(err)
	}
	req.Environ = []string{"XDG_RUNTIME_DIR=" + xdg}
	if p, err := Build(req, src); err == nil {
		p.Cleanup()
		t.Fatal("symlinked root accepted")
	}
	if left, _ := os.ReadDir(target); len(left) != 0 {
		t.Fatal("wrote through the symlink")
	}

	// An existing root with loose permissions is tightened.
	xdg = t.TempDir()
	os.Mkdir(filepath.Join(xdg, "agv"), 0o755)
	os.Chmod(filepath.Join(xdg, "agv"), 0o755)
	req.Environ = []string{"XDG_RUNTIME_DIR=" + xdg}
	p, err := Build(req, src)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()
	if fi, _ := os.Stat(filepath.Join(xdg, "agv")); fi.Mode().Perm() != 0o700 {
		t.Fatalf("root mode %v", fi.Mode().Perm())
	}

	// A relative temp dir would put secrets in the workspace.
	req.Environ = []string{"TMPDIR=relative/tmp"}
	if p, err := Build(req, src); err == nil {
		p.Cleanup()
		t.Fatal("relative temp directory accepted")
	}
}

// Without XDG_RUNTIME_DIR, which is the normal case on macOS, the root is under $TMPDIR. There it
// ends in a slash and sits behind a symlink (/var -> /private/var).
func TestTempRootUnderTMPDIR(t *testing.T) {
	tmp := t.TempDir()
	link := filepath.Join(t.TempDir(), "var")
	if err := os.Symlink(tmp, link); err != nil {
		t.Fatal(err)
	}
	p, err := Build(Request{Argv: []string{"cmd", "{{file:KEY}}"}, Environ: []string{"TMPDIR=" + link + "/"}}, fakeSource{"KEY": one("pem")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()
	want := filepath.Join(tmp, "agv-"+strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getpid()), "KEY.value")
	if got, err := os.ReadFile(want); err != nil || string(got) != "pem" {
		t.Fatalf("no temp file at %s: %q, %v (argv %q)", want, got, err, p.Argv)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
