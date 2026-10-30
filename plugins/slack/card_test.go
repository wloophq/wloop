package slack

import (
	"context"
	"encoding/json"
	"github.com/wloophq/wloop/plugins/slack/store"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

const permalink1 = "https://example.slack.com/archives/C0001/p1700000001000100"

var (
	mePerson    = items.Person{Key: "slack:" + me, Name: "Fer", Aliases: []string{"email:me@example.test"}}
	otherPerson = items.Person{Key: "slack:" + other, Name: "Ana"}
)

func tsAt(n int) time.Time { return time.Unix(int64(1700000000+n), 100_000).UTC() }

// people stores the viewer and another user in the user cache.
func (f *fixture) people(t testing.TB) {
	t.Helper()
	err := f.db.Slack().PutUsers(context.Background(), []store.UserRow{
		{ID: me, Name: "fer", DisplayName: "Fer", Email: "me@example.test", FetchedAt: f.now},
		{ID: other, Name: "Ana", FetchedAt: f.now},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// thread seeds a channel thread whose parent other wrote, replied by the viewer and then by other.
func (f *fixture) thread(t testing.TB) unit.Unit {
	t.Helper()
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(3), ReplyCount: 2, Messages: []dslack.Message{
			{TS: ts(1), User: other, Text: "Can you review <@" + me + ">'s deploy plan &amp; the <https://example.test/doc|rollback doc>?\nDetails follow.",
				Permalink: permalink1},
			{TS: ts(2), ThreadTS: ts(1), User: me, Text: "on it"},
			{TS: ts(3), ThreadTS: ts(1), User: other, Text: "thanks!\nmore", Permalink: permalink1 + "?thread_ts=1"}}}},
	})
	return dslack.ThreadUnit(team, chan1, ts(1))
}

func (f *fixture) ledger() *hosttest.Host { return f.host }

func (f *fixture) state(t testing.TB, u unit.Unit, st store.ExtractionState) {
	t.Helper()
	if err := f.db.Extraction().Save(context.Background(), store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{u: st}}); err != nil {
		t.Fatal(err)
	}
}

func applyOps(t testing.TB, l *hosttest.Host, prov items.Provenance, ops ...items.Op) {
	t.Helper()
	proposed := len(l.Proposals())
	if err := l.Apply(prov, ops...); err != nil || len(l.Proposals()) != proposed {
		t.Fatalf("apply: %v, %d proposals", err, len(l.Proposals())-proposed)
	}
}

func createOp(u unit.Unit, id string, origin int, requester items.Person, waiting ...items.Person) items.Op {
	kind, summary := items.Request, "item "+id
	return items.Op{Op: items.OpCreate, Unit: u, Item: id, Anchor: unit.Anchor{Unit: u, Ref: chan1 + "/" + ts(origin), At: tsAt(origin)},
		Fields: &items.Fields{Kind: &kind, Summary: &summary, Requester: &requester, WaitingOn: &waiting}}
}

// row is u's row as the plugin derives it; false when the plugin lists no row for u.
func (f *fixture) row(t testing.TB, u unit.Unit) (slackrows.Row, bool) {
	t.Helper()
	ctx := context.Background()
	p := f.p.rowsSource()
	listed, err := p.Listed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(listed, u) {
		return slackrows.Row{}, false
	}
	in, err := p.Load(ctx, []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	x, ok := in[u]
	if !ok {
		return slackrows.Row{}, false
	}
	r := slackrows.Derive(x)
	r.Unit = u
	return r, true
}

func (f *fixture) card(t testing.TB, u unit.Unit) src.Card {
	t.Helper()
	r, ok := f.row(t, u)
	if !ok {
		t.Fatalf("no row for %s", u)
	}
	c, err := Card(r)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCardFromFactsAndState(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	f.state(t, u, store.ExtractionState{Verdict: "waits_on_viewer", VerdictWhy: "Ana waits on your review since Tuesday", At: f.now})
	applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 1, otherPerson, mePerson), createOp(u, "e2", 3, mePerson, otherPerson))

	c := f.card(t, u)
	if c.Title != "Can you review @Fer's deploy plan & the rollback doc?" {
		t.Errorf("title %q", c.Title)
	}
	if c.State != "waits_on_viewer" || c.Why != "Ana waits on your review since Tuesday" || c.Link != permalink1 {
		t.Errorf("state %q why %q link %q", c.State, c.Why, c.Link)
	}
	if len(c.WaitsOn) != 2 || c.WaitsOn[0].Key != mePerson.Key || c.WaitsOn[1].Key != otherPerson.Key || !c.Since.Equal(tsAt(1)) {
		t.Errorf("waits on %+v since %v", c.WaitsOn, c.Since)
	}
	if h := c.LastHuman; h == nil || h.Who.Key != otherPerson.Key || h.Who.Name != "Ana" || h.What != "thanks!" || !h.At.Equal(tsAt(3)) {
		t.Errorf("last human %+v", c.LastHuman)
	}
	if c.Label != "#general" {
		t.Errorf("label %q", c.Label)
	}
	if c.TurnsLink != "#unit=slack/"+u.Key || c.Outcome != "" {
		t.Errorf("card %+v", c)
	}
	want := []src.CardSignal{{Key: "items", Value: "2 open", Tone: toneWarn}, {Key: "replies", Value: "2", Tone: toneNeutral},
		{Key: "last activity", Value: tsAt(3).Format("2006-01-02 15:04 UTC"), Tone: toneInfo, At: tsAt(3)}}
	if !slices.Equal(c.Signals, want) {
		t.Errorf("signals %+v", c.Signals)
	}
}

func TestCardTaskFields(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	f.state(t, u, store.ExtractionState{Verdict: "resolved", StartedByViewer: true, ClosurePosted: true, At: f.now})
	c := f.card(t, u)
	if !c.StartedByMe || !c.ClosurePosted || c.Cares {
		t.Fatalf("resolved, no item, not followed: %+v", c)
	}
	applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 1, mePerson, otherPerson))
	if c := f.card(t, u); !c.Cares {
		t.Fatalf("an open item: %+v", c)
	}

	g := newFixture(t)
	g.login(t)
	v := g.thread(t)
	if err := (Watches{g.db.Slack()}).Watch(context.Background(), dslack.ObjectRef{Conversation: chan1, TS: ts(1)}, dslack.ReasonUserFollow); err != nil {
		t.Fatal(err)
	}
	if c := g.card(t, v); !c.Cares || c.StartedByMe || c.ClosurePosted {
		t.Fatalf("followed, other started it: %+v", c)
	}
}

func TestRowsReadUnitClosedFromClosureMarked(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	u := f.thread(t)
	f.state(t, u, store.ExtractionState{Verdict: "needs_closure", StartedByViewer: true, At: f.now})
	l := f.ledger()
	applyOps(t, l, items.Extracted, createOp(u, "e1", 1, mePerson, otherPerson))
	for i, ref := range []string{"draft:7:reply", "draft:8", "draft:9:close"} {
		applyOps(t, l, items.Effects, items.Op{Op: items.OpEvidence, Unit: u, Item: "e1",
			Anchor:   unit.Anchor{Unit: u, Ref: ref, At: tsAt(4 + i)},
			Evidence: &items.Evidence{Kind: items.EvidenceSentDraft, Anchor: unit.Anchor{Unit: u, Ref: ref, At: tsAt(4 + i)}}})
	}
	if c := f.card(t, u); c.ClosurePosted {
		t.Fatalf("a sent draft's anchor is no closure: %+v", c)
	}
	if err := f.db.Extraction().MarkClosed(context.Background(), u, f.now); err != nil {
		t.Fatal(err)
	}
	if c := f.card(t, u); !c.ClosurePosted {
		t.Fatalf("a sent draft's unit.closed: %+v", c)
	}
	f.state(t, u, store.ExtractionState{Verdict: "needs_closure", StartedByViewer: true, At: f.now})
	if c := f.card(t, u); !c.ClosurePosted {
		t.Fatalf("an extraction pass cleared the closure: %+v", c)
	}
}

func TestDMSegmentCard(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: dm1, Kind: dslack.KindIM, IsMember: true, DMUser: other}},
		DMs: []dslack.DMWindow{{Conversation: dm1, Latest: ts(6), Messages: []dslack.Message{
			{TS: ts(5), User: other, Text: "the release?"}, {TS: ts(6), User: me, Text: "tomorrow"}}}},
	})
	segs, err := f.p.src.segments.(dslack.SegmentWriter).Apply(context.Background(), team, dm1, []dslack.Span{{FromTS: ts(5)}})
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments %v %v", segs, err)
	}
	u := segs[0]
	c := f.card(t, u)
	if c.Title != "DM with Ana" || c.State != "new" {
		t.Errorf("before extraction: %q %q", c.Title, c.State)
	}
	f.state(t, u, store.ExtractionState{Verdict: "fyi", Topic: "release date", At: f.now})
	c = f.card(t, u)
	if c.Title != "DM with Ana: release date" || c.State != "fyi" {
		t.Errorf("title %q state %q", c.Title, c.State)
	}
	if h := c.LastHuman; h == nil || h.Who.Key != otherPerson.Key || h.What != "the release?" {
		t.Errorf("last human %+v", h)
	}
	if r, _ := f.row(t, u); r.Label != "DM with Ana" || !slices.Equal(r.Facets[slackrows.FacetConversation], []string{"DM with Ana"}) {
		t.Errorf("row %+v", r)
	}
}

func TestCardBeforeExtraction(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	c := f.card(t, u)
	if c.State != "new" || c.Why != "not read yet" || c.Title == "" || c.Link != permalink1 || len(c.WaitsOn) != 0 || !c.Since.IsZero() {
		t.Fatalf("card %+v", c)
	}
	if c.StartedByMe || c.ClosurePosted || c.Cares || c.LastHuman == nil {
		t.Fatalf("the card's flags %+v", c)
	}
}

func TestSlackRowsAreGeneric(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	f.state(t, u, store.ExtractionState{Verdict: "waits_on_others", VerdictWhy: "Ana owes the doc", At: f.now})
	applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 1, mePerson, otherPerson))
	r, ok := f.row(t, u)
	if !ok {
		t.Fatal("no row")
	}
	if r.Kind != slackrows.KindWaitsOnOthers || r.Label != "#general" || r.Link != permalink1 {
		t.Errorf("row %+v", r)
	}
	s := r.Summary
	if s.WaitsOn != "Ana" || !s.Since.Equal(tsAt(1)) || s.Closed || s.OpenItems != 1 || s.Reason != "Ana owes the doc" ||
		s.Line != "waits on others · 1 open item" {
		t.Errorf("summary %+v", s)
	}
	if !slices.Equal(r.Views, []string{slackrows.ViewSlack}) || len(r.Items) != 1 || r.Items[0].ID != "e1" ||
		!slices.Equal(r.Items[0].WaitingOn, []string{"Ana"}) {
		t.Errorf("views %v items %+v", r.Views, r.Items)
	}
	var keys map[string]any
	if err := json.Unmarshal(r.Payload, &keys); err != nil {
		t.Fatal(err)
	}
	for _, pr := range []string{"author", "repo", "head", "approved", "reasons", "workspace", "brief", "facts"} {
		if _, ok := keys[pr]; ok {
			t.Errorf("the payload carries the pull request field %q", pr)
		}
	}
	if p, ok := slackrows.PayloadOf(r); !ok || p.Channel != "general" || p.Verdict != "waits_on_others" {
		t.Errorf("payload %+v %v", p, ok)
	}
	if !manifestSituation(t, string(r.Kind)) {
		t.Errorf("the manifest declares no situation %s", r.Kind)
	}
	if !slices.Contains(r.Views, slackrows.ViewSlack) {
		t.Error("an open row is out of the Slack view")
	}
	f.state(t, u, store.ExtractionState{Verdict: "resolved", At: f.now})
	applyOps(t, f.ledger(), items.Extracted, items.Op{Op: items.OpResolve, Unit: u, Item: "e1", Resolution: items.Done,
		Anchor: unit.Anchor{Unit: u, Ref: chan1 + "/" + ts(3), At: tsAt(3)}})
	if r, _ := f.row(t, u); !r.Summary.Closed || r.Summary.WaitsOn != "nobody" {
		t.Errorf("resolved row %+v", r.Summary)
	}
	in, err := f.p.rowsSource().Load(context.Background(), []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ := f.p.snapshot(u, in[u], nil, f.now); snap.Situation == nil || snap.Situation.Kind != kindDone {
		t.Errorf("resolved snapshot situation %+v", snap.Situation)
	}
}

func TestUserGroupsAreNamedNotShownByID(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{
			{TS: ts(1), User: other, Text: "<!subteam^S0000000001|@team-a> can you look?"}}}},
	})
	u := dslack.ThreadUnit(team, chan1, ts(1))
	f.state(t, u, store.ExtractionState{Verdict: "waits_on_others", At: f.now})
	applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 1, otherPerson, items.Person{Key: "group:S0000000001", Name: "S0000000001"},
		items.Person{Key: "group:S0000000002", Name: "S0000000002"}))
	r, ok := f.row(t, u)
	if !ok {
		t.Fatal("no row")
	}
	if want := []string{"@team-a", dslack.UnknownGroup}; !slices.Equal(r.Items[0].WaitingOn, want) || r.Summary.WaitsOn != "@team-a, "+dslack.UnknownGroup {
		t.Errorf("waiting on %v, summary %q", r.Items[0].WaitingOn, r.Summary.WaitsOn)
	}
	if got := r.Facets[slackrows.FacetWaitsOn]; !slices.Equal(got, []string{"@team-a", dslack.UnknownGroup}) {
		t.Errorf("waits-on facet %v", got)
	}
	if got := r.Facets[slackrows.FacetChannel]; !slices.Equal(got, []string{"general"}) {
		t.Errorf("channel facet %v", got)
	}
	in, err := f.p.rowsSource().Load(context.Background(), []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := f.p.snapshot(u, in[u], nil, f.now)
	if got := snap.FacetValues[slackrows.FacetWaitsOn]; !slices.Equal(got, []string{"@team-a", dslack.UnknownGroup}) {
		t.Errorf("reported waits-on facet %v", got)
	}
	if got := snap.Facets[slackrows.FacetChannel]; got != "general" {
		t.Errorf("reported channel facet %q", got)
	}
	c := f.card(t, u)
	if len(c.WaitsOn) != 2 || c.WaitsOn[0].Name != "@team-a" || c.WaitsOn[1].Name != dslack.UnknownGroup {
		t.Errorf("card waits on %+v", c.WaitsOn)
	}
}

func TestSlackLinksAreDesktopPermalinks(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: "C0009", Name: "elsewhere", Kind: dslack.KindChannel, IsMember: true},
			{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}, {ID: dm1, Kind: dslack.KindIM, IsMember: true, DMUser: other}},
		Threads: []dslack.Thread{
			{Conversation: "C0009", TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "x", Permalink: "https://acme.slack.com/archives/C0009/p1700000001000100"}}},
			{Conversation: chan1, TS: ts(2), LatestReply: ts(3), ReplyCount: 1, Messages: []dslack.Message{
				{TS: ts(2), User: me, Text: "parent"}, {TS: ts(3), ThreadTS: ts(2), User: other, Text: "reply"}}}},
		DMs: []dslack.DMWindow{{Conversation: dm1, Latest: ts(6), Messages: []dslack.Message{{TS: ts(5), User: other, Text: "the release?"}}}},
	})
	thread, _ := f.row(t, dslack.ThreadUnit(team, chan1, ts(2)))
	if want := "https://acme.slack.com/archives/" + chan1 + "/p1700000002000100"; thread.Link != want {
		t.Errorf("thread link %q, want %q", thread.Link, want)
	}
	c := f.card(t, dslack.ThreadUnit(team, chan1, ts(2)))
	if want := "https://acme.slack.com/archives/" + chan1 + "/p1700000003000100?thread_ts=" + ts(2) + "&cid=" + chan1; c.LastHuman == nil || c.LastHuman.Link != want {
		t.Errorf("reply link %+v, want %q", c.LastHuman, want)
	}
	segs, err := f.p.src.segments.(dslack.SegmentWriter).Apply(context.Background(), team, dm1, []dslack.Span{{FromTS: ts(5)}})
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments %v %v", segs, err)
	}
	dm, _ := f.row(t, segs[0])
	if want := "https://acme.slack.com/archives/" + dm1 + "/p1700000005000100"; dm.Link != want {
		t.Errorf("DM link %q, want %q", dm.Link, want)
	}
}

// manifestSituation says whether the manifest declares the situation kind.
func manifestSituation(t testing.TB, kind string) bool {
	t.Helper()
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(s.Situations, func(sit manifest.Situation) bool { return sit.Name == kind })
}

func TestSlackSnapshotsLeaveTheLoopActivityToTheHost(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	in, err := f.p.rowsSource().Load(context.Background(), []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := f.p.snapshot(u, in[u], nil, f.now)
	var keys map[string]any
	if err := json.Unmarshal(snap.Data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"turns", "next"} {
		if _, ok := keys[k]; ok {
			t.Errorf("the snapshot's data carries %q, which the host overlays", k)
		}
	}
}
