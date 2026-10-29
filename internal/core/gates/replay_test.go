package gates_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

func (f fixture) unapplied(t *testing.T) []gate.Gate {
	t.Helper()
	got, err := f.store.Unapplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAConsequenceACrashInterruptedRunsAtTheNextTick(t *testing.T) {
	f := newFixture(t, nil, bundled)
	ctx := context.Background()
	g := f.open(t, gate.Gate{Kind: gate.KindDraft, Subject: "7"}, nil)
	if _, err := f.store.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.svc.Tick(ctx, opened); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(f.cons.Calls, []string{"publish 1"}) || len(f.unapplied(t)) != 0 {
		t.Fatalf("calls %v, unapplied %+v: the consequence runs once at the next tick", f.cons.Calls, f.unapplied(t))
	}
}

func TestAResolvedGatesConsequenceIsMarkedApplied(t *testing.T) {
	f := newFixture(t, nil, bundled)
	g := f.open(t, gate.Gate{Kind: gate.KindDraft, Subject: "7"}, nil)
	if err := f.svc.Resolve(context.Background(), g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Tick(context.Background(), opened); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.cons.Calls, []string{"publish 1"}) || len(f.unapplied(t)) != 0 {
		t.Fatalf("calls %v, unapplied %+v", f.cons.Calls, f.unapplied(t))
	}
}

func TestAFailedConsequenceIsNotRetriedByTheTick(t *testing.T) {
	f := newFixture(t, nil, bundled)
	f.cons.Err = errors.New("the publisher is down")
	g := f.open(t, gate.Gate{Kind: gate.KindDraft, Subject: "7"}, nil)
	if err := f.svc.Resolve(context.Background(), g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err == nil {
		t.Fatal("a failed consequence resolved quietly")
	}
	if err := f.svc.Tick(context.Background(), opened); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.cons.Calls, []string{"publish 1"}) {
		t.Fatalf("calls %v: a consequence that ran and failed is a person's to retry", f.cons.Calls)
	}
}

func TestAConsequenceCutByItsContextRunsAtTheNextTick(t *testing.T) {
	f := newFixture(t, nil, bundled)
	g := f.open(t, gate.Gate{Kind: gate.KindDraft, Subject: "7"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.cons.Err = context.Canceled
	if err := f.svc.Resolve(ctx, g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err == nil {
		t.Fatal("a cut consequence resolved quietly")
	}
	f.cons.Err = nil
	if err := f.svc.Tick(context.Background(), opened); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.cons.Calls, []string{"publish 1", "publish 1"}) || len(f.unapplied(t)) != 0 {
		t.Fatalf("calls %v, unapplied %+v", f.cons.Calls, f.unapplied(t))
	}
}

// blockingPublisher holds the first Publish until release closes, counting the calls.
type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (p *blockingPublisher) Publish(context.Context, gate.Gate) error {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first {
		p.entered <- struct{}{}
		<-p.release
	}
	return nil
}

func TestTheTickLeavesAConsequenceInFlight(t *testing.T) {
	f := newFixture(t, nil, bundled)
	pub := &blockingPublisher{entered: make(chan struct{}, 2), release: make(chan struct{})}
	svc := gates.New(gates.Deps{Store: f.store, Reader: f.store, Profile: bundled, Clock: clock{fixedNow}, Publisher: pub})
	g, err := svc.Open(context.Background(), gate.Gate{Kind: gate.KindDraft, Subject: "7", Unit: task.UnitOf("t-0000000000000001")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := svc.Resolve(context.Background(), g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(wg.Wait)
	<-pub.entered
	if err := svc.Tick(context.Background(), opened); err != nil {
		t.Fatal(err)
	}
	close(pub.release)
	wg.Wait()
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if pub.calls != 1 {
		t.Fatalf("published %d times: the tick replayed a consequence still running", pub.calls)
	}
}
