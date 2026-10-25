package sqlite

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.Cards", "cardStore.Get")
	writes = append(writes,
		writeCase{
			method: "cardStore.Save",
			call: func(ctx context.Context, e *noteEnv) error {
				_, err := e.db.Cards().Save(ctx, sampleCard(slackThread, "t"), noteAt)
				return err
			},
			topics: port.TopicCards, units: []unit.Unit{slackThread},
		},
		writeCase{
			method: "cardStore.Save",
			name:   "an equal card",
			seed: func(t *testing.T, e *noteEnv) {
				if _, err := e.db.Cards().Save(context.Background(), sampleCard(slackThread, "t"), noteAt); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error {
				_, err := e.db.Cards().Save(ctx, sampleCard(slackThread, "t"), noteAt.Add(time.Hour))
				return err
			},
		},
	)
	hotPlans = append(hotPlans,
		hotPlan{name: "card of a unit", query: cardOfUnitSQL, args: 2, want: []string{"SEARCH unit_cards USING PRIMARY KEY (provider=? AND unit_key=?)"}})
}

func sampleCard(u unit.Unit, title string) card.Card {
	at := time.Date(2026, 10, 22, 12, 0, 0, 500, time.UTC)
	return card.Card{Unit: u, Title: title, State: "open", Signals: []card.Signal{{Key: "checks", Value: "green", Tone: "good"}},
		LastHuman: &card.Human{Who: items.Person{Key: "slack:U000BBB", Name: "b"}, What: "replied", At: at, Link: "https://example.test/1"},
		Why:       "asked", WaitsOn: []items.Person{{Key: "slack:U000AAA"}}, Since: at, Link: "https://example.test",
		StartedByMe: true, Cares: true, Mute: &card.Mute{Until: at.Add(time.Hour)}}
}

func TestCardsStoreRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, path := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 500, time.UTC)
	c := sampleCard(slackThread, "t")
	changed, err := db.Cards().Save(ctx, c, at)
	if err != nil || !changed {
		t.Fatalf("first save: changed %v, %v", changed, err)
	}
	if changed, err := db.Cards().Save(ctx, c, at.Add(time.Minute)); err != nil || changed {
		t.Fatalf("equal save: changed %v, %v", changed, err)
	}
	c2 := c
	c2.Outcome, c2.ClosurePosted = items.Done, true
	if changed, err := db.Cards().Save(ctx, c2, at.Add(time.Hour)); err != nil || !changed {
		t.Fatalf("changed save: changed %v, %v", changed, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Cards().Get(ctx, []unit.Unit{slackThread, ghPull})
	if err != nil {
		t.Fatal(err)
	}
	want := map[unit.Unit]card.Snapshot{slackThread: {Card: c2, At: at.Add(time.Hour)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestCardsStoreRefusesNoUnit(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	if _, err := db.Cards().Save(context.Background(), card.Card{Title: "x"}, time.Now()); err == nil {
		t.Error("saved a card without a unit")
	}
}
