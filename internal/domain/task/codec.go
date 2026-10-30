package task

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Codec is the codec of the task provider: a unit's key is a task's ID.
type Codec struct{}

func (Codec) Provider() unit.ProviderID { return unit.Task }

// Valid accepts a non-blank key without spaces or line breaks.
func (Codec) Valid(key string) error {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t\r\n") {
		return fmt.Errorf("invalid task key %q", key)
	}
	return nil
}

// IDOf is the task u names; false for a unit of another provider.
func IDOf(u unit.Unit) (ID, bool) {
	if u.Provider != unit.Task {
		return "", false
	}
	return ID(u.Key), true
}

// UnitOf is the task provider's unit of id.
func UnitOf(id ID) unit.Unit { return unit.Unit{Provider: unit.Task, Key: string(id)} }
