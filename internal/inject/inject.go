// Package inject turns placeholders and env flags into a runnable Plan.
// It does not import vault: cli adapts the vault to Source.
package inject

// Field is one decrypted field of an entry.
type Field struct {
	Value []byte
	Env   string // env var name, "" if none
	File  bool   // true: inject as a temp-file path
}

// Source looks up entries. cli implements it on top of the vault.
type Source interface {
	// Entry returns the fields of the named entry, keyed by field name.
	// For an unknown name it returns an error that names close matches (never values).
	Entry(name string) (map[string]Field, error)
}

// Secret is a value used by a run, labelled NAME.field for redaction.
type Secret struct {
	Label string
	Value []byte
}

// Plan is everything runner needs. All references are resolved and all temp
// files written before a Plan is returned.
type Plan struct {
	Argv    []string // argv[0] is the command, not substituted
	Env     []string // full child environment (inherited + injected)
	Secrets []Secret // every value used, for redaction
	Cleanup func() error
}
