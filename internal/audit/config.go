// Package audit keeps agv's audit log and sends webhook notifications. Both are set up by
// $AGV_HOME/config.json. A Record holds secret names and the program only, never a value and never
// arguments. See docs/design.md, "Audit log and webhooks".
package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ConfigFile is the name of the settings file inside the vault directory.
const ConfigFile = "config.json"

const defaultRetention = 7 * 24 * time.Hour

// Config is the parsed config.json. A missing file gives the defaults.
type Config struct {
	Enabled   bool // write the audit log
	Retention time.Duration
	Webhooks  []Webhook
}

// Webhook is one notification target. Empty Actions or Secrets match everything.
type Webhook struct {
	URL     string   `json:"url"`     // literal, or a template such as {{NTFY_TOPIC}} resolved from the vault
	Format  string   `json:"format"`  // "json" (default) or "ntfy"
	Actions []string `json:"actions"` // run, set, rm
	Secrets []string `json:"secrets"` // NAME matches NAME and NAME.field; NAME.field matches that field
}

// Actions that are logged.
const (
	ActionRun = "run"
	ActionSet = "set"
	ActionRm  = "rm"
)

type configFile struct {
	Audit struct {
		Enabled   *bool  `json:"enabled"`
		Retention string `json:"retention"`
	} `json:"audit"`
	Webhooks []Webhook `json:"webhooks"`
}

var secretRef = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(\.[a-z][a-z0-9_]*)?$`)

// LoadConfig reads dir/config.json. A missing file is not an error; an invalid one is, and the
// message names the file. Unknown keys are rejected so a typo cannot silently turn a setting off.
// Nothing in an error repeats a URL, which may itself be a secret.
func LoadConfig(dir string) (Config, error) {
	path := filepath.Join(dir, ConfigFile)
	cfg := Config{Enabled: true, Retention: defaultRetention}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var f configFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return cfg, fmt.Errorf("%s is not valid (%v); fix it or remove it", path, err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return cfg, fmt.Errorf("%s is not valid (more than one JSON value); fix it or remove it", path)
	}
	if err := f.apply(&cfg); err != nil {
		return cfg, fmt.Errorf("%s is not valid (%v); fix it or remove it", path, err)
	}
	return cfg, nil
}

func (f configFile) apply(cfg *Config) error {
	if f.Audit.Enabled != nil {
		cfg.Enabled = *f.Audit.Enabled
	}
	if f.Audit.Retention != "" {
		d, err := parseRetention(f.Audit.Retention)
		if err != nil {
			return err
		}
		cfg.Retention = d
	}
	for i, w := range f.Webhooks {
		if err := w.validate(); err != nil {
			return fmt.Errorf("webhook #%d: %v", i+1, err)
		}
		if w.Format == "" {
			w.Format = "json"
		}
		cfg.Webhooks = append(cfg.Webhooks, w)
	}
	return nil
}

// parseRetention reads "7d" (whole days) or any Go duration such as "36h".
func parseRetention(s string) (time.Duration, error) {
	bad := errors.New("audit.retention must be a positive duration such as 7d or 36h")
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 || days > 36500 {
			return 0, bad
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, bad
	}
	return d, nil
}

func (w Webhook) validate() error {
	if w.URL == "" {
		return errors.New("url is required")
	}
	if !strings.Contains(w.URL, "{{") { // a template is checked once the vault has filled it in
		if err := checkURL(w.URL); err != nil {
			return err
		}
	}
	if !slices.Contains([]string{"", "json", "ntfy"}, w.Format) {
		return errors.New(`format must be "json" or "ntfy"`)
	}
	for _, a := range w.Actions {
		if !slices.Contains([]string{ActionRun, ActionSet, ActionRm}, a) {
			return errors.New(`actions may only list "run", "set" and "rm"`)
		}
	}
	for _, s := range w.Secrets {
		if !secretRef.MatchString(s) {
			return errors.New("secrets must list names such as AWS_PROD or AWS_PROD.region")
		}
	}
	return nil
}

// checkURL accepts only http and https URLs with a host. Its error never contains the URL.
func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url is not a valid http or https URL")
	}
	return nil
}

// matches reports whether the webhook wants rec: its action is listed and one of its secrets is
// listed. An empty list matches everything.
func (w Webhook) matches(r Record) bool {
	if len(w.Actions) > 0 && !slices.Contains(w.Actions, r.Action) {
		return false
	}
	if len(w.Secrets) == 0 {
		return true
	}
	for _, have := range r.Secrets {
		for _, want := range w.Secrets {
			if have == want || strings.HasPrefix(have, want+".") {
				return true
			}
		}
	}
	return false
}
