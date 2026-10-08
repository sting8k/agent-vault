package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sting8k/agent-vault/internal/vault"
)

const listUsage = `usage: agv list [filter] [--json]

Lists the stored secrets: name, type, description, and each field with its env name
and file flag. It never shows a value. filter keeps entries whose name or description
contains it (ignoring case).
`

type listField struct {
	Name string `json:"name"`
	Env  string `json:"env"`
	File bool   `json:"file"`
}

type listEntry struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Fields      []listField `json:"fields"`
}

func cmdList(argv []string, sys IO) int {
	a, err := parseArgs(argv, []string{"json", "help"}, nil)
	if err != nil {
		return fail(sys, fmt.Errorf("list: %w (see 'agv list --help')", err))
	}
	if a.flags["help"] {
		fmt.Fprint(sys.Stdout, listUsage)
		return 0
	}
	if len(a.pos) > 1 {
		return fail(sys, fmt.Errorf("list takes at most one filter"))
	}
	v, err := vaultOf(sys)
	if err != nil {
		return fail(sys, err)
	}
	all, err := v.List()
	if err != nil {
		return fail(sys, err)
	}
	entries := listEntries(all, strings.Join(a.pos, ""))
	if a.flags["json"] {
		enc := json.NewEncoder(sys.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		enc.Encode(entries)
		return 0
	}
	printEntries(sys, entries, len(all))
	return 0
}

// listEntries applies the filter and strips control characters from every string.
func listEntries(all []vault.EntryInfo, filter string) []listEntry {
	filter = strings.ToLower(filter)
	out := []listEntry{} // not nil: --json prints [] for none
	for _, e := range all {
		e := listEntry{Name: stripControl(e.Name), Type: stripControl(e.Type), Description: stripControl(e.Description), Fields: fieldsOf(e)}
		if filter != "" && !strings.Contains(strings.ToLower(e.Name+"\n"+e.Description), filter) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func fieldsOf(e vault.EntryInfo) []listField {
	out := make([]listField, 0, len(e.Fields))
	for _, f := range e.Fields {
		out = append(out, listField{Name: stripControl(f.Name), Env: stripControl(f.Env), File: f.File})
	}
	return out
}

func printEntries(sys IO, entries []listEntry, stored int) {
	switch {
	case stored == 0:
		fmt.Fprintln(sys.Stdout, "No secrets are stored. Ask the user to run `agv set NAME` in a separate terminal.")
		return
	case len(entries) == 0:
		fmt.Fprintln(sys.Stdout, "No secret matches that filter. Run `agv list` to see all of them.")
		return
	}
	for _, e := range entries {
		fmt.Fprintf(sys.Stdout, "%s (%s): %s\n", e.Name, e.Type, e.Description)
		width := 0
		for _, f := range e.Fields {
			width = max(width, len(f.Name))
		}
		for _, f := range e.Fields {
			var notes []string
			if f.Env != "" {
				notes = append(notes, "env "+f.Env)
			}
			if f.File {
				notes = append(notes, "file")
			}
			if len(notes) == 0 {
				fmt.Fprintf(sys.Stdout, "  %s\n", f.Name)
				continue
			}
			fmt.Fprintf(sys.Stdout, "  %-*s  %s\n", width, f.Name, strings.Join(notes, ", "))
		}
	}
}
