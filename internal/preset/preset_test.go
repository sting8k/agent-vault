package preset

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sting8k/agent-vault/internal/vault"
)

// Every preset must produce an entry the vault accepts: valid field and env names, no env
// name used twice. A typo in the table would otherwise only show when a person runs set.
func TestEveryPresetIsStorable(t *testing.T) {
	v, err := vault.Open([]string{"AGV_HOME=" + filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range Names() {
		p, ok := Lookup(name)
		if !ok {
			t.Fatalf("Names lists %s but Lookup does not find it", name)
		}
		c := vault.Change{Description: "d", Type: p.Name}
		for _, f := range p.Fields {
			c.Fields = append(c.Fields, vault.FieldChange{Name: f.Name, Value: []byte("value"), Env: f.Env, File: f.File})
		}
		if p.Name == Custom {
			c.Fields = []vault.FieldChange{{Name: "any", Value: []byte("value")}}
		}
		if err := v.Set(fmt.Sprintf("PRESET_%d", i), c); err != nil {
			t.Errorf("preset %s cannot be stored: %v", name, err)
		}
	}
}

func TestLookupReturnsACopy(t *testing.T) {
	p, _ := Lookup("aws")
	p.Fields[0].Name = "changed"
	if again, _ := Lookup("aws"); again.Fields[0].Name != "access_key_id" {
		t.Error("changing a looked-up preset changed the table")
	}
}
