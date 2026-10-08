// Package preset holds the built-in preset table (docs/design.md, Adding secrets).
// A preset only fills in field names, env names and file flags when `set` creates an entry;
// changing it later does not change stored entries.
package preset

// Custom is the preset with no fields: the user defines them.
const Custom = "custom"

// Field is one field a preset creates.
type Field struct {
	Name     string
	Env      string // "" if none
	File     bool   // the value is a file's content, injected as a temp-file path
	Optional bool   // set may leave it out
}

// Preset is a named list of fields.
type Preset struct {
	Name   string
	Fields []Field
}

// table is in the order of the design doc; Names keeps that order for prompts.
var table = []Preset{
	{Name: "api-token", Fields: []Field{{Name: "value"}}},
	{Name: "basic", Fields: []Field{{Name: "username"}, {Name: "password"}}},
	{Name: "db-url", Fields: []Field{{Name: "url", Env: "DATABASE_URL"}}},
	{Name: "aws", Fields: []Field{
		{Name: "access_key_id", Env: "AWS_ACCESS_KEY_ID"},
		{Name: "secret_access_key", Env: "AWS_SECRET_ACCESS_KEY"},
		{Name: "session_token", Env: "AWS_SESSION_TOKEN", Optional: true},
		{Name: "region", Env: "AWS_REGION"},
	}},
	{Name: "azure-sp", Fields: []Field{
		{Name: "tenant_id", Env: "AZURE_TENANT_ID"},
		{Name: "client_id", Env: "AZURE_CLIENT_ID"},
		{Name: "client_secret", Env: "AZURE_CLIENT_SECRET"},
	}},
	{Name: "gcp-sa", Fields: []Field{{Name: "key", Env: "GOOGLE_APPLICATION_CREDENTIALS", File: true}}},
	{Name: "ssh-key", Fields: []Field{{Name: "key", File: true}}},
	{Name: Custom},
}

// Names lists the preset names in table order, Custom last.
func Names() []string {
	out := make([]string, len(table))
	for i, p := range table {
		out[i] = p.Name
	}
	return out
}

// Lookup returns the named preset. The result is a copy: callers may change it.
func Lookup(name string) (Preset, bool) {
	for _, p := range table {
		if p.Name == name {
			p.Fields = append([]Field(nil), p.Fields...)
			return p, true
		}
	}
	return Preset{}, false
}
