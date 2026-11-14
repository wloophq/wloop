package slack

import (
	"context"
	"slices"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// snapFacts are u's reported facts at f.now, by name, as the host reads them.
func (f *fixture) snapFacts(t *testing.T, u unit.Unit) map[string]any {
	t.Helper()
	in, err := f.p.rowsSource().Load(context.Background(), []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	if in[u] == nil {
		t.Fatalf("%s loads no row", u)
	}
	snap, _ := f.p.snapshot(u, in[u], nil, f.now)
	out := map[string]any{}
	for _, fc := range snap.Facts {
		out[fc.Name] = resolve(t, fc.Value, f.now)
	}
	return out
}

// askedOfOther makes the thread's one item the viewer's ask, waiting on other.
func (f *fixture) askedOfOther(t *testing.T, u unit.Unit) {
	t.Helper()
	it := f.host.Items(u)[0]
	it.Requester, it.WaitingOn = mePerson, []items.Person{otherPerson}
	f.host.Put(it)
}

func TestWhoSpokeLastReadsTheNewestPersonsMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		replies       []dslack.Message
		byMe, toMe    bool
		reactedToLast bool
	}{
		{"the viewer answered last", []dslack.Message{{TS: ts(2), User: me, Text: "on it"}}, true, false, false},
		{"someone else mentions the viewer last", []dslack.Message{{TS: ts(2), User: me, Text: "on it"},
			{TS: ts(3), User: other, Text: "<@" + me + "> can you approve it?", Mentions: []string{me}}}, false, true, false},
		{"someone else spoke last in a channel without a mention", []dslack.Message{{TS: ts(2), User: other, Text: "pushed a fix"}}, false, false, false},
		{"a bot's message is not a person's", []dslack.Message{{TS: ts(2), User: me, Text: "on it"},
			{TS: ts(3), User: "U0000000777", Text: "<@" + me + "> build passed", Mentions: []string{me}}}, true, false, false},
		{"the viewer reacted to the other side's newest message", []dslack.Message{{TS: ts(2), User: other, Text: "merged",
			Reactions: []dslack.Reaction{{Name: "+1::skin-tone-2", Users: []string{me}}}}}, false, false, true},
		{"someone else's reaction is no acknowledgement of the viewer's", []dslack.Message{{TS: ts(2), User: other, Text: "merged",
			Reactions: []dslack.Reaction{{Name: "+1", Users: []string{other}}}}}, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := signalsFixture(t)
			f.knownUser(t, "U0000000777", true)
			u := f.signalThread(t, nil, c.replies...)
			got := f.snapFacts(t, u)
			if got["slack.last_by_me"] != c.byMe || got["slack.last_to_me"] != c.toMe || got["slack.reacted_to_last"] != c.reactedToLast {
				t.Fatalf("last_by_me %v, last_to_me %v, reacted_to_last %v; want %v, %v, %v",
					got["slack.last_by_me"], got["slack.last_to_me"], got["slack.reacted_to_last"], c.byMe, c.toMe, c.reactedToLast)
			}
			sig, _ := f.signals(t, u)
			if sig[signalLastByMe].On != c.byMe || sig[signalLastToMe].On != c.toMe {
				t.Fatalf("signals last_by_me %v, last_to_me %v; want %v, %v", sig[signalLastByMe].On, sig[signalLastToMe].On, c.byMe, c.toMe)
			}
		})
	}
}

func TestTheNewestMessageIsToTheViewerInAOneToOneDMOrWhenItMentionsThem(t *testing.T) {
	t.Parallel()
	const third = "U0000000003"
	cases := []struct {
		name string
		kind dslack.ConversationKind
		last dslack.Message
		want bool
	}{
		{"the peer of a direct conversation", dslack.KindIM, dslack.Message{User: other, Text: "what about the flag?"}, true},
		{"a third person in a group conversation", dslack.KindMPIM, dslack.Message{User: third, Text: "what about the flag?"}, false},
		{"a third person who mentions the viewer in a group conversation", dslack.KindMPIM,
			dslack.Message{User: third, Text: "<@" + me + "> what about the flag?", Mentions: []string{me}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := signalsFixture(t)
			const conv = "G0000000001"
			last := c.last
			last.TS, last.ThreadTS = ts(2), ts(1)
			f.seed(t, dslack.Facts{
				Conversations: []dslack.Conversation{{ID: conv, Kind: c.kind, IsMember: true}},
				Threads: []dslack.Thread{{Conversation: conv, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{
					{TS: ts(1), User: me, Text: "shall we ship it?"}, last}}},
			})
			u := dslack.ThreadUnit(team, conv, ts(1))
			if got := f.snapFacts(t, u)["slack.last_to_me"]; got != c.want {
				t.Fatalf("last_to_me %v, want %v", got, c.want)
			}
		})
	}
}

func TestAResolvedItemNobodyButItsAskerAnsweredIsUnanswered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		replies []dslack.Message
		want    int
	}{
		{"only the asker followed up", []dslack.Message{{TS: ts(2), User: other, Text: "anyone?"}}, 1},
		{"someone else answered", []dslack.Message{{TS: ts(2), User: me, Text: "done"}}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := signalsFixture(t)
			u := f.signalThread(t, nil, c.replies...)
			it := f.host.Items(u)[0]
			it.State, it.Resolution = items.Resolved, items.Answered
			f.host.Put(it)
			if got := f.snapFacts(t, u); got["slack.items_resolved_unanswered"] != c.want {
				t.Fatalf("items_resolved_unanswered %v, want %d", got["slack.items_resolved_unanswered"], c.want)
			}
		})
	}
}

func TestTheWaitedOnPeoplesMovesSinceTheAskAreFacts(t *testing.T) {
	t.Parallel()
	at := func(n int) string { return tsAt(n).UTC().Format(time.RFC3339Nano) }
	cases := []struct {
		name      string
		askReacts []dslack.Reaction
		replies   []dslack.Message
		posted    []string
		reactions []string
	}{
		{"nothing since the ask", nil, nil, nil, nil},
		{"they replied twice, the newest counts", nil, []dslack.Message{{TS: ts(2), User: other, Text: "next week"}, {TS: ts(3), User: other, Text: "still on it"}},
			[]string{"i1 slack:" + other + " " + at(3)}, nil},
		{"they reacted on the ask", []dslack.Reaction{{Name: "hourglass::skin-tone-3", Users: []string{other}}}, nil, nil,
			[]string{"i1 slack:" + other + " " + at(1) + " hourglass"}},
		{"the viewer's own reaction is not theirs", []dslack.Reaction{{Name: "eyes", Users: []string{me}}}, nil, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := signalsFixture(t)
			f.now = tsAt(1)
			u := f.signalThread(t, c.askReacts, c.replies...)
			f.askedOfOther(t, u)
			got := f.snapFacts(t, u)
			if p := listOf(got["slack.waited_posted"]); !slices.Equal(p, c.posted) {
				t.Errorf("waited_posted %v, want %v", p, c.posted)
			}
			if r := listOf(got["slack.waited_reactions"]); !slices.Equal(r, c.reactions) {
				t.Errorf("waited_reactions %v, want %v", r, c.reactions)
			}
		})
	}
}

func TestThePeopleAMessageCannotMentionAreAFact(t *testing.T) {
	t.Parallel()
	f := signalsFixture(t)
	const bot = "U0000000777"
	f.knownUser(t, bot, true)
	u := f.signalThread(t, nil, dslack.Message{TS: ts(2), User: me, Text: "<@" + bot + "> rerun the scorecard", Mentions: []string{bot}})
	it := f.host.Items(u)[0]
	it.WaitingOn = []items.Person{{Key: "slack:" + bot}, {Key: "group:the platform team"}, {Key: "group:S0000000001"}, otherPerson}
	f.host.Put(it)
	want := []string{"slack:" + bot, "group:the platform team"}
	if got := listOf(f.snapFacts(t, u)["slack.unmentionable"]); !slices.Equal(got, want) {
		t.Fatalf("unmentionable %v, want the bot and the team named by text, not the user group nor a person", got)
	}
}

func listOf(v any) []string {
	l, _ := v.([]string)
	if len(l) == 0 {
		return nil
	}
	return l
}

func TestAUnitIsExtractedOnceTheExtractionReadItsNewestMessage(t *testing.T) {
	t.Parallel()
	f := signalsFixture(t)
	u := f.signalThread(t, nil, dslack.Message{TS: ts(2), User: me, Text: "thanks"})
	if got := f.snapFacts(t, u)["slack.extracted"]; got != false {
		t.Fatalf("a unit no extraction read: extracted %v", got)
	}
	save := func(last string) {
		t.Helper()
		if err := f.db.Extraction().Save(context.Background(), store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{u: {LastTS: last, At: tsAt(3)}}}); err != nil {
			t.Fatal(err)
		}
	}
	save(ts(1))
	if got := f.snapFacts(t, u)["slack.extracted"]; got != false {
		t.Fatalf("the extraction read up to the ask, not your newer reply: extracted %v", got)
	}
	if sig, _ := f.signals(t, u); !sig[signalExtractionPending].On {
		t.Fatal("extraction_pending is off while your newest reply waits for the extraction")
	}
	save(ts(2))
	if got := f.snapFacts(t, u)["slack.extracted"]; got != true {
		t.Fatalf("the extraction read the newest message: extracted %v", got)
	}
	if sig, _ := f.signals(t, u); sig[signalExtractionPending].On {
		t.Fatal("extraction_pending is on once the extraction read the newest message")
	}
}

func TestAnEditOrAPersonsMessageAfterTheExtractionLeavesTheUnitUnextracted(t *testing.T) {
	t.Parallel()
	const bot = "U0000000777"
	cases := []struct {
		name  string
		reply dslack.Message
		want  bool
	}{
		{"a reply the extraction read", dslack.Message{TS: ts(2), User: me, Text: "thanks"}, true},
		{"that reply edited after the pass", dslack.Message{TS: ts(2), User: me, Text: "thanks, shipping", EditedTS: ts(9)}, false},
		{"a bot's message after the pass", dslack.Message{TS: ts(3), User: bot, Text: "build passed"}, true},
		{"a person's message after the pass", dslack.Message{TS: ts(3), User: other, Text: "one more thing"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := signalsFixture(t)
			f.knownUser(t, bot, true)
			u := f.signalThread(t, nil, c.reply)
			if err := f.db.Extraction().Save(context.Background(), store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{u: {LastTS: ts(2), At: tsAt(5)}}}); err != nil {
				t.Fatal(err)
			}
			if got := f.snapFacts(t, u)["slack.extracted"]; got != c.want {
				t.Fatalf("extracted %v, want %v", got, c.want)
			}
		})
	}
}

func TestAReactionIsDatedWhenThePluginFirstSawIt(t *testing.T) {
	t.Parallel()
	f := signalsFixture(t)
	u := f.signalThread(t, nil)
	f.askedOfOther(t, u)
	f.now = tsAt(500)
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(1), Messages: []dslack.Message{
		{TS: ts(1), User: other, Text: "can you check the rollout?", Reactions: []dslack.Reaction{{Name: "+1", Users: []string{other}}}}}}}})
	f.now = tsAt(900)
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(1), Messages: []dslack.Message{
		{TS: ts(1), User: other, Text: "can you check the rollout?", Reactions: []dslack.Reaction{{Name: "+1", Users: []string{other}}}}}}}})
	want := []string{"i1 slack:" + other + " " + tsAt(500).UTC().Format(time.RFC3339Nano) + " +1"}
	if got := listOf(f.snapFacts(t, u)["slack.waited_reactions"]); !slices.Equal(got, want) {
		t.Fatalf("waited_reactions %v, want the +1 dated when first seen, %v", got, want)
	}
}
