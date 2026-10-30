package gatestest_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
)

func TestGateStoreContract(t *testing.T) {
	gatestest.StoreContract(t, func(t *testing.T) (gates.Store, gates.Reader, gates.Sent) {
		s := gatestest.NewStore(func() time.Time { return gatestest.Epoch })
		return s, s, s
	})
}
