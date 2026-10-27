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
		// A name that is not even valid (shop/prod/db) cannot be passed to set; the suggestion is the advice.
		set := "agv set"
		if vault.CheckName(name) == nil {
			set += " " + name
		}
		return nil, fmt.Errorf("%w\nNot stored? If the user gave you the value, add it with '%s'; otherwise ask the user to run it in a separate terminal.", err, set)
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
