package slack

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

var brt = time.FixedZone("BRT", -3*60*60)

const factsEndedAfter = 3 * 24 * time.Hour

// monday is 10:00 BRT on a Monday.
var monday = time.Date(2026, 3, 2, 10, 0, 0, 0, brt)

var (
	factsThread = dslack.ThreadUnit("T0001", "C0001", "1700000000.000100")
	factsOther  = dslack.ThreadUnit("T0001", "C0001", "1700000000.000200")
	factsDM     = dslack.Key{Team: "T0001", Conversation: "D0001", Segment: "s1"}.Unit()
)

func newFactsFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.now = monday
	f.cfg.Slack.EndedAfter = config.Days(factsEndedAfter)
	f.build(t)
	return f
}

// listed makes each of us load a row: the extraction read it at monday.
func (f *fixture) listed(t *testing.T, us ...unit.Unit) {
	t.Helper()
	states := map[unit.Unit]store.ExtractionState{}
	for _, u := range us {
		states[u] = store.ExtractionState{At: monday}
	}
	if err := f.db.Extraction().Save(context.Background(), store.ExtractionSave{States: states}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) item(u unit.Unit, id string, s items.State, created time.Time, owner ...items.Person) {
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "s " + id, Origin: unit.Anchor{Unit: u, Ref: "m:" + id},
		Requester: items.Person{Key: "slack:U0009"}, Basis: items.NoBasis, State: s, Confidence: items.High,
		Provenance: items.Extracted, Assignees: owner, CreatedAt: created, UpdatedAt: created}
	if s == items.Resolved {
		it.Resolution = items.Done
	}
	f.host.Put(it)
}

func hostTask(id, category string, us ...unit.Unit) src.Task {
	t := src.Task{ID: id, Title: "a", Category: category}
	for _, u := range us {
		t.Units = append(t.Units, u.Wire())
	}
	return t
}

// facts are u's reported facts as the host resolves them at f.now, and when
// the next of them changes with time alone.
func (f *fixture) facts(t *testing.T, u unit.Unit) (map[string]any, time.Time) {
	t.Helper()
	ctx := context.Background()
	in, err := f.p.rowsSource().Load(ctx, []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	if in[u] == nil {
		t.Fatalf("%s loads no row", u)
	}
	tasks, err := f.p.tasks(ctx, []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	snap, next := f.p.snapshot(u, in[u], tasks[u.Wire()], f.now)
	out := map[string]any{}
	for _, fc := range snap.Facts {
		out[fc.Name] = resolve(t, fc.Value, f.now)
	}
	return out, next
}

// resolve is v as the host reads it at now.
func resolve(t *testing.T, v src.Value, now time.Time) any {
	t.Helper()
	switch v.Kind {
	case src.KindBool:
		return v.Bool
	case src.KindInt:
		return int(v.Int)
	case src.KindString:
		return v.String
	case src.KindTime:
		return v.Time
	case src.KindAfter:
		if v.Unit != src.Calendar {
			t.Fatalf("an After of unit %d", v.Unit)
		}
		return !v.Time.IsZero() && now.Sub(v.Time) > v.Threshold
	}
	t.Fatalf("a value of kind %d", v.Kind)
	return nil
}

func TestSlackLiveTaskReadsTheHostsTasks(t *testing.T) {
	t.Parallel()
	f := newFactsFixture(t)
	f.listed(t, factsThread, factsOther, factsDM)
	f.host.SetTasks(hostTask("e-01", "next", factsThread, unit.Unit{Provider: "chat", Key: "x"}), hostTask("e-02", "waiting", factsDM),
		hostTask("e-03", "done", factsOther), hostTask("e-04", "dropped", factsOther))
	for u, want := range map[unit.Unit]bool{factsThread: true, factsDM: true, factsOther: false} {
		if got, _ := f.facts(t, u); got["slack.live_task"] != want {
			t.Errorf("%s live_task %v, want %v", u, got["slack.live_task"], want)
		}
	}
}

func TestSlackFactsAreTheManifestsFacts(t *testing.T) {
	t.Parallel()
	f := newFactsFixture(t)
	f.listed(t, factsThread)
	got, _ := f.facts(t, factsThread)
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	var want, names []string
	for _, d := range s.Facts {
		want = append(want, d.Name)
	}
	for k := range got {
		names = append(names, k)
	}
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("facts %v, want the manifest's %v", names, want)
	}
}

func TestSlackFactsReadTheExtractionTheConversationAndTheItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFactsFixture(t)
	if err := f.db.Slack().PutConversations(ctx, []store.ConversationRow{{ID: "C0001", Team: "T0001", Name: "general", Kind: string(dslack.KindChannel)},
		{ID: "D0001", Team: "T0001", Kind: string(dslack.KindMPIM)}}); err != nil {
		t.Fatal(err)
	}
	f.listed(t, factsDM)
	if err := f.db.Extraction().Save(ctx, store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{
		factsThread: {StartedByViewer: true, At: monday}, factsOther: {ClosurePosted: true, At: monday}}}); err != nil {
		t.Fatal(err)
	}
	f.item(factsThread, "i1", items.Open, monday.Add(-72*time.Hour))
	f.item(factsThread, "i2", items.Open, monday.Add(-time.Hour))
	f.item(factsThread, "i3", items.Open, monday.Add(-200*time.Hour), items.Person{Key: "slack:U0001"})
	f.item(factsThread, "i4", items.Resolved, monday.Add(-200*time.Hour))
	f.host.SetTasks(hostTask("e-01", "next", factsThread))
	got, _ := f.facts(t, factsThread)
	for k, v := range map[string]any{
		"slack.channel": "C0001", "slack.conversation": "channel", "slack.started_by_me": true, "slack.closure_posted": false,
		"slack.live_task": true, "slack.items_open": 3, "slack.items_unowned": 2, "slack.items_unowned_age_days": 1,
	} {
		if got[k] != v {
			t.Errorf("thread %s = %#v, want %#v", k, got[k], v)
		}
	}
	g, _ := f.facts(t, factsOther)
	if g["slack.closure_posted"] != true || g["slack.live_task"] != false || g["slack.items_unowned_age_days"] != 0 {
		t.Errorf("other: closure_posted %v, live_task %v, age %v", g["slack.closure_posted"], g["slack.live_task"], g["slack.items_unowned_age_days"])
	}
	if err := f.db.Extraction().MarkClosed(ctx, factsThread, monday); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.facts(t, factsThread); got["slack.closure_posted"] != true {
		t.Error("a sent draft's unit.closed is not read as closure_posted")
	}
	if got, _ := f.facts(t, factsDM); got["slack.conversation"] != "group" || got["slack.channel"] != "D0001" {
		t.Errorf("dm segment: conversation %v, channel %v", got["slack.conversation"], got["slack.channel"])
	}
}

func TestUnownedItemsAgeInLocalWorkingDays(t *testing.T) {
	t.Parallel()
	f := newFactsFixture(t)
	f.listed(t, factsThread, factsOther)
	friday := time.Date(2026, 2, 27, 11, 0, 0, 0, brt)
	f.item(factsThread, "i1", items.Open, friday)
	f.item(factsOther, "i2", items.Open, time.Date(2026, 2, 27, 22, 0, 0, 0, brt))
	if got, _ := f.facts(t, factsOther); got["slack.items_unowned_age_days"] != 0 {
		t.Errorf("an item of Friday 22:00 BRT is not a working day old on Monday 10:00 BRT: %v", got["slack.items_unowned_age_days"])
	}
	for _, c := range []struct {
		now  time.Time
		want int
	}{
		{time.Date(2026, 3, 1, 23, 0, 0, 0, brt), 0},
		{time.Date(2026, 3, 2, 10, 59, 0, 0, brt), 0},
		{time.Date(2026, 3, 2, 11, 0, 0, 0, brt), 1},
		{time.Date(2026, 3, 4, 12, 0, 0, 0, brt), 3},
		{time.Date(2026, 3, 2, 13, 30, 0, 0, time.UTC), 0},
	} {
		f.now = c.now
		if got, _ := f.facts(t, factsThread); got["slack.items_unowned_age_days"] != c.want {
			t.Errorf("at %v: %v working days, want %d", c.now, got["slack.items_unowned_age_days"], c.want)
		}
	}
	f.now = time.Date(2026, 3, 1, 9, 0, 0, 0, brt)
	if _, next := f.facts(t, factsThread); !next.Equal(time.Date(2026, 3, 2, 11, 0, 0, 0, brt)) {
		t.Errorf("due %v, want Monday 11:00 BRT", next)
	}
}

func TestSlackSettledAtIsTheHosts(t *testing.T) {
	t.Parallel()
	f := newFactsFixture(t)
	f.listed(t, factsThread)
	if got, _ := f.facts(t, factsThread); got["slack.settled_at"] != (time.Time{}) {
		t.Errorf("the plugin reports settled_at %v, which the host fills in a workflow's scope", got["slack.settled_at"])
	}
}

func (f *fixture) messages(t *testing.T, tss ...string) {
	t.Helper()
	k, _ := dslack.ParseUnit(factsThread)
	o := store.ObjectFacts{Object: store.ObjectRow{Object: k.Conversation + "/" + k.TS, Conversation: k.Conversation, TS: k.TS, Kind: store.SlackThread}}
	for _, ts := range tss {
		o.Messages = append(o.Messages, store.MessageRow{Conversation: k.Conversation, TS: ts, ThreadTS: k.TS, User: "U0009", Text: "m " + ts})
	}
	if _, err := f.db.Slack().Save(context.Background(), store.FactsRows{Team: k.Team, Objects: []store.ObjectFacts{o}}, monday); err != nil {
		t.Fatal(err)
	}
}

func tsOf(t time.Time) string { return fmt.Sprintf("%d.000100", t.Unix()) }

func TestASlackUnitEndsWhenClosedOrQuietPastTheSetting(t *testing.T) {
	t.Parallel()
	f := newFactsFixture(t)
	f.listed(t, factsThread)
	if got, _ := f.facts(t, factsThread); got["slack.ended"] != false {
		t.Fatal("a unit with no stored message has ended")
	}
	root, _ := dslack.ParseUnit(factsThread)
	f.messages(t, root.TS, tsOf(monday.Add(-factsEndedAfter+time.Minute)))
	if got, _ := f.facts(t, factsThread); got["slack.ended"] != false {
		t.Fatal("a unit whose newest message is younger than the setting has ended")
	}
	f.now = monday.Add(2 * time.Minute)
	if got, _ := f.facts(t, factsThread); got["slack.ended"] != true {
		t.Fatal("a unit quiet for the setting has not ended")
	}
	f.now = monday
	if err := f.db.Extraction().Save(context.Background(), store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{
		factsThread: {ClosurePosted: true, At: monday}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.facts(t, factsThread); got["slack.ended"] != true {
		t.Fatal("a closed unit has not ended")
	}
}
