package cli

import (
	"errors"
	"fmt"

	"github.com/sting8k/agent-vault/internal/inject"
	"github.com/sting8k/agent-vault/internal/vault"
)

// vaultSource adapts the vault to inject.Source.
type vaultSource struct{ v *vault.Vault }

func openVaultSource(io IO) (inject.Source, error) {
	v, err := vaultOf(io)
	if err != nil {
		return nil, err
	}
	return vaultSource{v}, nil
}

func (s vaultSource) Entry(name string) (map[string]inject.Field, error) {
	fields, err := s.v.Fields(name)
	var nf *vault.NotFoundError
	if errors.As(err, &nf) {
		return nil, fmt.Errorf("%w; if it is not stored, ask the user to run 'agv set %s' in a separate terminal", err, name)
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]inject.Field, len(fields))
	for n, f := range fields {
		out[n] = inject.Field(f)
	}
	return out, nil
}
