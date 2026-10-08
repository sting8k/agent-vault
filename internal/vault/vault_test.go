package vault

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

const sentinel = "S3NTINEL-value-do-not-leak"

func newVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Open([]string{"AGV_HOME=" + filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func setValue(t *testing.T, v *Vault, name, value string) {
	t.Helper()
	err := v.Set(name, Change{Description: "d", Type: "api-token", Fields: []FieldChange{{Name: "value", Value: []byte(value)}}})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, v *Vault, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(v.path(name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rewrite edits vault.json as a hand-editing attacker or a bad merge would.
func rewrite(t *testing.T, v *Vault, edit func(f *file)) {
	t.Helper()
	var f file
	if err := json.Unmarshal(readFile(t, v, vaultFile), &f); err != nil {
		t.Fatal(err)
	}
	edit(&f)
	b, _ := json.Marshal(&f)
	if err := os.WriteFile(v.path(vaultFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTripKeepsBytesAndStoresNoPlaintext(t *testing.T) {
	v := newVault(t)
	values := map[string][]byte{
		"multiline": []byte("-----BEGIN KEY-----\n" + sentinel + "\n-----END KEY-----\n"),
		"padded":    []byte("  " + sentinel + "\t\n"),
		"binary":    {0, 1, 2, 0xff, 0},
	}
	var fcs []FieldChange
	for n, val := range values {
		fcs = append(fcs, FieldChange{Name: n, Value: val, Env: strings.ToUpper(n) + "_ENV", File: n == "binary"})
	}
	if err := v.Set("RT", Change{Description: "round trip", Type: "custom", Fields: fcs}); err != nil {
		t.Fatal(err)
	}
	got, err := v.Fields("RT")
	if err != nil {
		t.Fatal(err)
	}
	for n, val := range values {
		if !bytes.Equal(got[n].Value, val) {
			t.Errorf("field %s changed on round trip", n)
		}
	}
	if !got["binary"].File || got["padded"].File || got["padded"].Env != "PADDED_ENV" {
		t.Errorf("env or file flag lost: %+v", got)
	}
	disk := readFile(t, v, vaultFile)
	for _, plain := range []string{sentinel, base64.StdEncoding.EncodeToString(values["padded"])} {
		if bytes.Contains(disk, []byte(plain)) {
			t.Errorf("vault.json contains a plaintext form of a value")
		}
	}
}

func TestCiphertextMovedToAnotherFieldOrEntryFails(t *testing.T) {
	v := newVault(t)
	err := v.Set("A", Change{Description: "d", Type: "custom", Fields: []FieldChange{
		{Name: "x", Value: []byte("one")}, {Name: "y", Value: []byte("two")}}})
	if err != nil {
		t.Fatal(err)
	}
	setValue(t, v, "B", "three")

	rewrite(t, v, func(f *file) { // swap A.x and A.y
		a := f.Entries["A"].Fields
		a["x"].Value, a["y"].Value = a["y"].Value, a["x"].Value
	})
	if _, err := v.Fields("A"); err == nil {
		t.Error("value moved to another field still decrypts")
	}

	rewrite(t, v, func(f *file) { // put B.value into A.x and A.y again
		f.Entries["A"].Fields["x"].Value = f.Entries["B"].Fields["value"].Value
		f.Entries["A"].Fields["y"].Value = f.Entries["B"].Fields["value"].Value
	})
	if _, err := v.Fields("A"); err == nil {
		t.Error("value moved to another entry still decrypts")
	}
}

func TestFileAndDirectoryModes(t *testing.T) {
	v := newVault(t)
	setValue(t, v, "M", sentinel)
	setValue(t, v, "M", sentinel+"2") // a second save goes through the temp+rename path again
	check := func(path string, want os.FileMode) {
		t.Helper()
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s has mode %o, want %o", filepath.Base(path), st.Mode().Perm(), want)
		}
	}
	check(v.dir, 0o700)
	check(v.path(vaultFile), 0o600)
	check(v.path(keyFile), 0o600)
	check(v.path(lockFile), 0o600)
	ents, _ := os.ReadDir(v.dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if want := []string{lockFile, keyFile, vaultFile}; fmt.Sprint(names) != fmt.Sprint(sorted(want)) {
		t.Errorf("directory has %v; a temp file was left behind", names)
	}
}

func sorted(s []string) []string { sort.Strings(s); return s }

func TestKeyVaultStateTable(t *testing.T) {
	otherKey := bytes.Repeat([]byte{7}, keySize)
	writeKey := func(t *testing.T, v *Vault, key []byte) {
		t.Helper()
		if err := os.MkdirAll(v.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(v.path(keyFile), key, 0o600); err != nil { // key is file content
			t.Fatal(err)
		}
	}
	// failing runs every operation and wants an error naming file, with the disk untouched.
	failing := func(t *testing.T, v *Vault, file string) {
		t.Helper()
		snap := snapshot(t, v)
		ops := map[string]func() error{
			"List":   func() error { _, err := v.List(); return err },
			"Entry":  func() error { _, err := v.Entry("E"); return err },
			"Fields": func() error { _, err := v.Fields("E"); return err },
			"Set": func() error {
				return v.Set("E", Change{Description: "d", Type: "custom", Fields: []FieldChange{{Name: "v", Value: []byte("x")}}})
			},
			"Remove": func() error { return v.Remove("E") },
		}
		for op, fn := range ops {
			err := fn()
			if err == nil || !strings.Contains(err.Error(), file) {
				t.Errorf("%s: want an error naming %s, got %v", op, file, err)
			}
		}
		if after := snapshot(t, v); after != snap {
			t.Errorf("the failed operations changed the directory:\nbefore %s\nafter  %s", snap, after)
		}
	}

	t.Run("missing/missing: readers see an empty vault and write nothing; first writer makes key then vault", func(t *testing.T) {
		v := newVault(t)
		if l, err := v.List(); err != nil || len(l) != 0 {
			t.Fatalf("List = %v, %v", l, err)
		}
		if err := v.Remove("E"); !isNotFound(err) {
			t.Fatalf("Remove on an empty vault = %v", err)
		}
		if _, err := os.Stat(v.dir); !os.IsNotExist(err) {
			t.Fatal("reading or removing created the vault directory")
		}
		setValue(t, v, "E", "x")
		key, err := v.readKey()
		if err != nil || !bytes.Equal(readFile(t, v, keyFile), encodeKey(key)) {
			t.Fatalf("master.key is not hex text: %v", err)
		}
		var f file
		if err := json.Unmarshal(readFile(t, v, vaultFile), &f); err != nil || f.KeyID != keyID(key) {
			t.Fatalf("key_id %q does not match master.key (%v)", f.KeyID, err)
		}
	})

	t.Run("present/missing: first writer creates the vault with the existing key", func(t *testing.T) {
		v := newVault(t)
		// A key pasted back by hand, with stray whitespace, must still be accepted.
		restored := append([]byte("  "), append(encodeKey(otherKey), '\n')...)
		writeKey(t, v, restored)
		if l, err := v.List(); err != nil || len(l) != 0 {
			t.Fatalf("List = %v, %v", l, err)
		}
		setValue(t, v, "E", "x")
		if !bytes.Equal(readFile(t, v, keyFile), restored) {
			t.Fatal("the existing key was replaced")
		}
		var f file
		json.Unmarshal(readFile(t, v, vaultFile), &f)
		if f.KeyID != keyID(otherKey) {
			t.Fatal("the vault was not created with the existing key's key_id")
		}
	})

	t.Run("missing/present: error, and no new key is made", func(t *testing.T) {
		v := newVault(t)
		setValue(t, v, "E", "x")
		os.Remove(v.path(keyFile))
		failing(t, v, keyFile)
	})

	t.Run("present/present with another key_id: error", func(t *testing.T) {
		v := newVault(t)
		setValue(t, v, "E", "x")
		writeKey(t, v, encodeKey(otherKey))
		failing(t, v, keyFile)
	})

	t.Run("invalid JSON is never overwritten, with or without a key", func(t *testing.T) {
		for _, withKey := range []bool{true, false} {
			v := newVault(t)
			writeKey(t, v, encodeKey(otherKey))
			if !withKey {
				os.Remove(v.path(keyFile))
			}
			os.WriteFile(v.path(vaultFile), []byte(`{"version": 1, "entries": {`), 0o600)
			failing(t, v, vaultFile)
		}
	})

	t.Run("a malformed key is an error, not a new key", func(t *testing.T) {
		for _, bad := range [][]byte{[]byte("short"), otherKey, append(encodeKey(otherKey)[:62], 'z', 'z')} {
			v := newVault(t)
			writeKey(t, v, bad)
			snap := snapshot(t, v)
			err := v.Set("E", Change{Description: "d", Type: "custom", Fields: []FieldChange{{Name: "v", Value: []byte("x")}}})
			if err == nil || !strings.Contains(err.Error(), keyFile) {
				t.Fatalf("Set with key %q = %v", bad, err)
			}
			if snapshot(t, v) != snap {
				t.Fatal("the key was touched")
			}
		}
	})
}

// snapshot is the content of the vault directory except the lock file.
func snapshot(t *testing.T, v *Vault) string {
	t.Helper()
	var sb strings.Builder
	ents, err := os.ReadDir(v.dir)
	if err != nil {
		return "(no directory)"
	}
	for _, e := range ents {
		if e.Name() == lockFile {
			continue
		}
		fmt.Fprintf(&sb, "%s=%x;", e.Name(), readFile(t, v, e.Name()))
	}
	return sb.String()
}

func isNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

func TestConcurrentWritersLoseNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home") // does not exist: the writers also race to create the key
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _ := Open([]string{"AGV_HOME=" + home}) // each writer has its own handle and lock fd
			errs <- v.Set(fmt.Sprintf("E%02d", i), Change{Description: "d", Type: "api-token",
				Fields: []FieldChange{{Name: "value", Value: []byte(fmt.Sprintf("value-%02d", i))}}})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	v, _ := Open([]string{"AGV_HOME=" + home})
	list, err := v.List()
	if err != nil || len(list) != n {
		t.Fatalf("%d entries survived of %d (%v)", len(list), n, err)
	}
	for i, e := range list {
		if e.Name != fmt.Sprintf("E%02d", i) {
			t.Fatalf("List is not in name order: %s at %d", e.Name, i)
		}
		f, err := v.Fields(e.Name)
		if err != nil || string(f["value"].Value) != fmt.Sprintf("value-%02d", i) {
			t.Fatalf("%s lost or mixed its value (%v)", e.Name, err)
		}
	}
}

func TestSetPatchesTheStoredEntry(t *testing.T) {
	v := newVault(t)
	err := v.Set("P", Change{Description: "first", Type: "custom", Fields: []FieldChange{
		{Name: "keep", Value: []byte("kept-value"), Env: "KEEP_ENV"},
		{Name: "swap", Value: []byte("old")},
		{Name: "drop", Value: []byte("gone")}}})
	if err != nil {
		t.Fatal(err)
	}
	var before file
	json.Unmarshal(readFile(t, v, vaultFile), &before)

	err = v.Set("P", Change{Description: "second", Type: "custom", Unset: []string{"drop"}, Fields: []FieldChange{
		{Name: "keep", Env: "KEEP_ENV"}, // no value: keep the stored one
		{Name: "swap", Value: []byte("new")},
		{Name: "added", Value: []byte("fresh"), File: true}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Fields("P")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || string(got["keep"].Value) != "kept-value" || string(got["swap"].Value) != "new" ||
		string(got["added"].Value) != "fresh" || !got["added"].File || got["keep"].Env != "KEEP_ENV" {
		t.Errorf("patch result wrong: %d fields", len(got))
	}
	var after file
	json.Unmarshal(readFile(t, v, vaultFile), &after)
	if !after.Entries["P"].CreatedAt.Equal(before.Entries["P"].CreatedAt) || after.Entries["P"].Description != "second" {
		t.Error("created_at changed or description not updated")
	}

	// A patch built from an earlier view must not invent a value for a field that is gone.
	snap := snapshot(t, v)
	err = v.Set("P", Change{Description: "x", Type: "custom", Fields: []FieldChange{{Name: "drop"}}})
	if err == nil || snapshot(t, v) != snap {
		t.Errorf("keeping a field that does not exist: err=%v, disk changed=%v", err, snapshot(t, v) != snap)
	}
}

func TestSetRejectsInvalidEntriesAndLeavesTheVaultAlone(t *testing.T) {
	v := newVault(t)
	setValue(t, v, "OK", "x")
	snap := snapshot(t, v)
	ok := func(c *Change) {}
	cases := map[string]struct {
		name string
		edit func(c *Change)
	}{
		"bad name":          {"aws", ok},
		"blank description": {"N", func(c *Change) { c.Description = "  " }},
		"newline in desc":   {"N", func(c *Change) { c.Description = "a\nb" }},
		"bad type":          {"N", func(c *Change) { c.Type = "AWS" }},
		"bad field name":    {"N", func(c *Change) { c.Fields[0].Name = "Value" }},
		"bad env name":      {"N", func(c *Change) { c.Fields[0].Env = "A-B" }},
		"empty value, new":  {"N", func(c *Change) { c.Fields[0].Value = nil }},
		"oversize value":    {"N", func(c *Change) { c.Fields[0].Value = make([]byte, MaxValueSize+1) }},
		"duplicate field":   {"N", func(c *Change) { c.Fields = append(c.Fields, c.Fields[0]) }},
		"set and unset":     {"N", func(c *Change) { c.Unset = []string{"value"} }},
		"no fields":         {"N", func(c *Change) { c.Fields = nil }},
		"two fields, same env": {"N", func(c *Change) {
			c.Fields[0].Env = "E"
			c.Fields = append(c.Fields, FieldChange{Name: "b", Value: []byte("y"), Env: "E"})
		}},
	}
	for label, tc := range cases {
		c := Change{Description: "d", Type: "custom", Fields: []FieldChange{{Name: "value", Value: []byte(sentinel)}}}
		tc.edit(&c)
		err := v.Set(tc.name, c)
		if err == nil {
			t.Errorf("%s: accepted", label)
		} else if strings.Contains(err.Error(), sentinel) {
			t.Errorf("%s: the error contains a value", label)
		}
	}
	if snapshot(t, v) != snap {
		t.Error("a rejected Set changed the vault")
	}
}

func TestUnknownNameSuggestsCloseNamesWithoutValues(t *testing.T) {
	v := newVault(t)
	for _, n := range []string{"AWS_PROD", "AWS_STAGING", "DB_URL"} {
		setValue(t, v, n, sentinel)
	}
	_, err := v.Fields("AWS_PORD")
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.Name != "AWS_PORD" || len(nf.Suggestions) == 0 || nf.Suggestions[0] != "AWS_PROD" {
		t.Fatalf("Fields(AWS_PORD) = %v", err)
	}
	for _, s := range nf.Suggestions {
		if s == "DB_URL" {
			t.Error("an unrelated name was suggested")
		}
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Error("the error contains a value")
	}
	if err := v.Remove("NOPE_AT_ALL"); !isNotFound(err) {
		t.Errorf("Remove(unknown) = %v", err)
	}
	if err := v.Remove("DB_URL"); err != nil {
		t.Fatal(err)
	}
	if l, _ := v.List(); len(l) != 2 {
		t.Errorf("Remove deleted the wrong number of entries: %d left", len(l))
	}
}

func TestHome(t *testing.T) {
	for _, tc := range []struct {
		env  []string
		want string
		fail bool
	}{
		{[]string{"HOME=/h", "AGV_HOME=/x"}, "/x", false},
		{[]string{"HOME=/h", "AGV_HOME="}, "/h/.agent-vault", false},
		{[]string{"HOME=/h"}, "/h/.agent-vault", false},
		{[]string{"PATH=/bin"}, "", true},
	} {
		got, err := Home(tc.env)
		if (err != nil) != tc.fail || got != tc.want {
			t.Errorf("Home(%v) = %q, %v", tc.env, got, err)
		}
	}
}
