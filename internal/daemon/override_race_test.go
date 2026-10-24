package daemon

import (
	"context"
	"sync"
	"testing"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

func TestMuteExpiryAndTakeOverKeepBothWrites(t *testing.T) {
	s := New(testDeps(t))
	ctx := context.Background()
	for n := 1; n <= 100; n++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: n}
		muted := dinv.Override{PR: ref, MuteUntilStateChange: true, MutedKind: "waiting", MutedWaitingOn: "alice"}
		if err := s.store().SetOverride(ctx, muted); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, _, err := s.expireMute(ctx, ref.Unit(), muted); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.store().SetAdopted(ctx, ref); err != nil {
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
	s := New(testDeps(t))
	ctx := context.Background()
	for n := 1; n <= 100; n++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: n}
		if err := s.store().SetOverride(ctx, dinv.Override{PR: ref, Tracked: true}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := s.updateOverride(ctx, ref.Unit(), func(o *dinv.Override) error { o.Hold = true; return nil }); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.store().SetAdopted(ctx, ref); err != nil {
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
