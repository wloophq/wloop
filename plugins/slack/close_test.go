package slack

import (
	"context"
	"slices"
	"testing"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// inSlackView says whether u's row is in the Slack view, failing when it has no row.
func (f *fixture) inSlackView(t *testing.T, u unit.Unit) bool {
	t.Helper()
	r, ok := f.row(t, u)
	if !ok {
		t.Fatalf("no row for %s", u)
	}
	return slices.Contains(r.Views, slackrows.ViewSlack)
}

// TestAClosedConversationLeavesTheViewUntilAnItemOpens: a closure, posted or marked, takes a conversation with
// no open item out of the Slack view, though it keeps its row, card and facts; an open item brings it back.
func TestAClosedConversationLeavesTheViewUntilAnItemOpens(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.people(t)
	u := f.thread(t)
	if !f.inSlackView(t, u) {
		t.Fatal("the thread is not in the Slack view")
	}
	if err := f.db.Extraction().MarkClosed(context.Background(), u, f.now); err != nil {
		t.Fatal(err)
	}
	if f.inSlackView(t, u) {
		t.Fatal("a closed thread with no open item stays in the Slack view")
	}
	f.host.Put(items.Item{ID: "i1", Unit: u, Kind: items.Request, Summary: "who ships it?", Origin: unit.Anchor{Unit: u, Ref: "m:i1"},
		Requester: items.Person{Key: "slack:" + other}, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
		Provenance: items.Extracted, CreatedAt: f.now, UpdatedAt: f.now})
	if !f.inSlackView(t, u) {
		t.Fatal("an open item does not bring the closed thread back to the Slack view")
	}
}

// TestUnitClosedUnwatchesAConversationWithNoItem: a closure with no message on a thread no item came from stops
// watching it at once.
func TestUnitClosedUnwatchesAConversationWithNoItem(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.thread(t)
	object := chan1 + "/" + ts(1)
	if o, ok := f.object(t, object); !ok || !o.Watched {
		t.Fatalf("object %+v %v, want watched", o, ok)
	}
	err := (&Plugin{p: f.p}).ApplyEffect(context.Background(), src.EffectRequest{Effect: EffectUnitClosed, Unit: u.Wire(), Draft: &src.Draft{}})
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := f.object(t, object); o.Watched {
		t.Error("the closed thread with no item stays watched")
	}
}
