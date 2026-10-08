package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sting8k/agent-vault/internal/vault"
)

// sentinel stands for a secret value. No agv output may ever contain it (G-7n9t).
const sentinel = "S3NTINEL-do-not-print"

type result struct {
	code     int
	out, err string
}

func (r result) all() string { return r.out + r.err }

func newHome(t *testing.T) string { return filepath.Join(t.TempDir(), "home") }

func agv(t *testing.T, home string, stdin io.Reader, args ...string) result {
	t.Helper()
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var out, errb bytes.Buffer
	code := Main(args, stdin, &out, &errb, []string{"AGV_HOME=" + home})
	return result{code, out.String(), errb.String()}
}

func openVault(t *testing.T, home string) *vault.Vault {
	t.Helper()
	v, err := vault.Open([]string{"AGV_HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tmpFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustFields(t *testing.T, home, name string) map[string]vault.Field {
	t.Helper()
	f, err := openVault(t, home).Fields(name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func noVault(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("a failed command created %s", home)
	}
}

func assertNoLeak(t *testing.T, r result, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(r.all(), s) {
			t.Errorf("output contains a secret value:\n%s", r.all())
		}
	}
}

// fakeTerm is stdin for tests: a person at a terminal. Each prompt takes the first unused
// reply whose key the prompt contains; with none, the person just presses Enter.
type fakeTerm struct {
	script   []reply
	asked    []asked
	onSecret func() // runs once, before the first hidden prompt is answered
}

type reply struct {
	key, text string
	used      bool
}

type asked struct {
	prompt string
	hidden bool
}

func person(replies ...string) *fakeTerm {
	f := &fakeTerm{}
	for i := 0; i+1 < len(replies); i += 2 {
		f.script = append(f.script, reply{key: replies[i], text: replies[i+1]})
	}
	return f
}

func (f *fakeTerm) Read([]byte) (int, error) { return 0, io.EOF }

func (f *fakeTerm) answer(prompt string, hidden bool) string {
	f.asked = append(f.asked, asked{prompt, hidden})
	for i := range f.script {
		if !f.script[i].used && strings.Contains(prompt, f.script[i].key) {
			f.script[i].used = true
			return f.script[i].text
		}
	}
	return ""
}

func (f *fakeTerm) line(p string) (string, error) { return f.answer(p, false), nil }

func (f *fakeTerm) secret(p string) ([]byte, error) {
	if f.onSecret != nil {
		hook := f.onSecret
		f.onSecret = nil
		hook()
	}
	return []byte(f.answer(p, true)), nil
}

func (f *fakeTerm) askedAbout(key string) *asked {
	for i := range f.asked {
		if strings.Contains(f.asked[i].prompt, key) {
			return &f.asked[i]
		}
	}
	return nil
}

func TestSetFromFilesAndStdinStoresPresetFieldsUntrimmed(t *testing.T) {
	home := newHome(t)
	idFile := tmpFile(t, "AKIA-test-id")
	regionFile := tmpFile(t, "eu-west-1\n") // a trailing newline is a value, not noise
	r := agv(t, home, strings.NewReader(sentinel+"\n"),
		"set", "AWS_PROD", "--type", "aws", "--desc", "AWS prod",
		"--field", "access_key_id=@"+idFile, "--field", "secret_access_key=-", "--field", "region=@"+regionFile)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	assertNoLeak(t, r, sentinel, "AKIA-test-id")
	f := mustFields(t, home, "AWS_PROD")
	if string(f["secret_access_key"].Value) != sentinel+"\n" || string(f["region"].Value) != "eu-west-1\n" {
		t.Error("values were changed or trimmed")
	}
	if f["secret_access_key"].Env != "AWS_SECRET_ACCESS_KEY" || f["region"].Env != "AWS_REGION" {
		t.Error("preset env names were not applied")
	}
	if _, ok := f["session_token"]; ok {
		t.Error("an optional field nobody gave was stored")
	}

	key := tmpFile(t, "-----BEGIN-----\n"+sentinel+"\n-----END-----\n")
	r = agv(t, home, nil, "set", "DEPLOY", "--type", "ssh-key", "--desc", "deploy key", "--field", "key=@"+key)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	assertNoLeak(t, r, sentinel)
	if k := mustFields(t, home, "DEPLOY")["key"]; !k.File || !strings.Contains(string(k.Value), sentinel) {
		t.Error("the ssh-key field is not a file field holding the file's content")
	}
}

func TestSetNeverEchoesWhatWasTypedOnTheCommandLine(t *testing.T) {
	for _, args := range [][]string{
		{"set", "N", "--type", "basic", "--desc", "d", "--field", "password=" + sentinel},
		{"set", "N", "--type", "basic", "--desc", "d", "--field", sentinel + "=="},
		{"set", "N", "--type", "basic", "--desc", "d", "--field", "password", sentinel},
		{"set", "N", "--type", "basic", "--desc", "d", "--field", "password=@" + sentinel},
		{"set", "N", "--type", "basic", "--desc", "d", "--field", "password=@" + sentinel + "-missing"},
		{"set", "N", "--type", "basic", "--desc", "d", "--field", "username=-", "--field", "password=-"},
		{"set", "N", sentinel},
		{"set", "N", "-" + sentinel},
		{"set", "N", "--type=" + sentinel},
		{"set", "N", "--yes=" + sentinel},
		{"list", sentinel, sentinel},
		{"rm", "N", sentinel},
	} {
		home := newHome(t)
		r := agv(t, home, strings.NewReader(""), args...)
		if r.code == 0 {
			t.Errorf("%v succeeded", args[:2])
		}
		assertNoLeak(t, r, sentinel)
		if args[0] == "set" {
			noVault(t, home)
		}
	}
}

func TestSetWithoutTerminalFailsAtOnceAndWritesNothing(t *testing.T) {
	for _, args := range [][]string{
		{"set"},
		{"set", "N"},
		{"set", "N", "--type", "aws", "--desc", "d"},       // fields missing
		{"set", "N", "--type", "api-token", "--desc", "d"}, // a required field missing
		{"set", "N", "--desc", "d", "--field", "value=-"},  // type missing
	} {
		home := newHome(t)
		r := agv(t, home, strings.NewReader("some piped input\n"), args...)
		if r.code == 0 || !strings.Contains(r.err, "--field") {
			t.Errorf("%v: exit %d, want a failure that shows the non-interactive form:\n%s", args, r.code, r.err)
		}
		noVault(t, home)
	}
}

func TestSetInteractiveAsksForValuesHiddenAndNeverPrintsThem(t *testing.T) {
	home := newHome(t)
	regionFile := tmpFile(t, "eu-west-1")
	term := person(
		"Name", "AWS_PROD", "Type", "aws", "Description", "AWS prod, read-only S3",
		".access_key_id", "AKIA"+sentinel, ".secret_access_key", sentinel,
		".region", "@"+regionFile, // hidden answer that names a file
		"Save?", "y")
	r := agv(t, home, term, "set")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	assertNoLeak(t, r, sentinel)
	f := mustFields(t, home, "AWS_PROD")
	if string(f["secret_access_key"].Value) != sentinel || string(f["region"].Value) != "eu-west-1" {
		t.Error("answers were not stored as given")
	}
	if _, ok := f["session_token"]; ok {
		t.Error("an optional field answered with Enter was stored")
	}
	for _, field := range []string{".access_key_id", ".secret_access_key", ".region"} {
		if a := term.askedAbout(field); a == nil || !a.hidden {
			t.Errorf("%s was not asked with hidden input", field)
		}
	}
}

func TestSetFileFieldAsksForAPathAndReadsIt(t *testing.T) {
	home := newHome(t)
	path := tmpFile(t, "{\"private_key\": \""+sentinel+"\"}\n")
	term := person("Name", "GCP", "Type", "gcp-sa", "Description", "gcp", ".key", path)
	r := agv(t, home, term, "set")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	assertNoLeak(t, r, sentinel)
	k := mustFields(t, home, "GCP")["key"]
	if !strings.Contains(string(k.Value), sentinel) || !k.File || k.Env != "GOOGLE_APPLICATION_CREDENTIALS" {
		t.Error("the file's content or the preset's flags were not stored")
	}
	if a := term.askedAbout(".key"); a == nil || a.hidden {
		t.Error("a path should be asked with visible input")
	}
}

func TestSetDeclinedConfirmationSavesNothing(t *testing.T) {
	home := newHome(t)
	term := person("Name", "N", "Type", "api-token", "Description", "d", ".value", sentinel, "Save?", "n")
	r := agv(t, home, term, "set")
	if r.code == 0 {
		t.Error("declining exited 0")
	}
	assertNoLeak(t, r, sentinel)
	noVault(t, home)
}

func TestSetPatchesAnExistingEntry(t *testing.T) {
	home := newHome(t)
	f := func(s string) string { return "@" + tmpFile(t, s) }
	r := agv(t, home, nil, "set", "AWS_PROD", "--type", "aws", "--desc", "AWS prod",
		"--field", "access_key_id="+f("old-id-value"), "--field", "secret_access_key="+f("old-secret-value"),
		"--field", "region="+f("eu-west-1"))
	if r.code != 0 {
		t.Fatal(r.err)
	}

	// Interactive: Enter keeps everything except what is answered.
	term := person(".access_key_id", "new-id-value")
	if r := agv(t, home, term, "set", "AWS_PROD"); r.code != 0 {
		t.Fatal(r.err)
	}
	got := mustFields(t, home, "AWS_PROD")
	if string(got["access_key_id"].Value) != "new-id-value" || string(got["secret_access_key"].Value) != "old-secret-value" ||
		string(got["region"].Value) != "eu-west-1" {
		t.Error("Enter did not keep the stored values")
	}
	if e, _ := openVault(t, home).Entry("AWS_PROD"); e.Description != "AWS prod" || e.Type != "aws" {
		t.Error("Enter did not keep the description and type")
	}

	// Only --unset removes a field, and it leaves the rest alone.
	if r := agv(t, home, person(), "set", "AWS_PROD", "--field", "session_token="+f("tok-tok-tok")); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := agv(t, home, person(), "set", "AWS_PROD", "--unset", "session_token"); r.code != 0 {
		t.Fatal(r.err)
	}
	got = mustFields(t, home, "AWS_PROD")
	if _, ok := got["session_token"]; ok || len(got) != 3 {
		t.Errorf("--unset removed the wrong fields: %d left", len(got))
	}
	if r := agv(t, home, person(), "set", "AWS_PROD", "--unset", "nothing"); r.code == 0 {
		t.Error("unsetting a field that does not exist succeeded")
	}
}

func TestSetChangingTheTypeKeepsMatchingFieldsAndRemovesNone(t *testing.T) {
	home := newHome(t)
	keyFile := tmpFile(t, "key-material-"+sentinel)
	extra := tmpFile(t, "extra-material")
	r := agv(t, home, nil, "set", "SVC", "--type", "custom", "--desc", "svc",
		"--field", "key=@"+keyFile, "--field", "extra=@"+extra)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	if k := mustFields(t, home, "SVC")["key"]; k.File || k.Env != "" {
		t.Fatal("a custom --field should carry no env name or file flag")
	}
	// Nothing is missing for gcp-sa (key is stored), so nothing is asked but the confirmation.
	if r := agv(t, home, person(), "set", "SVC", "--type", "gcp-sa"); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	got := mustFields(t, home, "SVC")
	if !strings.Contains(string(got["key"].Value), sentinel) {
		t.Error("the field with a matching name lost its value")
	}
	if !got["key"].File || got["key"].Env != "GOOGLE_APPLICATION_CREDENTIALS" {
		t.Error("the new type's env name and file flag were not applied to the matching field")
	}
	if string(got["extra"].Value) != "extra-material" {
		t.Error("a field outside the new type was removed without --unset")
	}
	// A required field of the new type that is not stored must be given.
	if r := agv(t, home, nil, "set", "SVC", "--type", "basic"); r.code == 0 || !strings.Contains(r.err, "terminal") {
		t.Errorf("changing to a type with unfilled required fields: exit %d\n%s", r.code, r.err)
	}
}

// An agent has no terminal. It may add entries, but must never overwrite or remove one:
// that would lose the only copy of a secret.
func TestWithoutTerminalExistingEntriesAreNeverChanged(t *testing.T) {
	home := newHome(t)
	seed(t, home, "KEEP", "api-token", "d", "value")
	for _, args := range [][]string{
		{"set", "KEEP", "--field", "value=-"},
		{"set", "KEEP", "--desc", "changed"},
		{"set", "KEEP", "--type", "custom"},
		{"set", "KEEP", "--unset", "value"},
		{"rm", "KEEP"},
		{"rm", "KEEP", "--yes"},
	} {
		r := agv(t, home, strings.NewReader("y\nreplacement-value\n"), args...)
		if r.code == 0 || strings.Contains(r.err, "--yes") {
			t.Errorf("%v: exit %d, or the error teaches a way around the terminal:\n%s", args, r.code, r.err)
		}
	}
	e, _ := openVault(t, home).Entry("KEEP")
	if e.Description != "d" || e.Type != "api-token" || string(mustFields(t, home, "KEEP")["value"].Value) != sentinel+"-value" {
		t.Error("an existing entry was changed without a terminal")
	}
}

func TestSetKeepsShortAndPaddedValuesExactlyAndWarns(t *testing.T) {
	home := newHome(t)
	path := tmpFile(t, "abc \n")
	r := agv(t, home, nil, "set", "N", "--type", "api-token", "--desc", "d", "--field", "value=@"+path)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	if string(mustFields(t, home, "N")["value"].Value) != "abc \n" {
		t.Error("the value was trimmed")
	}
	if !strings.Contains(r.err, "warning") {
		t.Error("a 5-byte value with trailing whitespace produced no warning")
	}
	// An empty file would otherwise read as "keep the current value".
	if r := agv(t, home, nil, "set", "N", "--field", "value=@"+tmpFile(t, "")); r.code == 0 {
		t.Error("an empty file was accepted as a value")
	}
	if string(mustFields(t, home, "N")["value"].Value) != "abc \n" {
		t.Error("a rejected empty value changed the stored one")
	}
}

func TestSetTakesTheLockOnlyAfterTheLastPrompt(t *testing.T) {
	home := newHome(t)
	other := openVault(t, home)
	term := person("Name", "SLOW", "Type", "api-token", "Description", "d", ".value", "a-long-token-value")
	term.onSecret = func() { // another writer works while the person is still typing
		done := make(chan error, 1)
		go func() {
			done <- other.Set("OTHER", vault.Change{Description: "d", Type: "api-token",
				Fields: []vault.FieldChange{{Name: "value", Value: []byte("other-value")}}})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("another writer was blocked while set was waiting for the person")
		}
	}
	if r := agv(t, home, term, "set"); r.code != 0 {
		t.Fatal(r.err)
	}
	if l, _ := openVault(t, home).List(); len(l) != 2 {
		t.Errorf("%d entries; a concurrent writer's entry was lost", len(l))
	}
}

func TestSetChecksTheVaultBeforeAsking(t *testing.T) {
	home := newHome(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "vault.json"), []byte("{broken"), 0o600)
	term := person()
	r := agv(t, home, term, "set")
	if r.code == 0 || !strings.Contains(r.err, "vault.json") {
		t.Errorf("exit %d: %s", r.code, r.err)
	}
	if len(term.asked) != 0 {
		t.Error("the person was asked questions before the broken vault was reported")
	}
}
