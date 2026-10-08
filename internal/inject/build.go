package inject

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Request describes one run.
type Request struct {
	Argv    []string // CMD ARGS... after "--"; only ARGS may hold placeholders
	Env     []string // --env values, each VAR=TEMPLATE
	EnvFrom []string // --env-from entry names
	Environ []string // inherited environment (os.Environ form); also locates the temp root
}

var (
	placeholder = regexp.MustCompile(`\{\{(file:)?([A-Z][A-Z0-9_]*)(?:\.([a-z][a-z0-9_]*))?\}\}`)
	entryName   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	fieldName   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Build resolves every reference of req against src and writes the temp files
// that file references need. It returns only when everything is resolved; on
// error, files already written are removed and nothing is left behind.
//
// Error messages are built from the request and NAME.field labels only, never
// from values. One Plan may be live per process: its temp directory is named
// after the pid.
func Build(req Request, src Source) (plan *Plan, err error) {
	if len(req.Argv) == 0 || req.Argv[0] == "" {
		return nil, errors.New("no command given")
	}
	if placeholder.MatchString(req.Argv[0]) {
		return nil, errors.New("placeholders are not allowed in the command itself")
	}
	b := &builder{src: src, environ: req.Environ, entries: map[string]map[string]Field{}, used: map[string]bool{}, files: map[string]string{}}
	defer func() {
		if err != nil {
			b.cleanup()
		}
	}()
	b.sweepStale()

	argv := make([]string, len(req.Argv))
	argv[0] = req.Argv[0]
	for i, a := range req.Argv[1:] {
		if argv[i+1], err = b.expand(a); err != nil {
			return nil, err
		}
	}
	set, err := b.envSettings(req)
	if err != nil {
		return nil, err
	}
	return &Plan{Argv: argv, Env: mergeEnv(req.Environ, set), Secrets: b.secrets, Cleanup: b.cleanup}, nil
}

type setting struct{ name, value string }

// envSettings returns the variables set by --env and --env-from, in flag order.
func (b *builder) envSettings(req Request) ([]setting, error) {
	var set []setting
	seen := map[string]bool{}
	add := func(name, value string) error {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("invalid environment variable name %q", name)
		}
		if seen[name] {
			return fmt.Errorf("environment variable %s is set by more than one flag", name)
		}
		seen[name] = true
		set = append(set, setting{name, value})
		return nil
	}
	for _, e := range req.Env {
		name, tmpl, ok := strings.Cut(e, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--env needs VAR=TEMPLATE, got %q", e)
		}
		v, err := b.expand(tmpl)
		if err != nil {
			return nil, err
		}
		if err := add(name, v); err != nil {
			return nil, err
		}
	}
	for _, n := range req.EnvFrom {
		if !entryName.MatchString(n) {
			return nil, fmt.Errorf("--env-from: %q is not a secret name", n)
		}
		entry, err := b.entry(n)
		if err != nil {
			return nil, err
		}
		count := 0
		for _, fname := range sortedKeys(entry) {
			f := entry[fname]
			if f.Env == "" {
				continue
			}
			count++
			v, err := b.render(n, fname, f.File)
			if err != nil {
				return nil, err
			}
			if err := add(f.Env, v); err != nil {
				return nil, err
			}
		}
		if count == 0 {
			return nil, noEnvFields(n, entry)
		}
	}
	return set, nil
}

// noEnvFields is the error for --env-from on an entry without env names. It
// points at --env, using a real field name (plaintext metadata, never a value)
// and the file form for a file field.
func noEnvFields(name string, entry map[string]Field) error {
	field, ref := "field", ""
	if keys := sortedKeys(entry); len(keys) > 0 {
		field = keys[0]
		if entry[field].File {
			ref = "file:"
		}
	}
	return fmt.Errorf("--env-from: %s has no field with an env name; use --env VAR='{{%s%s.%s}}' instead", name, ref, name, field)
}

// mergeEnv drops inherited variables that are overridden and appends the injected ones.
func mergeEnv(environ []string, set []setting) []string {
	over := make(map[string]bool, len(set))
	for _, s := range set {
		over[s.name] = true
	}
	env := make([]string, 0, len(environ)+len(set))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !over[name] {
			env = append(env, kv)
		}
	}
	for _, s := range set {
		env = append(env, s.name+"="+s.value)
	}
	return env
}

type builder struct {
	src     Source
	environ []string
	entries map[string]map[string]Field
	used    map[string]bool // labels already in secrets
	secrets []Secret
	files   map[string]string // label -> temp file path
	dir     string            // per-run temp directory, "" until a file is needed
}

// expand substitutes the placeholders of one template in a single pass:
// substituted text is never scanned again.
func (b *builder) expand(tmpl string) (string, error) {
	var out strings.Builder
	last := 0
	for _, m := range placeholder.FindAllStringSubmatchIndex(tmpl, -1) {
		out.WriteString(tmpl[last:m[0]])
		name, field := tmpl[m[4]:m[5]], "value"
		if m[6] >= 0 {
			field = tmpl[m[6]:m[7]]
		}
		s, err := b.render(name, field, m[2] >= 0)
		if err != nil {
			return "", err
		}
		out.WriteString(s)
		last = m[1]
	}
	out.WriteString(tmpl[last:])
	return out.String(), nil
}

// render returns what replaces one reference: the value, or the path of a temp file holding it.
func (b *builder) render(name, field string, asFile bool) (string, error) {
	entry, err := b.entry(name)
	if err != nil {
		return "", err
	}
	label := name + "." + field
	f, ok := entry[field]
	if !ok {
		return "", fmt.Errorf("%s: no such field (fields: %s)", label, strings.Join(sortedKeys(entry), ", "))
	}
	if !b.used[label] {
		b.used[label] = true
		b.secrets = append(b.secrets, Secret{Label: label, Value: f.Value})
	}
	if asFile {
		return b.file(label, f.Value)
	}
	if bytes.IndexByte(f.Value, 0) >= 0 {
		return "", fmt.Errorf("%s contains a NUL byte and cannot be used in an argument or environment variable", label)
	}
	return string(f.Value), nil
}

func (b *builder) entry(name string) (map[string]Field, error) {
	if e, ok := b.entries[name]; ok {
		return e, nil
	}
	e, err := b.src.Entry(name)
	if err != nil {
		return nil, err
	}
	b.entries[name] = e
	return e, nil
}

func sortedKeys(m map[string]Field) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
