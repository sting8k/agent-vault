package cli

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/sting8k/agent-vault/internal/audit"
	"github.com/sting8k/agent-vault/internal/inject"
	"github.com/sting8k/agent-vault/internal/vault"
)

// trail is what run, set and rm report to: the audit log and the webhooks of config.json.
// Neither can fail a command: problems are warnings on stderr.
type trail struct {
	sys   IO
	dir   string
	cfg   audit.Config
	start time.Time
}

// openTrail reads config.json. An invalid file is an error, so the command does not run with
// settings the user did not intend.
func openTrail(sys IO) (*trail, error) {
	dir, err := vault.Home(sys.Env)
	if err != nil {
		return nil, err
	}
	cfg, err := audit.LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	return &trail{sys: sys, dir: dir, cfg: cfg, start: time.Now()}, nil
}

// record describes a finished action. program is argv[0] for run and "" otherwise.
func (t *trail) record(action string, secrets []string, program string, code int) audit.Record {
	cwd, _ := os.Getwd()
	secrets = slices.Compact(slices.Sorted(slices.Values(secrets)))
	if secrets == nil {
		secrets = []string{}
	}
	return audit.Record{
		Time: time.Now().UTC().Truncate(time.Millisecond), Action: action, Secrets: secrets,
		Program: program, Cwd: cwd, Exit: code, DurationMS: time.Since(t.start).Milliseconds(),
	}
}

// log appends rec to the audit log if it is enabled.
func (t *trail) log(rec audit.Record) {
	if !t.cfg.Enabled {
		return
	}
	if err := audit.Append(t.dir, t.cfg.Retention, rec); err != nil {
		fmt.Fprintf(t.sys.Stderr, "agv: audit log: %v\n", err)
	}
}

// notify sends rec to the webhooks that match it. The URL may name a secret, so it is
// resolved here, at the end, and never appears in a warning.
func (t *trail) notify(rec audit.Record) {
	for _, err := range audit.Notify(t.cfg.Webhooks, rec, t.resolve) {
		fmt.Fprintf(t.sys.Stderr, "agv: %v\n", err)
	}
}

func (t *trail) resolve(tmpl string) (string, error) {
	src, err := openSource(t.sys)
	if err != nil {
		return "", err
	}
	return inject.Expand(tmpl, src)
}

// done reports a finished set or rm.
func (t *trail) done(action, name string) {
	rec := t.record(action, []string{name}, "", 0)
	t.log(rec)
	t.notify(rec)
}
