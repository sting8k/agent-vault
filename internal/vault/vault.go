// Package vault stores agv secrets: entries and validation, AES-256-GCM per field, the master
// key lifecycle, writer locking and atomic saves. See docs/design.md (Data model, Storage).
//
// Nothing here prints or logs a value, and no error message contains one.
package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxValueSize is the largest field value. It matches the cap the redactor applies to a run.
const MaxValueSize = 1 << 20

// Vault is a handle on one vault directory. It holds no state: every call reads the disk.
type Vault struct{ dir string }

// Home returns the vault directory: AGV_HOME from env (os.Environ form), else $HOME/.agent-vault.
func Home(env []string) (string, error) {
	if d := getenv(env, "AGV_HOME"); d != "" {
		return d, nil
	}
	if h := getenv(env, "HOME"); h != "" {
		return filepath.Join(h, ".agent-vault"), nil
	}
	return "", fmt.Errorf("cannot find the vault directory: set AGV_HOME or HOME")
}

func getenv(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Open returns a handle on the vault directory chosen by Home. It touches no files.
func Open(env []string) (*Vault, error) {
	dir, err := Home(env)
	if err != nil {
		return nil, err
	}
	return &Vault{dir: dir}, nil
}

// Dir is the vault directory.
func (v *Vault) Dir() string { return v.dir }

// FieldInfo is the plaintext metadata of one field.
type FieldInfo struct {
	Name string
	Env  string // "" if none
	File bool
}

// EntryInfo is the plaintext metadata of one entry: safe to show to agents. Fields are sorted by name.
type EntryInfo struct {
	Name        string
	Description string
	Type        string
	Fields      []FieldInfo
}

// Field is one decrypted field. It has the same layout as inject.Field.
type Field struct {
	Value []byte
	Env   string
	File  bool
}

// List returns the metadata of all entries, sorted by name.
func (v *Vault) List() ([]EntryInfo, error) {
	f, _, err := v.load(false)
	if err != nil {
		return nil, err
	}
	out := make([]EntryInfo, 0, len(f.Entries))
	for name, e := range f.Entries {
		out = append(out, info(name, e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Entry returns the metadata of one entry, or a *NotFoundError.
func (v *Vault) Entry(name string) (EntryInfo, error) {
	f, _, err := v.load(false)
	if err != nil {
		return EntryInfo{}, err
	}
	e, ok := f.Entries[name]
	if !ok {
		return EntryInfo{}, notFound(name, f)
	}
	return info(name, e), nil
}

// Fields decrypts the fields of one entry, keyed by field name, or returns a *NotFoundError.
func (v *Vault) Fields(name string) (map[string]Field, error) {
	f, key, err := v.load(false)
	if err != nil {
		return nil, err
	}
	e, ok := f.Entries[name]
	if !ok {
		return nil, notFound(name, f)
	}
	out := make(map[string]Field, len(e.Fields))
	for fname, fl := range e.Fields {
		plain, err := open(key, name, fname, fl.Value)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", name, fname, err)
		}
		out[fname] = Field{Value: plain, Env: fl.Env, File: fl.File}
	}
	return out, nil
}

// Change patches one entry. Set creates the entry if it does not exist.
type Change struct {
	Description string
	Type        string // preset name or "custom"; the vault checks only its shape
	Fields      []FieldChange
	Unset       []string // fields to remove
}

// FieldChange writes or keeps one field. Other stored fields are left as they are.
type FieldChange struct {
	Name  string
	Value []byte // empty: keep the stored value, which must exist
	Env   string
	File  bool
}

// Set applies c to the entry name under the writer lock, so it patches the entry as it is
// now, not as the caller saw it earlier. Call it after all prompts are answered.
func (v *Vault) Set(name string, c Change) error {
	if err := validateChange(name, c); err != nil {
		return err
	}
	return v.update(func(f *file, key []byte) error {
		now := time.Now().UTC().Truncate(time.Second)
		e := f.Entries[name]
		if e == nil {
			e = &entry{CreatedAt: now, Fields: map[string]*field{}}
			f.Entries[name] = e
		}
		e.Description, e.Type, e.UpdatedAt = c.Description, c.Type, now
		for _, u := range c.Unset {
			delete(e.Fields, u)
		}
		for _, fc := range c.Fields {
			nf := &field{Env: fc.Env, File: fc.File}
			if len(fc.Value) > 0 {
				var err error
				if nf.Value, err = seal(key, name, fc.Name, fc.Value); err != nil {
					return fmt.Errorf("cannot encrypt %s.%s: %w", name, fc.Name, err)
				}
			} else if cur := e.Fields[fc.Name]; cur != nil {
				nf.Value = cur.Value
			} else {
				return fmt.Errorf("%s.%s has no value", name, fc.Name)
			}
			e.Fields[fc.Name] = nf
		}
		return checkFinal(name, e)
	})
}

// checkFinal checks the entry as it will be stored.
func checkFinal(name string, e *entry) error {
	if len(e.Fields) == 0 {
		return fmt.Errorf("%s would have no fields; remove the entry with rm instead", name)
	}
	envOwner := map[string]string{}
	for _, fname := range sortedFields(e) {
		env := e.Fields[fname].Env
		if env == "" {
			continue
		}
		if other, dup := envOwner[env]; dup {
			return fmt.Errorf("%s: fields %s and %s both use env %s", name, other, fname, env)
		}
		envOwner[env] = fname
	}
	return nil
}

// Remove deletes the entry name, or returns a *NotFoundError. A missing vault is not created.
func (v *Vault) Remove(name string) error {
	f, _, err := v.load(false)
	if err != nil {
		return err
	}
	if _, ok := f.Entries[name]; !ok {
		return notFound(name, f)
	}
	return v.update(func(f *file, _ []byte) error {
		if _, ok := f.Entries[name]; !ok {
			return notFound(name, f)
		}
		delete(f.Entries, name)
		return nil
	})
}

// update runs one locked read-modify-write: the directory exists, the exclusive lock is held,
// the state table is checked (making the key and vault on first use), fn edits, the result is saved.
func (v *Vault) update(fn func(f *file, key []byte) error) error {
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", v.dir, err)
	}
	unlock, err := lockDir(v.dir)
	if err != nil {
		return fmt.Errorf("cannot lock %s: %w", v.path(lockFile), err)
	}
	defer unlock()
	f, key, err := v.load(true)
	if err != nil {
		return err
	}
	if err := fn(f, key); err != nil {
		return err
	}
	return v.save(f)
}

func info(name string, e *entry) EntryInfo {
	out := EntryInfo{Name: name, Description: e.Description, Type: e.Type}
	for _, fname := range sortedFields(e) {
		fl := e.Fields[fname]
		out.Fields = append(out.Fields, FieldInfo{Name: fname, Env: fl.Env, File: fl.File})
	}
	return out
}

func sortedFields(e *entry) []string {
	names := make([]string, 0, len(e.Fields))
	for n := range e.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
