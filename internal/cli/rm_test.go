package cli

import (
	"strings"
	"testing"
)

func names(t *testing.T, home string) string {
	t.Helper()
	l, err := openVault(t, home).List()
	if err != nil {
		t.Fatal(err)
	}
	var n []string
	for _, e := range l {
		n = append(n, e.Name)
	}
	return strings.Join(n, ",")
}

func TestRmNeverRemovesWithoutAnExplicitYes(t *testing.T) {
	home := newHome(t)
	seed(t, home, "KEEP_ME", "api-token", "d", "value")

	// At a terminal, anything but y is no: Enter, n, or no answer at all.
	for _, answer := range []string{"", "n", "maybe"} {
		if r := agv(t, home, person("Remove", answer), "rm", "KEEP_ME"); r.code == 0 {
			t.Errorf("answer %q removed or exited 0", answer)
		}
	}
	if names(t, home) != "KEEP_ME" {
		t.Error("the entry was removed")
	}
}

func TestRmRemovesOnlyTheNamedEntry(t *testing.T) {
	home := newHome(t)
	seed(t, home, "ONE", "api-token", "d", "value")
	seed(t, home, "TWO", "api-token", "d", "value")
	seed(t, home, "THREE", "api-token", "d", "value")

	if r := agv(t, home, person("Remove", "yes"), "rm", "ONE"); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := agv(t, home, person("Remove", "y"), "rm", "TWO"); r.code != 0 {
		t.Fatal(r.err)
	}
	if got := names(t, home); got != "THREE" {
		t.Errorf("entries left: %s", got)
	}
	if string(mustFields(t, home, "THREE")["value"].Value) != sentinel+"-value" {
		t.Error("a remaining entry changed")
	}

	r := agv(t, home, person("Remove", "y"), "rm", "THEE")
	if r.code == 0 || !strings.Contains(r.err, "THREE") {
		t.Errorf("an unknown name should fail and suggest THREE: exit %d: %s", r.code, r.err)
	}
	assertNoLeak(t, r, sentinel)
	if got := names(t, home); got != "THREE" {
		t.Errorf("a failed rm changed the vault: %s", got)
	}
}
