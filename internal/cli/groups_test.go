//go:build unix

package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sting8k/agent-vault/internal/vault"
)

// One grouped name must be the same name in every place that spells it: set, run, the
// redaction label, the audit record, the webhook filter and the webhook URL template.
func TestGroupedNameRoundTripsThroughSetRunRedactionAuditAndWebhook(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+" "+string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	env, home := auditEnv(t, `{"webhooks": [{"url": "{{ALERTS/NTFY}}", "actions": ["run"], "secrets": ["SHOP/PROD/"]}, {"url": "{{ALERTS/NTFY}}", "actions": ["run"], "secrets": ["SHOP/STAGING/"]}]}`)
	if r := agv(t, home, nil, "set", "ALERTS/NTFY", "--type", "api-token", "--desc", "hook", "--field", "value=@"+tmpFile(t, srv.URL+"/hook")); r.code != 0 {
		t.Fatal(r.err)
	}
	seed(t, home, "SHOP/PROD/DB", "api-token", "Shop prod Postgres", "value")

	code, out, errs := runIn(t, env, append([]string{"--env", "TOKEN={{SHOP/PROD/DB}}", "--"}, selfCmd("leak")...)...)
	if code != 3 || errs != "err:[REDACTED:SHOP/PROD/DB.value]\n" {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if !strings.Contains(out, "raw:[REDACTED:SHOP/PROD/DB.value]") || strings.Contains(out, sentinel) {
		t.Errorf("redaction label: %q", out)
	}
	raw, recs := auditLog(t, home)
	if last := recs[len(recs)-1]; last.Action != "run" || fmt.Sprint(last.Secrets) != "[SHOP/PROD/DB.value]" || strings.Contains(raw, sentinel) {
		t.Errorf("audit log: %s", raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.HasPrefix(bodies[0], "/hook ") || !strings.Contains(bodies[0], "SHOP/PROD/DB.value") {
		t.Errorf("webhooks (only the SHOP/PROD/ filter matches): %q", bodies)
	}
}

// Each mistake hands back a name to copy, and nothing else of a vault: never a value.
func TestMistakesNameTheRightSecret(t *testing.T) {
	env, home := auditEnv(t, "")
	for _, n := range []string{"SHOP/PROD/DB", "SHOP/STAGING/DB", "SHIP/PROD/DB", "PERSONAL/DB"} {
		// Through the vault: SHIP/ next to SHOP/ is what set asks about.
		err := openVault(t, home).Set(n, vault.Change{Description: "d", Type: "api-token", Fields: []vault.FieldChange{{Name: "value", Value: []byte(sentinel)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		what string
		args []string
		want string
	}{
		{"wrong case in a placeholder", []string{"--env", "X={{shop/prod/db}}", "--", "true"}, "write {{SHOP/PROD/DB}}"},
		{"wrong case in a field", []string{"--env", "X={{SHOP/PROD/DB.Value}}", "--", "true"}, "write {{SHOP/PROD/DB.value}}"},
		{"wrong case in --env-from", []string{"--env-from", "shop/prod/db", "--", "true"}, "closest: SHOP/PROD/DB"},
		{"misspelled group", []string{"--env", "X={{SHPO/PROD/DB}}", "--", "true"}, "closest: SHOP/PROD/DB"},
		{"the last part alone", []string{"--env", "X={{DB}}", "--", "true"}, "PERSONAL/DB\n  SHIP/PROD/DB\n  SHOP/PROD/DB\n  SHOP/STAGING/DB"},
		{"flat spelling of a grouped name", []string{"--env", "X={{SHOP_PROD_DB}}", "--", "true"}, "closest: SHOP/PROD/DB"},
	} {
		code, out, errs := runIn(t, env, c.args...)
		if code != 125 || out != "" || !strings.Contains(errs, c.want) || strings.Contains(errs+out, sentinel) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q, want %q", c.what, code, out, errs, c.want)
		}
	}
	if r := agv(t, home, nil, "rm", "shop/prod/db"); r.code != 125 || !strings.Contains(r.err, "closest: SHOP/PROD/DB") {
		t.Errorf("rm: %d %q", r.code, r.err)
	}

	// Other tools' braces still reach the command.
	code, out, errs := runIn(t, env, append([]string{"--"}, selfCmd("args", "{{.ID}}", "{{ .Values.x }}", "{{value}}")...)...)
	if code != 0 || out != "{{.ID}}\n{{ .Values.x }}\n{{value}}\n" {
		t.Errorf("foreign braces: exit %d, stdout %q, stderr %q", code, out, errs)
	}
}

func TestListShowsFullNamesInGroupOrderAndSuggestsGroups(t *testing.T) {
	home := newHome(t)
	seed(t, home, "GITHUB", "api-token", "personal PAT", "value")
	seed(t, home, "SHOP/PROD/DB", "api-token", "Shop prod Postgres", "value")
	seed(t, home, "SHOP/PROD/STRIPE", "api-token", "Shop prod Stripe", "value")
	seed(t, home, "SHOP/STAGING/DB", "api-token", "Shop staging Postgres", "value")

	r := agv(t, home, nil, "list")
	want := `GITHUB (api-token): personal PAT
  value
SHOP/PROD/DB (api-token): Shop prod Postgres
  value
SHOP/PROD/STRIPE (api-token): Shop prod Stripe
  value
SHOP/STAGING/DB (api-token): Shop staging Postgres
  value
`
	if r.code != 0 || r.out != want {
		t.Errorf("list:\n%s", r.out)
	}
	// The filter ignores case, and --json keeps full names.
	if r := agv(t, home, nil, "list", "shop/staging"); !strings.HasPrefix(r.out, "SHOP/STAGING/DB ") || strings.Contains(r.out, "PROD") {
		t.Errorf("filter:\n%s", r.out)
	}
	if r := agv(t, home, nil, "list", "--json", "stripe"); !strings.Contains(r.out, `"name": "SHOP/PROD/STRIPE"`) {
		t.Errorf("json:\n%s", r.out)
	}

	r = agv(t, home, nil, "list", "shpo")
	if !strings.Contains(r.out, "No secret matches") || !strings.Contains(r.out, "Close groups: SHOP/") {
		t.Errorf("no match, text:\n%s", r.out)
	}
	if r := agv(t, home, nil, "list", "--json", "shpo"); strings.TrimSpace(r.out) != "[]" || !strings.Contains(r.err, "Close groups: SHOP/") {
		t.Errorf("no match, json: stdout %q stderr %q", r.out, r.err)
	}
	if r := agv(t, home, nil, "list", "nothing-like-it"); strings.Contains(r.out, "Close") {
		t.Errorf("a filter near nothing got suggestions:\n%s", r.out)
	}
}

// A group that is a typo of an existing one is never opened silently. Without a terminal the
// message names the existing group and the way on; at a terminal the person is asked.
func TestSetNewGroupNearAnExistingOneIsAskedAbout(t *testing.T) {
	home := newHome(t)
	seed(t, home, "SHOP/PROD/DB", "api-token", "d", "value")
	set := func(in io.Reader, name string, extra ...string) result {
		args := append([]string{"set", name, "--type", "api-token", "--desc", "d", "--field", "value=@" + tmpFile(t, sentinel)}, extra...)
		return agv(t, home, in, args...)
	}
	has := func(name string) bool { return strings.Contains(names(t, home), name) }

	r := set(nil, "SHPO/PROD/DB")
	if r.code == 0 || has("SHPO") || !strings.Contains(r.err, "SHOP/PROD/DB") || !strings.Contains(r.err, "--new-group") {
		t.Errorf("no terminal: exit %d, %q", r.code, r.err)
	}
	assertNoLeak(t, r, sentinel)

	declined := person("new group", "n")
	if r := set(declined, "SHPO/PROD/DB"); r.code == 0 || has("SHPO") || declined.askedAbout("new group") == nil {
		t.Errorf("declined: exit %d, %q", r.code, r.err)
	}
	if r := set(person("new group", "y"), "SHPO/PROD/DB"); r.code != 0 || !has("SHPO/PROD/DB") {
		t.Errorf("accepted: exit %d, %q", r.code, r.err)
	}
	if r := set(nil, "SHOP/PRD/DB", "--new-group"); r.code != 0 || !has("SHOP/PRD/DB") {
		t.Errorf("--new-group: exit %d, %q", r.code, r.err)
	}
	// An existing group, a new environment and a flat name need no question.
	for _, n := range []string{"SHOP/PROD/STRIPE", "SHOP/DEV/DB", "ACME/PROD/DB", "FLAT_ONE"} {
		if r := set(nil, n); r.code != 0 || !has(n) {
			t.Errorf("%s: exit %d, %q", n, r.code, r.err)
		}
	}

	// A wrong-case name of a stored secret is answered with the stored name, not repeated.
	r = set(nil, "shop/prod/db")
	if r.code == 0 || !strings.Contains(r.err, "closest: SHOP/PROD/DB") {
		t.Errorf("wrong case: exit %d, %q", r.code, r.err)
	}
}

func TestSetWithoutANameShowsTheGroupsInUse(t *testing.T) {
	home := newHome(t)
	seed(t, home, "SHOP/PROD/DB", "api-token", "d", "value")
	term := person("Name", "ACME/DEV/KEY", "Type", "api-token", "Description", "d", ".value", sentinel, "Save?", "y")
	r := agv(t, home, term, "set")
	if r.code != 0 || !strings.Contains(r.err, "Groups in use: SHOP/, SHOP/PROD/") {
		t.Errorf("exit %d:\n%s", r.code, r.err)
	}
}
