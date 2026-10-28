package task

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Codec is the codec of the entry provider: a unit's key is an entry's ID.
type Codec struct{}

func (Codec) Provider() unit.ProviderID { return unit.Entry }

// Valid accepts a non-blank key without spaces or line breaks.
func (Codec) Valid(key string) error {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t\r\n") {
		return fmt.Errorf("invalid entry key %q", key)
	}
	return nil
}

// IDOf is the entry u names; false for a unit of another provider.
func IDOf(u unit.Unit) (ID, bool) {
	if u.Provider != unit.Entry {
		return "", false
	}
	return ID(u.Key), true
}

// UnitOf is the entry provider's unit of id.
func UnitOf(id ID) unit.Unit { return unit.Unit{Provider: unit.Entry, Key: string(id)} }
