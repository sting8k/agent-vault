package vault

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	nameRE  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	fieldRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	envRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	typeRE  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// CheckName reports whether s is a valid entry name.
func CheckName(s string) error {
	if !nameRE.MatchString(s) {
		return errors.New("a name must start with A-Z and use only A-Z, 0-9 and _")
	}
	return nil
}

// CheckField reports whether s is a valid field name.
func CheckField(s string) error {
	if !fieldRE.MatchString(s) {
		return errors.New("a field name must start with a-z and use only a-z, 0-9 and _")
	}
	return nil
}

// CheckEnv reports whether s is a valid environment variable name. "" means none.
func CheckEnv(s string) error {
	if s != "" && !envRE.MatchString(s) {
		return errors.New("an env name must start with a letter or _ and use only letters, digits and _")
	}
	return nil
}

// CheckDescription reports whether s is a usable description: required, one line.
func CheckDescription(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("a description is required")
	}
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return errors.New("a description must be one line without control characters")
	}
	return nil
}

func checkType(s string) error {
	if !typeRE.MatchString(s) {
		return errors.New("a type must start with a-z and use only a-z, 0-9 and -")
	}
	return nil
}

// validateChange checks everything about c that does not depend on the stored entry.
// Errors name entries and fields, never values.
func validateChange(name string, c Change) error {
	if err := CheckName(name); err != nil {
		return err
	}
	if err := CheckDescription(c.Description); err != nil {
		return err
	}
	if err := checkType(c.Type); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, f := range c.Fields {
		if err := CheckField(f.Name); err != nil {
			return fmt.Errorf("field %q: %w", f.Name, err)
		}
		if err := CheckEnv(f.Env); err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		if seen[f.Name] {
			return fmt.Errorf("field %s is given twice", f.Name)
		}
		seen[f.Name] = true
		if len(f.Value) > MaxValueSize {
			return fmt.Errorf("field %s: value is larger than %d bytes", f.Name, MaxValueSize)
		}
	}
	for _, u := range c.Unset {
		if seen[u] {
			return fmt.Errorf("field %s is both set and unset", u)
		}
	}
	return nil
}

// validate checks a parsed vault.json. A hand-edited file must not smuggle odd names into output.
func (f *file) validate() error {
	if f.Version != fileVersion {
		return fmt.Errorf("unsupported version %d", f.Version)
	}
	if f.KeyID == "" {
		return errors.New("key_id is missing")
	}
	if f.Entries == nil {
		f.Entries = map[string]*entry{}
	}
	for name, e := range f.Entries {
		if CheckName(name) != nil || e == nil {
			return fmt.Errorf("entry %q is invalid", name)
		}
		if len(e.Fields) == 0 {
			return fmt.Errorf("entry %s has no fields", name)
		}
		for fname, fl := range e.Fields {
			if CheckField(fname) != nil || fl == nil || !strings.HasPrefix(fl.Value, valuePrefix) {
				return fmt.Errorf("field %q of entry %s is invalid", fname, name)
			}
		}
	}
	return nil
}
