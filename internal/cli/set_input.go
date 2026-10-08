package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sting8k/agent-vault/internal/vault"
)

// givenField is one --field FIELD=@PATH|- option with its value already read.
type givenField struct {
	name  string
	value []byte
}

// readFieldFlags parses the --field options and reads their values now, so that a bad file
// fails before anything is asked. Values are never accepted inline. Errors refer to an option by
// position and never repeat any part of it: a secret typed here by mistake must not be echoed.
func readFieldFlags(specs []string, sys IO) ([]givenField, error) {
	var out []givenField
	seen := map[string]bool{}
	stdinUsed := false
	for i, spec := range specs {
		opt := fmt.Sprintf("--field #%d", i+1)
		name, src, ok := strings.Cut(spec, "=")
		if !ok || vault.CheckField(name) != nil {
			return nil, fmt.Errorf("%s: expected FIELD=@PATH or FIELD=- (a field name is a-z, 0-9, _)", opt)
		}
		if src != "-" && !strings.HasPrefix(src, "@") {
			return nil, fmt.Errorf("%s: values are never accepted on the command line; use FIELD=@PATH to read a file or FIELD=- to read stdin", opt)
		}
		if seen[name] {
			return nil, fmt.Errorf("%s: this field was already given", opt)
		}
		seen[name] = true
		var value []byte
		var err error
		if src == "-" {
			if stdinUsed {
				return nil, fmt.Errorf("%s: only one field can read stdin", opt)
			}
			stdinUsed = true
			value, err = readValue(sys.Stdin, "stdin", true)
		} else {
			value, err = readPath(src[1:], sys.Env)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", opt, err)
		}
		out = append(out, givenField{name, value})
	}
	return out, nil
}

// readPath reads a value from a file. Errors never include the path: when a person typed a
// secret where a path was expected, the message must not repeat it.
func readPath(path string, env []string) ([]byte, error) {
	f, err := os.Open(expandHome(path, env))
	if err != nil {
		return nil, fmt.Errorf("cannot read the file: %s", reasonOf(err))
	}
	defer f.Close()
	return readValue(f, "the file", false)
}

// readValue reads a whole value, byte for byte (a PEM file legitimately ends in a newline).
// With dropEOL (stdin) exactly one trailing "\n" or "\r\n" is removed first: a pipe such as
// `gh auth token | agv set ...` ends its output with a newline that is not part of the value,
// the same convention as `docker login --password-stdin`. An empty value is an error because
// an empty answer means "keep the current value" elsewhere.
func readValue(r io.Reader, what string, dropEOL bool) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, vault.MaxValueSize+1))
	if rest, ok := bytes.CutSuffix(b, []byte("\n")); ok && dropEOL {
		b = bytes.TrimSuffix(rest, []byte("\r"))
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("cannot read %s: %s", what, reasonOf(err))
	case len(b) == 0:
		return nil, fmt.Errorf("%s is empty", what)
	case len(b) > vault.MaxValueSize:
		return nil, fmt.Errorf("%s is larger than %d bytes", what, vault.MaxValueSize)
	}
	return b, nil
}

// reasonOf is the system's reason without the path of an *fs.PathError.
func reasonOf(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}

// expandHome turns a leading ~ into $HOME from the environment slice.
func expandHome(path string, env []string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home := envOf(env, "HOME"); home != "" {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}
