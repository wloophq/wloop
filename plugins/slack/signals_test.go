package slack

import (
	"context"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

const signalsRepliedAfter = repliedAfter

// signalsFixture is a logged-in plugin at t0.
func signalsFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.login(t)
	f.people(t)
	return f
}

// signalThread seeds a thread other asked on at ts(1), the ask carrying askReactions, with msgs as its replies.
func (f *fixture) signalThread(t *testing.T, askReactions []dslack.Reaction, msgs ...dslack.Message) unit.Unit {
	t.Helper()
	all := append([]dslack.Message{{TS: ts(1), User: other, Text: "can you check the rollout?", Reactions: askReactions}}, msgs...)
	for i := range all[1:] {
		all[i+1].ThreadTS = ts(1)
	}
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: all[len(all)-1].TS, ReplyCount: len(all) - 1, Messages: all}},
	})
	u := dslack.ThreadUnit(team, chan1, ts(1))
	f.host.Put(items.Item{ID: "i1", Unit: u, Kind: items.Request, Summary: "check the rollout",
		Origin: unit.Anchor{Unit: u, Ref: chan1 + "/" + ts(1), At: tsAt(1)}, Requester: otherPerson, WaitingOn: []items.Person{mePerson},
		Basis: items.DMAddressee, State: items.Open, Confidence: items.High, Provenance: items.Extracted, CreatedAt: tsAt(1), UpdatedAt: tsAt(1)})
	return u
}

// signals are u's reported link signals at f.now, by name, and when the next of them changes with time alone.
func (f *fixture) signals(t *testing.T, u unit.Unit) (map[string]src.Signal, time.Time) {
	t.Helper()
	ctx := context.Background()
	in, err := f.p.rowsSource().Load(ctx, []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	if in[u] == nil {
		t.Fatalf("%s loads no row", u)
	}
	snap, next := f.p.snapshot(u, in[u], nil, f.now)
	out := map[string]src.Signal{}
	for _, g := range snap.Signals {
		out[g.Name] = g
	}
	return out, next
}

func TestAskedReactedIsACheckMarkOfWhoAskedOnALaterMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		reply dslack.Message
		want  bool
	}{
		{"the asker checks the viewer's answer", dslack.Message{TS: ts(2), User: me, Text: "done",
			Reactions: []dslack.Reaction{{Name: "white_check_mark", Users: []string{other}}}}, true},
		{"a skin tone of a check mark", dslack.Message{TS: ts(2), User: me, Text: "done",
			Reactions: []dslack.Reaction{{Name: "heavy_check_mark::skin-tone-2", Users: []string{other}}}}, true},
		{"the viewer checks their own answer", dslack.Message{TS: ts(2), User: me, Text: "done",
			Reactions: []dslack.Reaction{{Name: "white_check_mark", Users: []string{me}}}}, false},
		{"another emoji", dslack.Message{TS: ts(2), User: me, Text: "done",
			Reactions: []dslack.Reaction{{Name: "eyes", Users: []string{other}}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := signalsFixture(t)
			u := f.signalThread(t, nil, c.reply)
			got, _ := f.signals(t, u)
			if got[signalAskedReacted].On != c.want {
				t.Fatalf("asked_reacted %v, want %v", got[signalAskedReacted].On, c.want)
			}
		})
	}
}

func TestAskedReactedIgnoresACheckMarkOnTheAskItself(t *testing.T) {
	t.Parallel()
	f := signalsFixture(t)
	u := f.signalThread(t, []dslack.Reaction{{Name: "white_check_mark", Users: []string{other}}})
	if got, _ := f.signals(t, u); got[signalAskedReacted].On {
		t.Fatal("a check mark on the ask itself reads as the ask settled")
	}
}

func TestRepliedLastHoldsOnceTheViewersReplyIsQuietPastTheSetting(t *testing.T) {
	t.Parallel()
	f := signalsFixture(t)
	f.now = tsAt(2).Add(signalsRepliedAfter - time.Minute)
	u := f.signalThread(t, nil, dslack.Message{TS: ts(2), User: me, Text: "here it is"})
	got, next := f.signals(t, u)
	if got[signalRepliedLast].On {
		t.Fatal("replied_last holds before the reply is quiet for the setting")
	}
	if want := tsAt(2).Add(signalsRepliedAfter); !next.Equal(want) {
		t.Fatalf("next %v, want %v, when the reply has been quiet for the setting", next, want)
	}
	f.now = tsAt(2).Add(signalsRepliedAfter)
	got, _ = f.signals(t, u)
	if g := got[signalRepliedLast]; !g.On || !g.Since.Equal(tsAt(2)) {
		t.Fatalf("replied_last %+v, want on since the reply", g)
	}
}

func TestRepliedLastIsOffWhenSomeoneElseSpokeLastOrNothingIsOpen(t *testing.T) {
	t.Parallel()
	f := signalsFixture(t)
	f.now = tsAt(3).Add(2 * signalsRepliedAfter)
	u := f.signalThread(t, nil, dslack.Message{TS: ts(2), User: me, Text: "here it is"}, dslack.Message{TS: ts(3), User: other, Text: "one more thing"})
	if got, _ := f.signals(t, u); got[signalRepliedLast].On {
		t.Fatal("replied_last holds after someone else spoke")
	}
	g := signalsFixture(t)
	g.now = f.now
	v := g.signalThread(t, nil, dslack.Message{TS: ts(2), User: me, Text: "here it is"})
	it := g.host.Items(v)[0]
	it.State, it.Resolution = items.Resolved, items.Done
	g.host.Put(it)
	if got, _ := g.signals(t, v); got[signalRepliedLast].On || got[signalAskedReacted].On {
		t.Fatal("a unit with no open item reports a closing signal")
	}
}
