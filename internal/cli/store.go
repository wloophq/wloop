package cli

import (
	"errors"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
)

// errNoStore answers a use case of the daemon called in another process.
var errNoStore = errors.New(brand.Name + "'s store is open only in its daemon")

// store is the daemon's store, which no other process opens.
func (e *Env) store() (*inventory.Store, error) {
	if e.Store == nil {
		return nil, errNoStore
	}
	return e.Store, nil
}
