package slack

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	team  = "T0001"
	chan1 = "C0001"
	dm1   = "D0001"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// ts is the n-th synthetic timestamp.
func ts(n int) string { return fmt.Sprintf("%d.000100", 1700000000+n) }

type rig struct {
	st  *Store
	db  *sqlite.DB
	log *change.Log
	now time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "state", "store.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{db: db, log: log, now: t0}
	r.st = &Store{DB: db.Slack(), Now: func() time.Time { return r.now }}
	return r
}

func (r *rig) save(t *testing.T, f dslack.Facts) port.Change {
	t.Helper()
	if f.Team == "" {
		f.Team = team
	}
	from := r.log.Gen()
	if err := r.st.Save(context.Background(), nil, f, "", r.now); err != nil {
		t.Fatal(err)
	}
	if n := r.log.Gen() - from; n > 1 {
		t.Fatalf("one save noted %d times", n)
	}
	return r.log.Since(from, port.TopicSourceEvents)
}

func (r *rig) changes(t *testing.T) []dslack.Change {
	t.Helper()
	cs, err := r.st.Changes(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func (r *rig) messages(t *testing.T, conv string) []sqlite.MessageRow {
	t.Helper()
	ms, err := r.db.Slack().Messages(context.Background(), conv, sqlite.MessageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func thread(conv string, parent int, ms ...dslack.Message) dslack.Facts {
	th := dslack.Thread{Conversation: conv, TS: ts(parent), Messages: ms}
	for _, m := range ms {
		if m.TS != th.TS {
			th.LatestReply, th.ReplyCount = m.TS, th.ReplyCount+1
		}
	}
	return dslack.Facts{Threads: []dslack.Thread{th}}
}

func msg(n int, text string) dslack.Message {
	return dslack.Message{TS: ts(n), User: "U0001", Text: text}
}

func reply(n, parent int, text string) dslack.Message {
	m := msg(n, text)
	m.ThreadTS = ts(parent)
	return m
}

func kinds(cs []dslack.Change) []dslack.ChangeKind {
	out := make([]dslack.ChangeKind, len(cs))
	for i, c := range cs {
		out[i] = c.Kind
	}
	return out
}

func TestSaveNotesSourceEventsPerUnit(t *testing.T) {
	r := newRig(t)
	got := r.save(t, thread(chan1, 1, reply(2, 1, "text-2"), reply(3, 1, "text-3")))
	want := []unit.Unit{dslack.ThreadUnit(team, chan1, ts(1))}
	if !got.Has(port.TopicSourceEvents) || got.All || !slices.Equal(got.Units, want) {
		t.Errorf("noted %+v, want %v", got, want)
	}
	if cs := r.changes(t); len(cs) != 2 || cs[0].TS != ts(2) || cs[1].TS != ts(3) || cs[0].ThreadTS != ts(1) {
		t.Errorf("changes %+v", cs)
	}
}

func TestSaveCollapsesDuplicateDeltas(t *testing.T) {
	r := newRig(t)
	r.save(t, thread(chan1, 1, msg(1, "text-1")))
	again := msg(1, "text-1")
	again.Permalink = "https://example.slack.com/archives/C0001/p1700000001000100"
	if got := r.save(t, thread(chan1, 1, again)); !got.Empty() {
		t.Errorf("the same message noted %+v", got)
	}
	if ms := r.messages(t, chan1); len(ms) != 1 || ms[0].Permalink != again.Permalink {
		t.Errorf("messages %+v", ms)
	}
	if cs := r.changes(t); len(cs) != 1 {
		t.Errorf("changes %+v", cs)
	}
}

func TestSaveKeepsOnlyTheLatestText(t *testing.T) {
	r := newRig(t)
	r.save(t, thread(chan1, 1, msg(1, "text-1")))
	edited := msg(1, "text-2")
	edited.EditedTS = ts(5)
	if got := r.save(t, thread(chan1, 1, edited)); len(got.Units) != 1 {
		t.Errorf("an edit noted %+v", got)
	}
	stale := msg(1, "text-stale")
	stale.EditedTS = ts(4)
	if got := r.save(t, thread(chan1, 1, stale)); !got.Empty() {
		t.Errorf("an older edit noted %+v", got)
	}
	ms := r.messages(t, chan1)
	if len(ms) != 1 || ms[0].Text != "text-2" || ms[0].EditedTS != ts(5) {
		t.Errorf("messages %+v", ms)
	}
	if got := kinds(r.changes(t)); !slices.Equal(got, []dslack.ChangeKind{dslack.ChangeNew, dslack.ChangeEdited}) {
		t.Errorf("changes %v", got)
	}
}

func TestSaveDeleteBlanksTheText(t *testing.T) {
	r := newRig(t)
	r.save(t, thread(chan1, 1, msg(1, "text-1")))
	gone := msg(1, "text-1")
	gone.Deleted = true
	if got := r.save(t, thread(chan1, 1, gone)); len(got.Units) != 1 {
		t.Errorf("a delete noted %+v", got)
	}
	ms := r.messages(t, chan1)
	if len(ms) != 1 || !ms[0].Deleted || ms[0].Text != "" {
		t.Errorf("messages %+v", ms)
	}
	if got := r.save(t, thread(chan1, 1, msg(1, "text-1"))); !got.Empty() {
		t.Errorf("a deleted message came back: %+v", got)
	}
	if got := kinds(r.changes(t)); !slices.Equal(got, []dslack.ChangeKind{dslack.ChangeNew, dslack.ChangeDeleted}) {
		t.Errorf("changes %v", got)
	}
}

func TestReactionsReplaceTheSet(t *testing.T) {
	r := newRig(t)
	r.save(t, thread(chan1, 1, msg(1, "text-1")))
	m := msg(1, "text-1")
	m.Reactions = []dslack.Reaction{{Name: "eyes", Users: []string{"U0002", "U0003"}}}
	if got := r.save(t, thread(chan1, 1, m)); len(got.Units) != 1 {
		t.Errorf("a reaction noted %+v", got)
	}
	if got := r.save(t, thread(chan1, 1, msg(1, "text-1"))); !got.Empty() {
		t.Errorf("a message without reactions noted %+v", got)
	}
	m.Reactions = []dslack.Reaction{}
	if got := r.save(t, thread(chan1, 1, m)); len(got.Units) != 1 {
		t.Errorf("removing the reactions noted %+v", got)
	}
	if ms := r.messages(t, chan1); len(ms) != 1 || len(ms[0].Reactions) != 0 {
		t.Errorf("an empty set did not remove the last reaction: %+v", ms)
	}
	m.Reactions = []dslack.Reaction{{Name: "eyes", Users: []string{"U0002", "U0003"}}}
	if got := r.save(t, thread(chan1, 1, m)); len(got.Units) != 1 {
		t.Errorf("the first set again noted %+v", got)
	}
	want := []dslack.ChangeKind{dslack.ChangeNew, dslack.ChangeReaction, dslack.ChangeReaction, dslack.ChangeReaction}
	if got := kinds(r.changes(t)); !slices.Equal(got, want) {
		t.Errorf("changes %v, want %v", got, want)
	}
}

func TestSaveRefusesMessagesOutsideTheObject(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for name, f := range map[string]sqlite.FactsRows{
		"a foreign anchor": {Team: team, Objects: []sqlite.ObjectFacts{{
			Object:   sqlite.ObjectRow{Object: dslack.ThreadObject(chan1, ts(1)), Conversation: chan1, TS: ts(1), Kind: sqlite.SlackThread},
			Messages: []sqlite.MessageRow{{TS: ts(1), Text: "text-1"}, {TS: ts(9), Text: "text-9"}},
		}}},
		"a foreign conversation": {Team: team, Objects: []sqlite.ObjectFacts{{
			Object:   sqlite.ObjectRow{Object: dslack.DMObject(dm1), Conversation: dm1, Kind: sqlite.SlackDM},
			Messages: []sqlite.MessageRow{{Conversation: "C0009", TS: ts(2), Text: "text-2"}},
		}}},
	} {
		from := r.log.Gen()
		if _, err := r.db.Slack().Save(ctx, f, r.now); err == nil {
			t.Errorf("%s: saved", name)
		}
		if r.log.Gen() != from {
			t.Errorf("%s: noted", name)
		}
	}
	if err := r.st.Save(ctx, nil, thread(chan1, 1, msg(1, "text-1"), msg(9, "text-9")), "", r.now); err == nil {
		t.Error("the adapter saved a lone message into another thread")
	}
	for _, conv := range []string{chan1, dm1, "C0009"} {
		if ms := r.messages(t, conv); len(ms) != 0 {
			t.Errorf("%s holds %+v", conv, ms)
		}
	}
	if cs := r.changes(t); len(cs) != 0 {
		t.Errorf("changes %+v", cs)
	}
}

func TestUnsegmentedDMNotesWithoutUnits(t *testing.T) {
	r := newRig(t)
	got := r.save(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{msg(1, "text-1"), msg(2, "text-2")}}}})
	if !got.Has(port.TopicSourceEvents) || got.All || len(got.Units) != 0 {
		t.Errorf("noted %+v, want a unit-less note", got)
	}
	if cs := r.changes(t); len(cs) != 2 {
		t.Errorf("changes %+v", cs)
	}
}

// segments maps anchors and ranges of one DM to segment units.
type segments struct {
	spans map[string][2]string // segment id -> [from, to]
}

func (s segments) unit(id string) unit.Unit {
	return dslack.Key{Team: team, Conversation: dm1, Segment: id}.Unit()
}

func (s segments) UnitsFor(_ context.Context, conv, at string) ([]unit.Unit, error) {
	var out []unit.Unit
	for id, sp := range s.spans {
		if conv == dm1 && dslack.CompareTS(at, sp[0]) >= 0 && (sp[1] == "" || dslack.CompareTS(at, sp[1]) <= 0) {
			out = append(out, s.unit(id))
		}
	}
	return out, nil
}

func (s segments) Range(_ context.Context, u unit.Unit) (string, string, string, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return "", "", "", err
	}
	sp, ok := s.spans[k.Segment]
	if !ok || k.Conversation != dm1 {
		return "", "", "", nil
	}
	return dm1, sp[0], sp[1], nil
}

func TestDMThreadReplyFollowsParentSegment(t *testing.T) {
	r := newRig(t)
	segs := segments{spans: map[string][2]string{"a": {ts(1), ts(4)}, "b": {ts(5), ""}}}
	r.st.Segments = segs
	got := r.save(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{reply(9, 2, "text-9")}}}})
	if want := []unit.Unit{segs.unit("a")}; !slices.Equal(got.Units, want) {
		t.Errorf("noted %v, want the parent's segment %v", got.Units, want)
	}
	ctx := context.Background()
	if _, ok, _ := r.db.Slack().Object(ctx, dslack.DMObject(dm1)); !ok {
		t.Error("the reply is not in the DM object")
	}
	if _, ok, _ := r.db.Slack().Object(ctx, dslack.ThreadObject(dm1, ts(2))); ok {
		t.Error("a DM reply made a thread object")
	}
}

func TestHighWaterMoveStoresNoText(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	from := r.log.Gen()
	if err := r.db.Slack().MarkSeen(ctx, "C0009", ts(7)); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Slack().MarkSeen(ctx, "C0009", ts(3)); err != nil {
		t.Fatal(err)
	}
	cs, err := r.db.Slack().Conversations(ctx, []string{"C0009"})
	if err != nil || len(cs) != 1 || cs[0].HWM != ts(7) || cs[0].Admitted {
		t.Fatalf("conversation %+v %v", cs, err)
	}
	if ms := r.messages(t, "C0009"); len(ms) != 0 {
		t.Errorf("messages %+v", ms)
	}
	if got := r.changes(t); len(got) != 0 {
		t.Errorf("changes %+v", got)
	}
	if r.log.Gen() != from {
		t.Error("MarkSeen noted the log")
	}
}

// fixed is a Scheduler that answers one offset per object.
type fixed map[string]time.Duration

func (f fixed) Next(_ context.Context, objects []string, now time.Time) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for _, o := range objects {
		out[o] = now.Add(f[o])
	}
	return out, nil
}

func watchedObjects(t *testing.T, st *Store) []string {
	t.Helper()
	stubs, err := st.Watched(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range stubs {
		out = append(out, s.Object)
	}
	return out
}

func TestWatchedReturnsDueObjects(t *testing.T) {
	r := newRig(t)
	o1, o2, o3 := dslack.ThreadObject(chan1, ts(1)), dslack.ThreadObject(chan1, ts(2)), dslack.ThreadObject(chan1, ts(3))
	r.st.Scheduler = fixed{o1: time.Hour, o2: time.Minute, o3: time.Hour}
	for i := 1; i <= 3; i++ {
		r.save(t, thread(chan1, i, msg(i, "text")))
	}
	r.now = t0.Add(2 * time.Minute)
	stubs, err := r.st.Watched(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stubs) != 1 || stubs[0].Object != o2 || stubs[0].Version != ts(2) {
		t.Errorf("watched %+v, want %s only", stubs, o2)
	}
}

func TestSeeReschedulesUnmovedObjects(t *testing.T) {
	r := newRig(t)
	o := dslack.ThreadObject(chan1, ts(1))
	r.save(t, thread(chan1, 1, msg(1, "text")))
	r.now = t0.Add(6 * time.Minute)
	if got := watchedObjects(t, r.st); !slices.Equal(got, []string{o}) {
		t.Fatalf("watched %v", got)
	}
	if err := r.st.See(context.Background(), []string{o}, r.now); err != nil {
		t.Fatal(err)
	}
	row, _, _ := r.db.Slack().Object(context.Background(), o)
	if !row.NextCheckAt.Equal(r.now.Add(DefaultPoll)) || !row.SeenAt.Equal(r.now) {
		t.Errorf("next %v seen %v", row.NextCheckAt, row.SeenAt)
	}
	if got := watchedObjects(t, r.st); len(got) != 0 {
		t.Errorf("an unmoved object is due again: %v", got)
	}
}

func TestSaveSchedulesTheNextCheck(t *testing.T) {
	r := newRig(t)
	o := dslack.ThreadObject(chan1, ts(1))
	r.st.Scheduler = fixed{o: 42 * time.Minute}
	r.save(t, thread(chan1, 1, msg(1, "text")))
	row, ok, err := r.db.Slack().Object(context.Background(), o)
	if err != nil || !ok || !row.NextCheckAt.Equal(t0.Add(42*time.Minute)) {
		t.Errorf("object %+v %v %v", row, ok, err)
	}
	if !row.Watched || !slices.Equal(row.Reasons, []string{"admission"}) || !row.AdmittedAt.Equal(t0) {
		t.Errorf("a saved object is not admitted and watched: %+v", row)
	}
}

func TestScheduleSetsTheNextCheck(t *testing.T) {
	r := newRig(t)
	o := dslack.ThreadObject(chan1, ts(1))
	r.save(t, thread(chan1, 1, msg(1, "text")))
	at := t0.Add(time.Minute)
	if err := r.db.Slack().Schedule(context.Background(), o, at); err != nil {
		t.Fatal(err)
	}
	row, _, _ := r.db.Slack().Object(context.Background(), o)
	if !row.NextCheckAt.Equal(at) {
		t.Errorf("next %v, want %v", row.NextCheckAt, at)
	}
	r.now = at
	if got := watchedObjects(t, r.st); !slices.Equal(got, []string{o}) {
		t.Errorf("watched %v", got)
	}
}

func TestUnwatchKeepsMessages(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	o := dslack.ThreadObject(chan1, ts(1))
	r.save(t, thread(chan1, 1, msg(1, "text")))
	r.now = t0.Add(time.Hour)
	if err := r.db.Slack().SetWatched(ctx, o, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := watchedObjects(t, r.st); len(got) != 0 {
		t.Errorf("an unwatched object is watched: %v", got)
	}
	if ms := r.messages(t, chan1); len(ms) != 1 {
		t.Errorf("unwatch dropped messages: %+v", ms)
	}
	if err := r.db.Slack().SetWatched(ctx, o, true, "turn"); err != nil {
		t.Fatal(err)
	}
	if got := watchedObjects(t, r.st); !slices.Equal(got, []string{o}) {
		t.Errorf("watched %v", got)
	}
	row, _, _ := r.db.Slack().Object(ctx, o)
	if !slices.Equal(row.Reasons, []string{"turn"}) {
		t.Errorf("reasons %v", row.Reasons)
	}
	follow := dslack.ThreadObject("C0002", ts(8))
	if err := r.db.Slack().SetWatched(ctx, follow, true, "user-follow"); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.st.Versions(ctx, []string{follow}); v[follow] != "" {
		t.Errorf("a followed object not read yet has version %q", v[follow])
	}
	if got := watchedObjects(t, r.st); !slices.Contains(got, follow) {
		t.Errorf("a new follow is not due at once: %v", got)
	}
}

func TestMessagesAfterCursor(t *testing.T) {
	r := newRig(t)
	last := reply(4, 1, "text-4")
	last.Reactions = []dslack.Reaction{{Name: "eyes", Users: []string{"U0002", "U0003"}}, {Name: "ok", Users: []string{"U0002"}}}
	r.save(t, thread(chan1, 1, msg(1, "text-1"), reply(2, 1, "text-2"), reply(3, 1, "text-3"), last))
	r.save(t, thread("C0002", 5, msg(5, "text-5")))
	got, err := r.st.Messages(context.Background(), dslack.ThreadUnit(team, chan1, ts(1)), ts(2), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TS != ts(3) || got[1].TS != ts(4) {
		t.Fatalf("messages %+v", got)
	}
	want := []dslack.Reaction{{Name: "eyes", Users: []string{"U0002", "U0003"}}, {Name: "ok", Users: []string{"U0002"}}}
	if fmt.Sprint(got[1].Reactions) != fmt.Sprint(want) {
		t.Errorf("reactions %v, want %v", got[1].Reactions, want)
	}
	if got, _ := r.st.Messages(context.Background(), dslack.ThreadUnit(team, chan1, ts(1)), "", 1); len(got) != 1 || got[0].TS != ts(1) {
		t.Errorf("limit 1: %+v", got)
	}
}

func TestSegmentUnitIsSliced(t *testing.T) {
	r := newRig(t)
	segs := segments{spans: map[string][2]string{"a": {ts(2), ts(4)}, "b": {ts(5), ""}}}
	r.st.Segments = segs
	var ms []dslack.Message
	for i := 1; i <= 5; i++ {
		ms = append(ms, msg(i, "text"))
	}
	ms = append(ms, reply(6, 2, "text-6"), reply(7, 5, "text-7"))
	r.save(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: ms}}})
	got, err := r.st.Messages(context.Background(), segs.unit("a"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var tss []string
	for _, m := range got {
		tss = append(tss, m.TS)
	}
	if want := []string{ts(2), ts(3), ts(4), ts(6)}; !slices.Equal(tss, want) {
		t.Errorf("segment a: %v, want %v", tss, want)
	}
	got, _ = r.st.Messages(context.Background(), segs.unit("b"), ts(5), 0)
	if len(got) != 1 || got[0].TS != ts(7) {
		t.Errorf("open segment b after its first message: %+v", got)
	}
}

func TestUnknownUnitFails(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for _, u := range []unit.Unit{
		dslack.ThreadUnit(team, chan1, ts(1)),
		dslack.Key{Team: team, Conversation: dm1, Segment: "x"}.Unit(),
		{Provider: unit.GitHub, Key: "o/r#1"},
	} {
		if _, err := r.st.Messages(ctx, u, "", 0); !errors.Is(err, dslack.ErrUnknownUnit) {
			t.Errorf("%v without a segment map: %v", u, err)
		}
	}
	r.st.Segments = segments{spans: map[string][2]string{"a": {ts(1), ""}}}
	if _, err := r.st.Messages(ctx, dslack.Key{Team: team, Conversation: dm1, Segment: "x"}.Unit(), "", 0); !errors.Is(err, dslack.ErrUnknownUnit) {
		t.Errorf("an unknown segment: %v", err)
	}
}

func TestChangesPage(t *testing.T) {
	r := newRig(t)
	for i := 1; i <= 5; i++ {
		r.save(t, thread(chan1, i, msg(i, "text")))
	}
	ctx := context.Background()
	first, err := r.st.Changes(ctx, 0, 2)
	if err != nil || len(first) != 2 || first[0].TS != ts(1) || first[1].TS != ts(2) || first[0].Seq >= first[1].Seq {
		t.Fatalf("first page %+v %v", first, err)
	}
	rest, err := r.st.Changes(ctx, first[1].Seq, 0)
	if err != nil || len(rest) != 3 || rest[0].TS != ts(3) || rest[2].TS != ts(5) {
		t.Errorf("rest %+v %v", rest, err)
	}
	if !rest[0].At.Equal(t0) || rest[0].Kind != dslack.ChangeNew {
		t.Errorf("change %+v", rest[0])
	}
}

func TestUsersRoundTrip(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.save(t, dslack.Facts{Users: []dslack.User{
		{ID: "U0001", Name: "name-1", DisplayName: "display-1", Email: "user-1@example.com", Fetched: t0},
		{ID: "U0002", Name: "name-2", IsBot: true, Fetched: t0},
		{ID: "USLACKBOT", Name: "slackbot"},
	}})
	got, err := r.st.Users(ctx, []string{"U0001", "U0002", "USLACKBOT", "U0404"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["U0001"].DisplayName != "display-1" || !got["U0001"].Fetched.Equal(t0) || got["U0001"].IsBot ||
		!got["U0002"].IsBot || !got["USLACKBOT"].IsBot {
		t.Errorf("users %+v", got)
	}
	u, ok, err := r.st.UserByEmail(ctx, "USER-1@example.com")
	if err != nil || !ok || u.ID != "U0001" {
		t.Errorf("by email %+v %v %v", u, ok, err)
	}
	if _, ok, _ := r.st.UserByEmail(ctx, "none@example.com"); ok {
		t.Error("an unknown email matched")
	}
	if _, ok, _ := r.st.UserByEmail(ctx, ""); ok {
		t.Error("an empty email matched")
	}
}

func TestStoreLookups(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := r.db.Slack()
	r.save(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "name-1", Kind: dslack.KindChannel, IsMember: true}, {ID: "C0002", Name: "name-2", Kind: dslack.KindPrivate}},
		Threads:       thread(chan1, 1, msg(1, "text-1"), reply(3, 1, "text-3")).Threads,
	})
	row, ok, err := s.Object(ctx, dslack.ThreadObject(chan1, ts(1)))
	if err != nil || !ok || row.Kind != sqlite.SlackThread || row.Version != ts(3) || row.ReplyCount != 1 || !row.LastActivityAt.Equal(time.Unix(1700000003, 0).UTC()) {
		t.Errorf("object %+v %v %v", row, ok, err)
	}
	if _, ok, _ := s.Object(ctx, dslack.ThreadObject(chan1, ts(9))); ok {
		t.Error("an unknown object was found")
	}
	adm, err := s.AdmittedConversations(ctx)
	if err != nil || len(adm) != 1 || adm[0].ID != chan1 || adm[0].HWM != ts(3) || adm[0].Team != team || adm[0].Name != "name-1" {
		t.Errorf("admitted %+v %v", adm, err)
	}
	c, err := r.st.Conversation(ctx, chan1)
	if err != nil || c.Kind != dslack.KindChannel || !c.IsMember || c.Name != "name-1" {
		t.Errorf("conversation %+v %v", c, err)
	}
	if _, err := r.st.Conversation(ctx, "C0404"); err == nil {
		t.Error("an unknown conversation was found")
	}
	if err := r.st.Save(ctx, nil, nil, "cursor-1", r.now); err != nil {
		t.Fatal(err)
	}
	if got, err := r.st.Cursor(ctx); err != nil || got != "cursor-1" {
		t.Errorf("cursor %q %v", got, err)
	}
	if _, _, err := r.st.Viewer(ctx); err == nil {
		t.Error("a viewer before login")
	}
	for name, v := range map[string]string{CursorViewerTeam: team, CursorViewerUser: "U0001"} {
		if err := s.SetCursor(ctx, name, v); err != nil {
			t.Fatal(err)
		}
	}
	if tm, u, err := r.st.Viewer(ctx); err != nil || tm != team || u != "U0001" {
		t.Errorf("viewer %q %q %v", tm, u, err)
	}
	if err := r.st.Save(ctx, nil, dslack.Facts{Threads: thread("C0002", 4, msg(4, "text")).Threads}, "", r.now); err != nil {
		t.Errorf("thread facts without a team and a known viewer: %v", err)
	}
}

// source is a port.Source over one thread whose replies the test appends.
type source struct {
	th    dslack.Thread
	reads int
}

func (s *source) Provider() unit.ProviderID { return dslack.Provider }

func (s *source) Discover(context.Context, string) ([]port.Stub, string, error) { return nil, "", nil }

func (s *source) Stubs(_ context.Context, objects []string) ([]port.Stub, error) {
	var out []port.Stub
	for _, o := range objects {
		if o == dslack.ThreadObject(s.th.Conversation, s.th.TS) {
			out = append(out, port.Stub{Object: o, Version: s.th.Version()})
		}
	}
	return out, nil
}

func (s *source) Read(context.Context, []string) (port.Facts, error) {
	s.reads++
	return dslack.Facts{Team: team, Threads: []dslack.Thread{s.th}}, nil
}

func (s *source) Viewer(context.Context) (string, error) { return "U0001", nil }

func TestIngestPassOverTheStore(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	src := &source{th: thread(chan1, 1, msg(1, "text-1")).Threads[0]}
	reg := ingest.Registration{Source: src, Store: r.st, Codec: dslack.Codec()}
	o := dslack.ThreadObject(chan1, ts(1))
	if err := r.db.Slack().SetWatched(ctx, o, true, "user-follow"); err != nil {
		t.Fatal(err)
	}
	pass := func() port.Change {
		t.Helper()
		from := r.log.Gen()
		if _, err := ingest.Run(ctx, reg, r.now); err != nil {
			t.Fatal(err)
		}
		return r.log.Since(from, port.TopicSourceEvents)
	}
	u := dslack.ThreadUnit(team, chan1, ts(1))
	if got := pass(); !slices.Equal(got.Units, []unit.Unit{u}) || src.reads != 1 {
		t.Fatalf("first pass noted %+v after %d reads", got, src.reads)
	}
	r.now = r.now.Add(DefaultPoll)
	if got := pass(); !got.Empty() || src.reads != 1 {
		t.Errorf("an unmoved thread noted %+v after %d reads", got, src.reads)
	}
	if got := watchedObjects(t, r.st); len(got) != 0 {
		t.Errorf("an unmoved thread is due at once: %v", got)
	}
	src.th = thread(chan1, 1, msg(1, "text-1"), reply(2, 1, "text-2")).Threads[0]
	r.now = r.now.Add(DefaultPoll)
	if got := pass(); !slices.Equal(got.Units, []unit.Unit{u}) || src.reads != 2 {
		t.Errorf("a new reply noted %+v after %d reads", got, src.reads)
	}
	if got := kinds(r.changes(t)); !slices.Equal(got, []dslack.ChangeKind{dslack.ChangeNew, dslack.ChangeNew}) {
		t.Errorf("changes %v", got)
	}
}
