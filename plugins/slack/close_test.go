package slack

import (
	"context"
	"slices"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
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
	if err := f.db.Extraction().MarkClosed(context.Background(), u, f.now, ""); err != nil {
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

// reply stores a reply of user at ts(n) in the fixture's thread.
func (f *fixture) reply(t *testing.T, user string, n int) {
	t.Helper()
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(n), ReplyCount: n - 1, Messages: []dslack.Message{
			{TS: ts(n), ThreadTS: ts(1), User: user, Text: "one more thing"}}}},
	})
}

// TestSomeoneElsesMessageAfterTheClosureReopensTheConversation: a marked closure holds until someone else posts
// after the newest message it saw, whatever the clock said when it applied, which brings the conversation back to the Slack view and its closure fact back to false; your own
// message after it does not.
func TestSomeoneElsesMessageAfterTheClosureReopensTheConversation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.now = tsAt(4)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	farAhead := src.Draft{Evidence: src.Evidence{At: tsAt(3).Add(365 * 24 * time.Hour)}}
	if err := (&Plugin{p: f.p}).ApplyEffect(context.Background(), src.EffectRequest{Effect: EffectUnitClosed, Unit: u.Wire(), Draft: &farAhead}); err != nil {
		t.Fatal(err)
	}
	f.reply(t, me, 6)
	if f.inSlackView(t, u) {
		t.Fatal("your own message after the closure brought the conversation back")
	}
	f.reply(t, other, 7)
	if !f.inSlackView(t, u) {
		t.Fatal("someone else's message after the closure does not bring the conversation back")
	}
	if got, _ := f.facts(t, u); got["slack.closure_posted"] != false {
		t.Errorf("after a new message: closure_posted %v, want false", got["slack.closure_posted"])
	}
}

// TestAClosureOfAUnitWithNoMessageHoldsUntilALaterOne: unit.closed on a unit with no stored message keeps the ts
// of when it applied, so a message posted before it that comes in later keeps it closed.
func TestAClosureOfAUnitWithNoMessageHoldsUntilALaterOne(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := dslack.ThreadUnit(team, chan1, ts(1))
	d := src.Draft{Evidence: src.Evidence{At: tsAt(5)}}
	if err := (&Plugin{p: f.p}).ApplyEffect(context.Background(), src.EffectRequest{Effect: EffectUnitClosed, Unit: u.Wire(), Draft: &d}); err != nil {
		t.Fatal(err)
	}
	st, err := f.db.Extraction().States(context.Background(), []unit.Unit{u})
	if err != nil || !dslack.TSTime(st[u].ClosedTS).Equal(tsAt(5).Truncate(time.Microsecond)) {
		t.Fatalf("closed at %q (%v), want the ts of %v", st[u].ClosedTS, err, tsAt(5))
	}
}
