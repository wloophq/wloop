package wakestest_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/core/wakes/wakestest"
)

func TestWakeStoreContract(t *testing.T) {
	wakestest.StoreContract(t, func(*testing.T) wakes.Store { return wakestest.NewStore() })
}
