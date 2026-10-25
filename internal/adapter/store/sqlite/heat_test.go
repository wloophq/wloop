package sqlite

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func init() {
	hotPlans = append(hotPlans,
		hotPlan{name: "heat of units", query: heatSQL, args: 2, want: []string{"SEARCH i USING PRIMARY KEY (provider=? AND unit_key=?)"},
			tempSort: "USE TEMP B-TREE FOR GROUP BY"},
	)
}

type heatClock struct{ now time.Time }

func (c *heatClock) Now() time.Time { return c.now }

var heatNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func heatUnit(n int) unit.Unit {
	return unit.Unit{Provider: "slack", Key: fmt.Sprintf("T000AAA/D000AAA/seg/%d", n)}
}

func createOp(u unit.Unit, id string) items.Op {
	kind, summary := items.Request, "ask "+id
	return items.Op{Op: items.OpCreate, Unit: u, Item: id, Anchor: unit.Anchor{Unit: u, Ref: "D000AAA/1.1"},
		Fields: &items.Fields{Kind: &kind, Summary: &summary}}
}

func resolveOp(u unit.Unit, id string) items.Op {
	return items.Op{Op: items.OpResolve, Unit: u, Item: id, Anchor: unit.Anchor{Unit: u, Ref: "D000AAA/1.2"}, Resolution: items.Done}
}

func ledgerAt(t *testing.T, db *DB, clock *heatClock, at time.Time, ops ...items.Op) {
	t.Helper()
	clock.now = at
	l := &coreitems.Ledger{Store: db.Items(), Clock: clock}
	res, err := l.Apply(context.Background(), items.User, ops)
	if err != nil || len(res.Refused) > 0 || len(res.Applied) != len(ops) {
		t.Fatalf("apply %v: %+v", err, res)
	}
}

func TestHeatFromTheLedger(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	clock := &heatClock{}
	active, warm, cooled, none := heatUnit(1), heatUnit(2), heatUnit(3), heatUnit(4)
	ledgerAt(t, db, clock, heatNow.Add(-6*24*time.Hour), createOp(active, "i1"), createOp(warm, "i1"), createOp(cooled, "i1"),
		createOp(active, "i2"), createOp(warm, "i2"))
	ledgerAt(t, db, clock, heatNow.Add(-5*24*time.Hour), resolveOp(cooled, "i1"), resolveOp(warm, "i2"))
	ledgerAt(t, db, clock, heatNow.Add(-time.Hour), resolveOp(warm, "i1"), resolveOp(active, "i2"))
	clock.now = heatNow
	got, err := HeatReader{DB: db, Clock: clock}.Heat(context.Background(), []unit.Unit{active, warm, cooled, none})
	if err != nil {
		t.Fatal(err)
	}
	want := map[unit.Unit]port.Heat{active: port.HeatActive, warm: port.HeatWarm}
	if !maps.Equal(got, want) {
		t.Errorf("heat %v, want %v", got, want)
	}
	clock.now = heatNow.Add(72*time.Hour - time.Hour - time.Second)
	if got, _ := (HeatReader{DB: db, Clock: clock}).Heat(context.Background(), []unit.Unit{warm}); got[warm] != port.HeatWarm {
		t.Errorf("a second before warm_for ends: %v", got)
	}
	clock.now = heatNow.Add(72 * time.Hour)
	if got, _ := (HeatReader{DB: db, Clock: clock}).Heat(context.Background(), []unit.Unit{warm}); len(got) != 0 {
		t.Errorf("after warm_for: %v", got)
	}
	short := HeatReader{DB: db, Clock: clock, WarmFor: func() time.Duration { return 100 * time.Hour }}
	if got, _ := short.Heat(context.Background(), []unit.Unit{warm}); got[warm] != port.HeatWarm {
		t.Errorf("with warm_for 100h: %v", got)
	}
}

func TestHeatFollowsItemChanges(t *testing.T) {
	t.Parallel()
	db, log := openLogged(t, filepath.Join(t.TempDir(), "heat.db"))
	clock := &heatClock{}
	cold := heatUnit(1)
	h := HeatReader{DB: db, Clock: clock}
	clock.now = heatNow
	if got, _ := h.Heat(context.Background(), []unit.Unit{cold}); len(got) != 0 {
		t.Fatalf("heat of a unit without items: %v", got)
	}
	from := log.Gen()
	ledgerAt(t, db, clock, heatNow, createOp(cold, "i1"))
	c := log.Since(from, port.TopicItems)
	if !c.Has(port.TopicItems) || !slices.Equal(c.Units, []unit.Unit{cold}) {
		t.Errorf("the open noted %+v", c)
	}
	if got, _ := h.Heat(context.Background(), []unit.Unit{cold}); got[cold] != port.HeatActive {
		t.Errorf("after the open: %v", got)
	}
}

func TestHeatIsOneQuery(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	clock := &heatClock{}
	units := make([]unit.Unit, 500)
	var ops []items.Op
	for i := range units {
		units[i] = heatUnit(i)
		if i%2 == 0 {
			ops = append(ops, createOp(units[i], "i1"))
		}
	}
	ledgerAt(t, db, clock, heatNow, ops...)
	ctx, s := metertest.Start(t, context.Background())
	got, err := HeatReader{DB: db, Clock: clock}.Heat(ctx, units)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 250 {
		t.Errorf("%d units hot, want 250", len(got))
	}
	if n := metertest.Count(s, metertest.Limit{Res: meter.SQL}); n != 1 {
		t.Errorf("%d statements for 500 units, want 1\n%s", n, metertest.Report(s.Tally()))
	}
}

func BenchmarkHeat500(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "heat.db"), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	clock := &heatClock{now: heatNow}
	units := make([]unit.Unit, 500)
	var ops []items.Op
	for i := range units {
		units[i] = heatUnit(i)
		for j := range 4 {
			ops = append(ops, createOp(units[i], fmt.Sprintf("i%d", j)))
		}
	}
	l := &coreitems.Ledger{Store: db.Items(), Clock: clock}
	if _, err := l.Apply(context.Background(), items.User, ops); err != nil {
		b.Fatal(err)
	}
	h := HeatReader{DB: db, Clock: clock}
	b.ResetTimer()
	for b.Loop() {
		if _, err := h.Heat(context.Background(), units); err != nil {
			b.Fatal(err)
		}
	}
}
