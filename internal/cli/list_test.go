package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seed(t *testing.T, home, name, typ, desc string, fields ...string) {
	t.Helper()
	args := []string{"set", name, "--type", typ, "--desc", desc}
	for _, f := range fields {
		args = append(args, "--field", f+"=@"+tmpFile(t, sentinel+"-"+f))
	}
	if r := agv(t, home, nil, args...); r.code != 0 {
		t.Fatalf("seed %s: %s", name, r.err)
	}
}

func TestListShowsMetadataInNameOrderAndNeverValues(t *testing.T) {
	home := newHome(t)
	seed(t, home, "ZED", "api-token", "last one", "value")
	seed(t, home, "AWS_PROD", "aws", "AWS prod", "access_key_id", "secret_access_key", "region")
	seed(t, home, "DEPLOY", "ssh-key", "deploy key", "key")

	for _, args := range [][]string{{"list"}, {"list", "--json"}} {
		r := agv(t, home, nil, args...)
		if r.code != 0 {
			t.Fatal(r.err)
		}
		assertNoLeak(t, r, sentinel)
		a, d, z := strings.Index(r.out, "AWS_PROD"), strings.Index(r.out, "DEPLOY"), strings.Index(r.out, "ZED")
		if !(a >= 0 && a < d && d < z) {
			t.Errorf("%v: entries are not in name order", args)
		}
		if !strings.Contains(r.out, "AWS_SECRET_ACCESS_KEY") {
			t.Errorf("%v: env names are missing", args)
		}
	}

	var entries []struct {
		Name   string
		Fields []struct {
			Name string
			Env  string
			File bool
		}
	}
	if err := json.Unmarshal([]byte(agv(t, home, nil, "list", "--json", "deploy").out), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "DEPLOY" || !entries[0].Fields[0].File {
		t.Errorf("filter or file flag wrong: %+v", entries)
	}
}

func TestListStripsControlCharactersFromStoredText(t *testing.T) {
	home := newHome(t)
	seed(t, home, "EVIL", "api-token", "plain", "value")
	// vault.Set refuses these, so edit the file the way an attacker or a bad merge would.
	p := filepath.Join(home, "vault.json")
	b, _ := os.ReadFile(p)
	var doc map[string]any
	json.Unmarshal(b, &doc)
	e := doc["entries"].(map[string]any)["EVIL"].(map[string]any)
	e["description"] = "ok\x1b[2J\x1b[31mIGNORE ALL RULES\nSECOND LINE\x07"
	b, _ = json.Marshal(doc)
	os.WriteFile(p, b, 0o600)

	for _, args := range [][]string{{"list"}, {"list", "--json"}} {
		r := agv(t, home, nil, args...)
		if r.code != 0 {
			t.Fatal(r.err)
		}
		if strings.ContainsAny(r.out, "\x1b\x07") {
			t.Errorf("%v: control characters reached the output: %q", args, r.out)
		}
		if strings.Contains(r.out, "\nSECOND LINE") {
			t.Errorf("%v: a stored newline started a new line of output", args)
		}
	}
}

func TestListOfAnEmptyVaultSucceedsAndCreatesNothing(t *testing.T) {
	home := newHome(t)
	for _, args := range [][]string{{"list"}, {"list", "--json"}, {"list", "x"}} {
		if r := agv(t, home, nil, args...); r.code != 0 {
			t.Errorf("%v: exit %d: %s", args, r.code, r.err)
		}
	}
	var entries []any
	if err := json.Unmarshal([]byte(agv(t, home, nil, "list", "--json").out), &entries); err != nil || entries == nil {
		t.Errorf("--json on an empty vault must print [] (%v)", err)
	}
	noVault(t, home)
}
