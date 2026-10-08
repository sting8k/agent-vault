package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const rmUsage = `usage: agv rm NAME [--yes]

Removes a secret. Run it in your own terminal: it asks y/N first, unless --yes is given.
`

func cmdRm(argv []string, sys IO) int {
	a, err := parseArgs(argv, []string{"yes", "help"}, nil)
	if err != nil {
		return fail(sys, fmt.Errorf("rm: %w (see 'agv rm --help')", err))
	}
	if a.flags["help"] {
		fmt.Fprint(sys.Stdout, rmUsage)
		return 0
	}
	if len(a.pos) != 1 {
		return fail(sys, errors.New("rm takes exactly one NAME"))
	}
	name := a.pos[0]
	v, err := vaultOf(sys)
	if err != nil {
		return fail(sys, err)
	}
	e, err := v.Entry(name) // an unknown name fails here, before any question
	if err != nil {
		return fail(sys, err)
	}
	if !a.flags["yes"] {
		t := terminalOf(sys.Stdin, sys.Stderr)
		if t == nil {
			return fail(sys, errors.New("rm asks for confirmation and needs a terminal; to remove without asking run: agv rm NAME --yes"))
		}
		prompt := fmt.Sprintf("Remove %s (%s) with %d field(s)? [y/N] ", stripControl(e.Name), stripControl(e.Description), len(e.Fields))
		ok, err := yesNo(t, prompt, false)
		if err != nil {
			return fail(sys, err)
		}
		if !ok {
			return fail(sys, errCancelled)
		}
	}
	if err := v.Remove(name); err != nil {
		return fail(sys, err)
	}
	fmt.Fprintf(sys.Stdout, "Removed %s.\n", name)
	return 0
}

// yesNo asks a y/N question; Enter answers def, and anything but y or yes is no.
func yesNo(t terminal, prompt string, def bool) (bool, error) {
	ans, err := t.line(prompt)
	if errors.Is(err, io.EOF) {
		return false, errCancelled
	}
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
