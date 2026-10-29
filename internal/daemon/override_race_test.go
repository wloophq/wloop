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

func TestMuteExpiryAndTakeOverKeepBothWrites(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	ctx := context.Background()
	for n := 1; n <= 100; n++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: n}
		muted := dinv.Override{PR: ref, MuteUntilStateChange: true, MutedKind: "waiting", MutedWaitingOn: "alice"}
		if err := inventorytest.Of(s.store()).SetOverride(ctx, muted); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			cleared := map[string]string{port.OverrideMuteUntilStateChange: "", port.OverrideMutedKind: "", port.OverrideMutedWaitingOn: ""}
			match := map[string]string{port.OverrideMuteUntilStateChange: port.OverrideTrue, port.OverrideMutedKind: "waiting", port.OverrideMutedWaitingOn: "alice"}
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
		if !o.Adopted || o.MuteUntilStateChange || o.MutedKind != "" {
			t.Fatalf("%s: adopted %v, mute %v %q after an expiry beside a take-over", ref, o.Adopted, o.MuteUntilStateChange, o.MutedKind)
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
