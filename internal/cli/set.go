package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sting8k/agent-vault/internal/audit"
	"github.com/sting8k/agent-vault/internal/preset"
	"github.com/sting8k/agent-vault/internal/vault"
)

const setUsage = `usage: agv set [NAME] [--type TYPE] [--desc TEXT] [--field FIELD=@PATH|-]... [--unset FIELD]... [--new-group]

Adds or updates a secret. Run it in your own terminal, not through an agent.

With no options it asks for everything, with hidden input for values. Anything you pass
as an option is not asked. In a prompt, "@path" reads the value from a file.
For an existing entry an empty answer keeps the current value; a field is removed only
with --unset.

  --type TYPE          one of: %s
  --desc TEXT          what the secret is for
  --field FIELD=@PATH  read a field's value from a file
  --field FIELD=-      read a field's value from stdin, minus one trailing newline (at most one field)
  --unset FIELD        remove a field of an existing entry
  --new-group          create the name's group even though it looks like a typo of an existing one

NAME may be grouped with "/": SHOP/PROD/DB is the secret DB in group SHOP/PROD/. The whole
string is the name, everywhere. Groups only organise: they are not stored and not scopes.
A group close to an existing one (SHPO/ when SHOP/ exists) is asked about first.

Values are never accepted on the command line.
Name, description, type, field names, env names and file flags are stored in plain text and
'agv list' shows them to agents: never put a secret in them.
Back up master.key and vault.json together (in AGV_HOME or ~/.agent-vault);
a lost key cannot be recovered.
`

const nonInteractiveForm = `  agv set NAME --type TYPE --desc TEXT --field FIELD=@PATH [--field FIELD=-] [--unset FIELD]
Values are never accepted on the command line: @PATH reads a file, - reads stdin (one field only).`

// session is one run of set: what the person can be asked and what has been asked.
type session struct {
	sys      IO
	t        terminal // nil: no terminal, nothing may be asked
	full     bool     // no change options were given: walk through everything
	prompted bool     // something was asked, so show a summary and confirm
}

func cmdSet(argv []string, sys IO) int {
	if err := runSet(argv, sys); err != nil {
		return fail(sys, err)
	}
	return 0
}

func runSet(argv []string, sys IO) error {
	a, err := parseArgs(argv, []string{"help", "new-group"}, []string{"type", "desc", "field", "unset"})
	if err != nil {
		return fmt.Errorf("set: %w (see 'agv set --help')", err)
	}
	if a.flags["help"] {
		fmt.Fprintf(sys.Stdout, setUsage, strings.Join(preset.Names(), ", "))
		return nil
	}
	if len(a.pos) > 1 {
		return errors.New("set takes at most one NAME")
	}
	typeOpt, hasType, err := a.one("type")
	if err != nil {
		return err
	}
	descOpt, hasDesc, err := a.one("desc")
	if err != nil {
		return err
	}
	given, err := readFieldFlags(a.vals["field"], sys)
	if err != nil {
		return err
	}
	unset := a.vals["unset"]

	tr, err := openTrail(sys)
	if err != nil {
		return err
	}
	v, err := vaultOf(sys)
	if err != nil {
		return err
	}
	all, err := v.List() // checks the key/vault state before the person types anything
	if err != nil {
		return err
	}
	s := &session{sys: sys, t: terminalOf(sys.Stdin, sys.Stderr)}
	s.full = !hasType && !hasDesc && len(given) == 0 && len(unset) == 0
	if s.full && s.t == nil {
		return s.need("what to store")
	}

	name, err := s.name(a.pos, all)
	if err != nil {
		return err
	}
	var cur *vault.EntryInfo
	for i := range all {
		if all[i].Name == name {
			cur = &all[i]
		}
	}
	if cur != nil {
		// Changing a stored entry can lose its only copy, so it needs the person at a terminal
		// and always asks before saving. Without a terminal (an agent), set only adds entries.
		if s.t == nil {
			return fmt.Errorf("%s already exists; changing it needs your confirmation: run 'agv set %s' in your own terminal", name, name)
		}
		s.prompted = true
	} else if !a.flags["new-group"] {
		if err := s.checkGroup(name, all); err != nil {
			return err
		}
	}
	d := &draft{name: name, cur: cur, unset: unset}
	if d.typ, err = s.entryType(typeOpt, hasType, cur); err != nil {
		return err
	}
	if d.desc, err = s.description(descOpt, hasDesc, cur); err != nil {
		return err
	}
	if err := s.planFields(d, given); err != nil {
		return err
	}
	s.warn(d)
	if s.prompted {
		if err := s.confirm(d); err != nil {
			return err
		}
	}

	// Everything is answered. Only now does vault take the lock and apply the patch.
	if err := v.Set(name, d.change()); err != nil {
		return err
	}
	d.report(sys.Stdout, len(all) == 0)
	tr.done(audit.ActionSet, name)
	return nil
}

// need is the error for a question that cannot be asked because there is no terminal.
func (s *session) need(what string) error {
	return fmt.Errorf("set needs a terminal to ask for %s. Without one, pass it as options:\n%s", what, nonInteractiveForm)
}

// line and secret ask the person. Ending input (Ctrl-D) cancels.
func (s *session) line(prompt string) (string, error) {
	s.prompted = true
	ans, err := s.t.line(prompt)
	if errors.Is(err, io.EOF) {
		return "", errCancelled
	}
	return ans, err
}

func (s *session) secret(prompt string) ([]byte, error) {
	s.prompted = true
	ans, err := s.t.secret(prompt)
	if errors.Is(err, io.EOF) {
		return nil, errCancelled
	}
	return ans, err
}

// askText asks until valid accepts the answer. An empty answer returns def when there is one.
func (s *session) askText(prompt, def string, valid func(string) error) (string, error) {
	for {
		ans, err := s.line(prompt)
		if err != nil {
			return "", err
		}
		if ans == "" && def != "" {
			return def, nil
		}
		if err := valid(ans); err != nil {
			fmt.Fprintf(s.sys.Stderr, "  %v\n", err)
			continue
		}
		return ans, nil
	}
}

func (s *session) name(pos []string, all []vault.EntryInfo) (string, error) {
	names := entryNames(all)
	valid := func(n string) error {
		err := vault.CheckName(n)
		if err == nil {
			return nil
		}
		// Only stored names are offered, so what was typed is never repeated.
		if close := vault.Suggest(n, names); len(close) > 0 {
			return fmt.Errorf("%w; closest: %s", err, strings.Join(close, ", "))
		}
		return err
	}
	if len(pos) == 1 {
		return pos[0], valid(pos[0])
	}
	if s.t == nil {
		return "", s.need("the name")
	}
	if groups := vault.Groups(names); len(groups) > 0 {
		for i := range groups {
			groups[i] += "/"
		}
		fmt.Fprintf(s.sys.Stderr, "Groups in use: %s\n", strings.Join(groups, ", "))
	}
	return s.askText("Name (A-Z, 0-9, _; group with /, as in SHOP/PROD/DB): ", "", valid)
}

func entryNames(all []vault.EntryInfo) []string {
	names := make([]string, len(all))
	for i, e := range all {
		names[i] = e.Name
	}
	return names
}

// checkGroup stops a new entry from silently opening a group that is a typo of an existing one
// (SHPO/PROD/DB beside SHOP/). A person at a terminal is asked. Without one nothing is created
// and the message names the way on: use the existing group, or repeat with --new-group.
func (s *session) checkGroup(name string, all []vault.EntryInfo) error {
	group, near := vault.NewGroupNear(name, entryNames(all))
	if len(near) == 0 {
		return nil
	}
	rest := name[len(group):] // starts with "/"
	var instead []string
	for _, n := range near[:min(len(near), 3)] {
		instead = append(instead, n+rest)
	}
	have := strings.Join(near, "/, ") + "/"
	if s.t == nil {
		return fmt.Errorf("%s/ would be a new group, and %s already exists. If you meant it, use %s. "+
			"If %s/ really is new, repeat with --new-group, or ask the user to run 'agv set %s' in a separate terminal",
			group, have, strings.Join(instead, " or "), group, name)
	}
	ok, err := yesNo(s.t, fmt.Sprintf("%s/ is a new group, and %s exists (did you mean %s?). Create %s in the new group? [y/N] ",
		group, have, strings.Join(instead, " or "), name), false)
	if err != nil {
		return err
	}
	if !ok {
		return errCancelled
	}
	return nil
}

func (s *session) entryType(opt string, has bool, cur *vault.EntryInfo) (string, error) {
	known := func(t string) error {
		if _, ok := preset.Lookup(t); !ok {
			return fmt.Errorf("unknown type; choose one of: %s", strings.Join(preset.Names(), ", "))
		}
		return nil
	}
	switch {
	case has:
		return opt, known(opt)
	case cur != nil && !s.full:
		return cur.Type, nil
	case s.t == nil:
		return "", s.need("the type")
	}
	def, prompt := "", "Type: "
	if cur != nil {
		def, prompt = cur.Type, fmt.Sprintf("Type [%s]: ", stripControl(cur.Type))
	}
	fmt.Fprintf(s.sys.Stderr, "Types: %s\n", strings.Join(preset.Names(), ", "))
	return s.askText(prompt, def, known)
}

func (s *session) description(opt string, has bool, cur *vault.EntryInfo) (string, error) {
	switch {
	case has:
		return opt, vault.CheckDescription(opt)
	case cur != nil && !s.full:
		return cur.Description, nil
	case s.t == nil:
		return "", s.need("the description")
	}
	def, prompt := "", "Description (agents can read it with 'agv list'; never put a secret here): "
	if cur != nil {
		def, prompt = cur.Description, fmt.Sprintf("Description (agents can read it; never put a secret here) [%s]: ", stripControl(cur.Description))
	}
	return s.askText(prompt, def, vault.CheckDescription)
}

// confirm shows what will be saved, never a value, and asks.
func (s *session) confirm(d *draft) error {
	w := s.sys.Stderr
	fmt.Fprintf(w, "\nSummary for %s\n  type         %s\n  description  %s\n", d.name, stripControl(d.typ), stripControl(d.desc))
	for _, t := range d.targets {
		switch {
		case t.value != nil:
			fmt.Fprintf(w, "  %-20s %d bytes%s\n", t.name, len(t.value), t.notes())
		case t.stored:
			fmt.Fprintf(w, "  %-20s unchanged%s\n", t.name, t.notes())
		}
	}
	if len(d.unset) > 0 {
		fmt.Fprintf(w, "  removing: %s\n", strings.Join(d.unset, ", "))
	}
	ok, err := yesNo(s.t, "Save? [Y/n] ", true)
	if err != nil {
		return err
	}
	if !ok {
		return errCancelled
	}
	return nil
}
