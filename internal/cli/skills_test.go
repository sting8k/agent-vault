package cli

import "testing"

// An agent's first call must work, under either name, whatever else is configured.
func TestSkillsPrintsTheSkillUnderBothNames(t *testing.T) {
	for _, name := range []string{"skills", "skill"} {
		r := agv(t, newHome(t), nil, name)
		if r.code != 0 || r.out == "" {
			t.Errorf("agv %s: exit %d, %d bytes of output", name, r.code, len(r.out))
		}
	}
}
