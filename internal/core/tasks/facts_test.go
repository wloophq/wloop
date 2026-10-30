package tasks_test

import (
	"context"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type cardMap map[unit.Unit]card.Card

func (m cardMap) Save(context.Context, card.Card, time.Time) (bool, error) { return false, nil }

func (m cardMap) Get(_ context.Context, us []unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	out := map[unit.Unit]card.Snapshot{}
	for _, u := range us {
		if c, ok := m[u]; ok {
			out[u] = card.Snapshot{Card: c, At: t0}
		}
	}
	return out, nil
}

func cardFacts(cs cardMap, off ...unit.ProviderID) coretasks.CardFacts {
	return coretasks.CardFacts{Cards: cs, Me: func() task.Me { return me },
		Enabled: func(p unit.ProviderID) bool {
			for _, o := range off {
				if o == p {
					return false
				}
			}
			return true
		}}
}

func TestFactsFromCard(t *testing.T) {
	f := cardFacts(cardMap{unitU: {Unit: unitU, StartedByMe: true, ClosurePosted: true, Cares: true,
		LastHuman: &card.Human{Who: ana, What: "commented", At: t0.Add(-time.Hour)}}})
	got, ok, err := f.UnitFacts(context.Background(), unitU)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	want := task.UnitFacts{Unit: unitU, StartedByMe: true, ClosurePosted: true, Cares: true, Carded: true, LastHuman: t0.Add(-time.Hour)}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	none, ok, err := f.UnitFacts(context.Background(), unitV)
	if err != nil || !ok || none != (task.UnitFacts{Unit: unitV}) {
		t.Fatalf("a unit with no card: %+v %v %v", none, ok, err)
	}
}

func TestOwnLastHumanIsIgnored(t *testing.T) {
	alias := items.Person{Key: "slack:U000AAA", Aliases: []string{"github:ME"}}
	for _, who := range []items.Person{meP, alias} {
		f := cardFacts(cardMap{unitU: {Unit: unitU, LastHuman: &card.Human{Who: who, At: t0}}})
		got, ok, err := f.UnitFacts(context.Background(), unitU)
		if err != nil || !ok || !got.LastHuman.IsZero() {
			t.Fatalf("%s: %+v %v %v", who.Key, got, ok, err)
		}
	}
}

func TestDisabledPluginFactsNotOK(t *testing.T) {
	f := cardFacts(cardMap{unitU: {Unit: unitU, Cares: true}}, unit.GitHub)
	if got, ok, err := f.UnitFacts(context.Background(), unitU); ok || err != nil || got.Cares {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

type countingCards struct {
	cardMap
	reads *int
}

func (c countingCards) Get(ctx context.Context, us []unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	*c.reads++
	return c.cardMap.Get(ctx, us)
}

func TestAPassReadsTheCardsOnce(t *testing.T) {
	reads := 0
	cards := countingCards{cardMap{unitU: {Unit: unitU, StartedByMe: true}, unitV: {Unit: unitV, Cares: true}}, &reads}
	f := coretasks.CardFacts{Cards: cards}
	other := unit.Unit{Provider: "mail", Key: "m-9"}
	pass := &coretasks.Pass{Tasks: []task.Task{{ID: "t-1", Units: []unit.Unit{unitU}}, {ID: "t-2", Units: []unit.Unit{unitV}}}}
	ctx := coretasks.WithPass(context.Background(), pass)
	for _, u := range []unit.Unit{unitU, unitV, unitU} {
		got, ok, err := f.UnitFacts(ctx, u)
		if err != nil || !ok || !got.Carded || got.Unit != u {
			t.Fatalf("%s: %+v %v %v", u, got, ok, err)
		}
	}
	if reads != 1 {
		t.Fatalf("the cards were read %d times", reads)
	}
	if got, ok, err := f.UnitFacts(ctx, other); err != nil || !ok || got.Carded {
		t.Fatalf("a unit outside the pass: %+v %v %v", got, ok, err)
	}
	if reads != 2 {
		t.Fatalf("a unit outside the pass read the cards %d times in all", reads)
	}
}
