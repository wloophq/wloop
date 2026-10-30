package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

func TestAGuidanceClearAndTakeOverKeepBothWrites(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	ctx := context.Background()
	for n := 1; n <= 100; n++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: n}
		if err := s.store().SetNextGuidance(ctx, ref, "ask alice"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			cleared := map[string]string{port.OverrideNextGuidance: ""}
			match := map[string]string{port.OverrideNextGuidance: "ask alice"}
			if _, err := s.deps.Overrides.SetOverride(ctx, ref.Unit(), cleared, match); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := inventorytest.Of(s.store()).SetAdopted(ctx, ref); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		o, err := s.store().Override(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Adopted || o.NextGuidance != "" {
			t.Fatalf("%s: adopted %v, guidance %q after a clear beside a take-over", ref, o.Adopted, o.NextGuidance)
		}
	}
}

func TestOverrideActionAndTakeOverKeepBothWrites(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	ctx := context.Background()
	for n := 1; n <= 100; n++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: n}
		if err := inventorytest.Of(s.store()).SetOverride(ctx, dinv.Override{PR: ref, Tracked: true}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := s.applyHold(ctx, "pause", ref.Unit()); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := inventorytest.Of(s.store()).SetAdopted(ctx, ref); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		o, err := s.store().Override(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Adopted || !o.Hold || !o.Tracked {
			t.Fatalf("%s: adopted %v, hold %v, tracked %v after a hold beside a take-over", ref, o.Adopted, o.Hold, o.Tracked)
		}
	}
}
