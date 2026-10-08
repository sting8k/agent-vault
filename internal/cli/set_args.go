package cli

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/sting8k/agent-vault/internal/vault"
)

// args is the result of parseArgs.
type args struct {
	pos   []string
	flags map[string]bool
	vals  map[string][]string
}

// parseArgs parses the options of set, list and rm: bools take no value, strs take one and may
// repeat. Unlike package flag it accepts options after positional arguments, and its errors
// never repeat any argument: a secret mistyped on the command line must not be echoed (G-7n9t).
// -h is --help.
func parseArgs(argv []string, bools, strs []string) (*args, error) {
	a := &args{flags: map[string]bool{}, vals: map[string][]string{}}
	known := func(list []string, name string) bool {
		for _, n := range list {
			if n == name {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			a.pos = append(a.pos, argv[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' { // "-" alone is a positional argument
			a.pos = append(a.pos, arg)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if name == "h" {
			name = "help"
		}
		switch {
		case known(bools, name):
			if hasVal {
				return nil, fmt.Errorf("option --%s takes no value", name)
			}
			a.flags[name] = true
		case known(strs, name):
			if !hasVal {
				if i+1 >= len(argv) {
					return nil, fmt.Errorf("option --%s needs a value", name)
				}
				i++
				val = argv[i]
			}
			a.vals[name] = append(a.vals[name], val)
		default:
			return nil, errors.New("unknown option")
		}
	}
	return a, nil
}

// one returns the single value of a string option, and whether it was given.
func (a *args) one(name string) (string, bool, error) {
	switch vs := a.vals[name]; len(vs) {
	case 0:
		return "", false, nil
	case 1:
		return vs[0], true, nil
	}
	return "", true, fmt.Errorf("option --%s was given more than once", name)
}

// stripControl removes control characters, so stored text cannot move the cursor, recolor
// output or hide lines when an agent or a person reads it.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// vaultOf opens the vault chosen by the environment slice (AGV_HOME, else $HOME/.agent-vault).
func vaultOf(sys IO) (*vault.Vault, error) { return vault.Open(sys.Env) }

// envOf returns the first value of key in an os.Environ-style slice.
func envOf(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
