package cli

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sting8k/agent-vault/internal/preset"
	"github.com/sting8k/agent-vault/internal/vault"
)

// target is one field of the entry being saved.
type target struct {
	name     string
	env      string
	file     bool
	optional bool   // a preset field that may be left out
	stored   bool   // the entry already has it
	value    []byte // new value; nil keeps the stored one, or skips an unstored field
}

// draft is the entry being built by set, from options, prompts and the stored entry.
type draft struct {
	name, typ, desc string
	cur             *vault.EntryInfo // nil: a new entry
	targets         []*target
	unset           []string
}

func (t *target) notes() string {
	var n []string
	if t.env != "" {
		n = append(n, "env "+t.env)
	}
	if t.file {
		n = append(n, "file")
	}
	if len(n) == 0 {
		return ""
	}
	return "  (" + strings.Join(n, ", ") + ")"
}

// planFields decides the fields of the entry:
//   - the type's preset fields plus every stored field, minus --unset ones, so a field is
//     removed only by --unset and a changed type keeps fields with matching names;
//   - a stored field keeps its env name and file flag unless the type changed and the new
//     preset defines it; presets only fill these in when a field is first set;
//   - a custom type (or a stored type this version does not know) takes any field name.
//
// Values come from --field, else from prompts, else are kept. A preset field that is
// required, not stored and not given must be asked for; without a terminal that is an error.
func (s *session) planFields(d *draft, given []givenField) error {
	p, known := preset.Lookup(d.typ)
	freeform := !known || p.Name == preset.Custom
	typeChanged := d.cur == nil || d.cur.Type != d.typ

	stored := map[string]vault.FieldInfo{}
	if d.cur != nil {
		for _, f := range d.cur.Fields {
			stored[f.Name] = f
		}
	}
	for _, u := range d.unset {
		if _, ok := stored[u]; !ok {
			return fmt.Errorf("--unset names a field that %s does not have", d.name)
		}
	}
	presetField := map[string]preset.Field{}
	var names []string
	add := func(n string) {
		if !slices.Contains(d.unset, n) && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	for _, pf := range p.Fields {
		presetField[pf.Name] = pf
		add(pf.Name)
	}
	if d.cur != nil {
		for _, f := range d.cur.Fields { // sorted by name
			add(f.Name)
		}
	}
	values := map[string][]byte{}
	for _, g := range given {
		_, inStored := stored[g.name]
		_, inPreset := presetField[g.name]
		switch {
		case slices.Contains(d.unset, g.name):
			return fmt.Errorf("a field cannot be both set and unset")
		case !freeform && !inStored && !inPreset:
			return fmt.Errorf("type %s has no such field; its fields are: %s", d.typ, fieldNames(p))
		}
		values[g.name] = g.value
		add(g.name)
	}

	for _, n := range names {
		sf, isStored := stored[n]
		pf, inPreset := presetField[n]
		t := &target{name: n, stored: isStored, optional: inPreset && pf.Optional, value: values[n]}
		if isStored && !(typeChanged && inPreset) {
			t.env, t.file = sf.Env, sf.File
		} else if inPreset {
			t.env, t.file = pf.Env, pf.File
		}
		d.targets = append(d.targets, t)
	}

	for _, t := range d.targets {
		if t.value != nil {
			continue
		}
		switch {
		case s.full:
			if err := s.fieldValue(d.name, t); err != nil {
				return err
			}
		case !t.stored && !t.optional:
			if s.t == nil {
				return s.need("the value of field " + t.name)
			}
			if err := s.fieldValue(d.name, t); err != nil {
				return err
			}
		}
	}
	if s.full && freeform {
		if err := s.extraFields(d); err != nil {
			return err
		}
	}
	if freeform && len(d.targets) == 0 {
		return s.need("at least one field")
	}
	return nil
}

func fieldNames(p preset.Preset) string {
	var n []string
	for _, f := range p.Fields {
		n = append(n, f.Name)
	}
	return strings.Join(n, ", ")
}

// extraFields lets the person add fields to a custom entry: name, env name, file flag, value.
func (s *session) extraFields(d *draft) error {
	valid := func(n string) error {
		if n == "" {
			return nil
		}
		if err := vault.CheckField(n); err != nil {
			return err
		}
		for _, t := range d.targets {
			if t.name == n {
				return fmt.Errorf("%s is already a field", n)
			}
		}
		return nil
	}
	for {
		n, err := s.askText("Add a field? Field name (Enter to finish): ", "", valid)
		if err != nil {
			return err
		}
		if n == "" {
			if len(d.targets) == 0 {
				fmt.Fprintln(s.sys.Stderr, "  an entry needs at least one field")
				continue
			}
			return nil
		}
		env, err := s.askText(fmt.Sprintf("Env var name for %s.%s (Enter for none): ", d.name, n), "", func(e string) error {
			if err := vault.CheckEnv(e); err != nil {
				return err
			}
			for _, t := range d.targets {
				if e != "" && t.env == e {
					return fmt.Errorf("%s already uses that env name", t.name)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		ans, err := s.line("Is the value a file (injected as a path to a temp file)? [y/N] ")
		if err != nil {
			return err
		}
		t := &target{name: n, env: env, file: strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "y")}
		if err := s.fieldValue(d.name, t); err != nil {
			return err
		}
		d.targets = append(d.targets, t)
	}
}

// fieldValue asks for one field's value until it gets a usable answer. An empty answer keeps
// a stored value or skips an optional field; it is not accepted for a required one.
// Hidden input is a value, or @path to read a file; @@ starts a value that begins with @.
func (s *session) fieldValue(entry string, t *target) error {
	hint := ""
	switch {
	case t.stored:
		hint = "; Enter keeps the current value"
	case t.optional:
		hint = "; optional, Enter skips"
	}
	for {
		var path string
		var literal []byte
		if t.file {
			ans, err := s.line(fmt.Sprintf("File path for %s.%s (a file%s): ", entry, t.name, hint))
			if err != nil {
				return err
			}
			path = strings.TrimSpace(ans)
		} else {
			ans, err := s.secret(fmt.Sprintf("Value for %s.%s (hidden; @path reads a file%s): ", entry, t.name, hint))
			if err != nil {
				return err
			}
			switch {
			case bytes.HasPrefix(ans, []byte("@@")):
				literal = ans[1:]
			case bytes.HasPrefix(ans, []byte("@")):
				path = strings.TrimSpace(string(ans[1:]))
			default:
				literal = ans
			}
		}
		switch {
		case path != "":
			b, err := readPath(path, s.sys.Env)
			if err != nil {
				fmt.Fprintf(s.sys.Stderr, "  %v\n", err)
				continue
			}
			t.value = b
		case len(literal) > vault.MaxValueSize:
			fmt.Fprintf(s.sys.Stderr, "  the value is larger than %d bytes; use @path\n", vault.MaxValueSize)
			continue
		case len(literal) > 0:
			t.value = literal
		case t.stored || t.optional:
			return nil
		default:
			fmt.Fprintln(s.sys.Stderr, "  this field is required")
			continue
		}
		return nil
	}
}

// warn reports values that are likely mistakes. They are still saved, exactly as given.
func (s *session) warn(d *draft) {
	for _, t := range d.targets {
		if t.value == nil {
			continue
		}
		if len(t.value) < 6 {
			fmt.Fprintf(s.sys.Stderr, "warning: %s.%s is only %d bytes; a short value makes redaction mask unrelated text\n", d.name, t.name, len(t.value))
		}
		if !t.file && len(bytes.TrimRight(t.value, " \t\r\n\v\f")) != len(t.value) {
			fmt.Fprintf(s.sys.Stderr, "warning: %s.%s ends with whitespace or a newline; it is kept exactly as given\n", d.name, t.name)
		}
	}
}

// change is the patch for vault.Set: every field that is given, kept or newly added.
func (d *draft) change() vault.Change {
	c := vault.Change{Description: d.desc, Type: d.typ, Unset: d.unset}
	for _, t := range d.targets {
		if t.value == nil && !t.stored {
			continue // an optional field that was skipped
		}
		c.Fields = append(c.Fields, vault.FieldChange{Name: t.name, Value: t.value, Env: t.env, File: t.file})
	}
	return c
}

// report says what was saved and how an agent would use it. It names fields, never values.
func (d *draft) report(w io.Writer, first bool) {
	c := d.change()
	fmt.Fprintf(w, "Saved %s (%s).\n", d.name, d.typ)
	if len(d.unset) > 0 {
		fmt.Fprintf(w, "Removed fields: %s.\n", strings.Join(d.unset, ", "))
	}
	if len(c.Fields) == 0 {
		return
	}
	fmt.Fprintln(w, "An agent can use it with:")
	for _, f := range c.Fields {
		if f.Env != "" {
			fmt.Fprintf(w, "  agv run --env-from %s -- COMMAND\n", d.name)
			break
		}
	}
	f := c.Fields[0]
	ref := d.name
	if len(c.Fields) > 1 || f.Name != "value" {
		ref += "." + f.Name
	}
	if f.File {
		ref = "file:" + ref
	}
	fmt.Fprintf(w, "  agv run -- COMMAND '{{%s}}'\n", ref)
	if first {
		fmt.Fprintln(w, "Back up master.key and vault.json together; a lost key cannot be recovered.")
	}
}
