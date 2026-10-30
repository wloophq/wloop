package readmodel_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

// threads is a provider of units that the test lists and drops.
type threads struct {
	mu    sync.Mutex
	units []unit.Unit
}

func (p *threads) set(units []unit.Unit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.units = units
}

func (p *threads) Provider() unit.ProviderID { return "thread" }

func (p *threads) Listed(context.Context) ([]unit.Unit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.units), nil
}

func (p *threads) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	out := map[unit.Unit]any{}
	for _, u := range units {
		out[u] = u.Key
	}
	return out, nil
}

func (p *threads) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: in.(string), Label: "#" + in.(string), Views: []string{}}, nil
}

func threadUnits(from, to int) []unit.Unit {
	var out []unit.Unit
	for i := from; i < to; i++ {
		out = append(out, unit.Unit{Provider: "thread", Key: fmt.Sprintf("t%04d", i)})
	}
	return out
}

func TestALeavingUnitIsATombstoneUntilTheWindowDropsIt(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, nil)
	p := &threads{}
	p.set(threadUnits(0, 1100))
	m := readmodel.New(log, nil, nil, nil, nil, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	published := make(chan api.Change, 4)
	done := make(chan struct{})
	full, err := m.Rows(ctx, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	running := runStarted(m)
	go func() {
		m.Run(ctx, func(c api.Change) { published <- c })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	<-running
	at := api.Since{Epoch: full.Epoch, Rev: full.Rev}

	p.set(threadUnits(76, 1100))
	log.Note(port.TopicPRs)
	one, err := m.Rows(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if one.Full || len(one.Rows) != 0 || !slices.Equal(one.Removed, threadUnits(0, 76)) {
		t.Fatalf("76 units left: full %v, %d rows, %d removed", one.Full, len(one.Rows), len(one.Removed))
	}
	select {
	case c := <-published:
		if c.All || !slices.Equal(c.Units, threadUnits(0, 76)) {
			t.Fatalf("change %+v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a unit that left published nothing")
	}

	p.set(nil)
	log.Note(port.TopicPRs)
	behind, err := m.Rows(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if !behind.Full || len(behind.Rows) != 0 {
		t.Fatalf("a client behind the dropped tombstones got a delta: full %v, %d rows, %d removed", behind.Full, len(behind.Rows), len(behind.Removed))
	}
	recent, err := m.Rows(ctx, api.Since{Epoch: one.Epoch, Rev: one.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if recent.Full || len(recent.Removed) != 1024 {
		t.Fatalf("a client at the window's edge: full %v, %d removed", recent.Full, len(recent.Removed))
	}
	select {
	case c := <-published:
		if c.Rev != one.Rev+1 || len(c.Units) != 1024 || c.All {
			t.Fatalf("change after 1024 units left: rev %d, %d units, all %v", c.Rev, len(c.Units), c.All)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the units that left published nothing")
	}
}

// runStarted closes once Run has subscribed and refreshed.
func runStarted(m *readmodel.Model) <-chan struct{} {
	running := make(chan struct{})
	var once sync.Once
	m.UseKeeper(func(context.Context, readmodel.Kept) { once.Do(func() { close(running) }) })
	return running
}

func TestAChangeBehindTheTombstonesIsAll(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, nil)
	p := &threads{}
	p.set(threadUnits(0, 1100))
	m := readmodel.New(log, nil, nil, nil, nil, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := m.Rows(ctx, api.Since{}); err != nil {
		t.Fatal(err)
	}
	published := make(chan api.Change, 4)
	done := make(chan struct{})
	running := runStarted(m)
	go func() {
		m.Run(ctx, func(c api.Change) { published <- c })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	<-running
	p.set(threadUnits(0, 10))
	log.Note(port.TopicPRs)
	if _, err := m.Rows(ctx, api.Since{}); err != nil {
		t.Fatal(err)
	}
	p.set(nil)
	log.Note(port.TopicPRs)
	if _, err := m.Rows(ctx, api.Since{}); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-published:
		if !c.All || len(c.Units) != 0 {
			t.Fatalf("a change from behind the dropped tombstones listed units: %+v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing published")
	}
}

func TestRunPublishesWhatMovedBeforeItStarted(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, nil)
	p := &threads{}
	p.set(threadUnits(0, 20))
	m := readmodel.New(log, nil, nil, nil, nil, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := m.Rows(ctx, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	p.set(threadUnits(0, 10))
	log.Note(port.TopicPRs)
	moved, err := m.Rows(ctx, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Rev == first.Rev {
		t.Fatalf("ten units left and the rev stayed at %d", first.Rev)
	}
	published := make(chan api.Change, 4)
	done := make(chan struct{})
	go func() {
		m.Run(ctx, func(c api.Change) { published <- c })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	select {
	case c := <-published:
		if c.Rev != moved.Rev || c.All || !slices.Equal(c.Units, threadUnits(10, 20)) {
			t.Fatalf("change %+v for a client at rev %d", c, first.Rev)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("a client at rev %d was never told of rev %d", first.Rev, moved.Rev)
	}
}

func TestPresentationsComeFromTheLatestSnapshot(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, nil)
	p := &threads{}
	p.set(threadUnits(0, 2))
	m := readmodel.New(log, nil, nil, nil, nil, p)
	gone := unit.Unit{Provider: "thread", Key: "gone"}
	if got := m.Presentations(threadUnits(0, 1)); len(got) != 0 {
		t.Fatalf("presentations before any snapshot %v", got)
	}
	if _, err := m.Rows(context.Background(), api.Since{}); err != nil {
		t.Fatal(err)
	}
	got := m.Presentations(append(threadUnits(0, 2), gone))
	if _, listed := got[gone]; len(got) != 2 || got[threadUnits(0, 1)[0]].Label == "" || listed {
		t.Fatalf("presentations %v", got)
	}
}
