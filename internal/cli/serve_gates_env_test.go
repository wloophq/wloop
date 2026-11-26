package cli

import (
	"context"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/messages"
)

// An Env not built through owning reads its gates and its messages service
// without panicking.
func TestAZeroEnvReadsItsGates(t *testing.T) {
	t.Parallel()
	var e Env
	if svc, rd := e.builtGates(); svc != nil || rd != nil {
		t.Fatalf("gates %v reader %v before any was built", svc, rd)
	}
	if e.inbox() != nil {
		t.Fatal("a messages service before any was built")
	}
}

// The gates read the messages service through the lock its writer takes,
// so a resolution that runs while serve sets the service is no race.
func TestTheGatesReadTheMessagesServiceUnderItsLock(t *testing.T) {
	t.Parallel()
	e := (&Env{}).owning()
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			_, _ = messageOptions{e}.PinnedDrafts(context.Background())
		}
	})
	e.setInbox(&messages.Service{})
	wg.Wait()
	if e.inbox() == nil {
		t.Fatal("the messages service set is not read back")
	}
}
