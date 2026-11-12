package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var acmePull = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}

func reviewDraft(at time.Time) draft.Draft {
	return draft.Draft{Unit: acmePull, Kind: "review", Purpose: "review", Delivery: "review", Text: "Two findings.", Original: "Two findings.",
		Origin:  draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review", Session: "s-review"},
		Payload: json.RawMessage(`{"event":"REQUEST_CHANGES","comments":[{"path":"a.go","line":3,"body":"nil map"}]}`),
		Basis:   "c0ffee", Hold: true, State: draft.Held, CreatedAt: at, UpdatedAt: at}
}

func TestDraftPayloadRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	s := db.Drafts()
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	in, err := s.Insert(ctx, reviewDraft(at))
	if err != nil {
		t.Fatal(err)
	}
	if in.Rev != 1 || in.By != (draft.Actor{Kind: draft.ActorSession, Session: "s-review"}) {
		t.Fatalf("inserted at rev %d by %+v, want rev 1 by its proposer", in.Rev, in.By)
	}
	got, err := s.Get(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("read back\n%+v\nwant\n%+v", got, in)
	}
	if string(got.Payload) != string(reviewDraft(at).Payload) || got.Basis != "c0ffee" || !got.Hold {
		t.Fatalf("payload %s basis %q hold %v", got.Payload, got.Basis, got.Hold)
	}
	listed, err := s.List(ctx, outbox.Filter{Unit: &acmePull})
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0], in) {
		t.Fatalf("listed %+v %v", listed, err)
	}

	got.Payload, got.Basis, got.Hold = json.RawMessage(`{"event":"COMMENT"}`), "beef", false
	up, err := s.Update(ctx, got, draft.Held)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Get(ctx, in.ID)
	if err != nil || !reflect.DeepEqual(again, up) || string(again.Payload) != `{"event":"COMMENT"}` || again.Basis != "beef" || again.Hold {
		t.Fatalf("after update %+v %v", again, err)
	}

	plain, err := s.Insert(ctx, sampleDraft(slackThread, at))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, plain.ID); err != nil || got.Payload != nil || got.Basis != "" || got.Hold {
		t.Fatalf("a draft without a payload reads %s %q %v, %v", got.Payload, got.Basis, got.Hold, err)
	}
}

func TestRevisionsKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	s := db.Drafts()
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	d, err := s.Insert(ctx, reviewDraft(at))
	if err != nil {
		t.Fatal(err)
	}

	person := d
	person.Text, person.Edited, person.By, person.UpdatedAt = "One finding.", true, draft.Actor{Kind: draft.ActorPerson}, at.Add(time.Minute)
	if d, err = s.Update(ctx, person, draft.Held); err != nil {
		t.Fatal(err)
	}
	agent := d
	agent.Payload, agent.Basis, agent.By, agent.UpdatedAt = json.RawMessage(`{"event":"APPROVE"}`), "f00d", draft.Actor{Kind: draft.ActorSession, Session: "s-address"}, at.Add(2*time.Minute)
	if d, err = s.Update(ctx, agent, draft.Held); err != nil {
		t.Fatal(err)
	}
	queued := d
	queued.State, queued.UpdatedAt = draft.Queued, at.Add(3*time.Minute)
	if d, err = s.Update(ctx, queued, draft.Held); err != nil {
		t.Fatal(err)
	}
	if d.Rev != 3 || d.Version != 4 || d.By != (draft.Actor{Kind: draft.ActorSession, Session: "s-address"}) {
		t.Fatalf("rev %d version %d by %+v, want rev 3, a state move later version 4, by s-address", d.Rev, d.Version, d.By)
	}
	if got, err := s.Get(ctx, d.ID); err != nil || got.By != d.By {
		t.Fatalf("stored author %+v %v", got.By, err)
	}

	revs, err := s.Revisions(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []draft.Revision{
		{Rev: 1, Text: "Two findings.", Payload: reviewDraft(at).Payload, Basis: "c0ffee", By: draft.Actor{Kind: draft.ActorSession, Session: "s-review"}, At: at},
		{Rev: 2, Text: "One finding.", Payload: reviewDraft(at).Payload, Basis: "c0ffee", By: draft.Actor{Kind: draft.ActorPerson}, At: at.Add(time.Minute)},
		{Rev: 3, Text: "One finding.", Payload: json.RawMessage(`{"event":"APPROVE"}`), Basis: "f00d", By: draft.Actor{Kind: draft.ActorSession, Session: "s-address"}, At: at.Add(2 * time.Minute)},
	}
	if !reflect.DeepEqual(revs, want) {
		t.Fatalf("revisions\n%+v\nwant\n%+v", revs, want)
	}

	if _, err := s.Revisions(ctx, 999); !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("a missing draft's revisions: %v, want ErrNotFound", err)
	}
	auto, err := s.Insert(ctx, sampleDraft(slackThread, at))
	if err != nil {
		t.Fatal(err)
	}
	if revs, err := s.Revisions(ctx, auto.ID); err != nil || len(revs) != 1 || revs[0].By != (draft.Actor{Kind: draft.ActorSession, Session: "s-worker"}) {
		t.Fatalf("a session's proposal: %+v %v", revs, err)
	}
}

func TestARevisionNamesItsProposer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		origin draft.Origin
		want   draft.Actor
	}{
		{draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge"}, draft.Actor{Kind: draft.ActorRule}},
		{draft.Origin{Kind: draft.OriginUser}, draft.Actor{Kind: draft.ActorPerson}},
		{draft.Origin{Kind: draft.OriginTurn, TurnSeq: 2}, draft.Actor{Kind: draft.ActorSession}},
	} {
		d := reviewDraft(at)
		d.Origin = c.origin
		in, err := db.Drafts().Insert(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		if revs, err := db.Drafts().Revisions(ctx, in.ID); err != nil || len(revs) != 1 || revs[0].By != c.want {
			t.Errorf("%s: %+v %v, want by %+v", c.origin.Kind, revs, err, c.want)
		}
	}
}

func TestApprovedRevStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	gs := db.Gates(func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) })
	open, err := gs.Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: acmePull, Subject: "1", Intents: []string{"approve", "drop"},
		Session: "s-review", ApprovedRev: 2, Sessions: []string{"s-review", "s-address"}})
	if err != nil {
		t.Fatal(err)
	}
	if open.ApprovedRev != 2 || !slices.Equal(open.Sessions, []string{"s-review", "s-address"}) {
		t.Fatalf("opened %+v", open)
	}
	got, err := gs.Get(ctx, open.ID)
	if err != nil || got.ApprovedRev != 2 || !slices.Equal(got.Sessions, []string{"s-review", "s-address"}) {
		t.Fatalf("read back %+v %v", got, err)
	}
	listed, err := gs.Where(ctx, gates.Query{Kind: gate.KindDraft, Unit: acmePull})
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0], got) {
		t.Fatalf("listed %+v %v", listed, err)
	}
	resolved, err := gs.Resolve(ctx, open.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: "person"})
	if err != nil || resolved.ApprovedRev != 2 || !slices.Equal(resolved.Sessions, []string{"s-review", "s-address"}) {
		t.Fatalf("resolved %+v %v", resolved, err)
	}
	if again, err := gs.Get(ctx, open.ID); err != nil || again.ApprovedRev != 2 || again.Status != gate.Approved {
		t.Fatalf("after resolve %+v %v", again, err)
	}
	sent, err := gs.RecordSent(ctx, gate.Gate{Kind: gate.KindDraft, Unit: acmePull, Subject: "2", Intents: []string{"approve"}, ApprovedRev: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := gs.Get(ctx, sent.ID); err != nil || got.ApprovedRev != 5 || got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("recorded send %+v %v", got, err)
	}
}

// storeAtV12 is a store at version 12 holding drafts in every state a send
// passes through, their gates, and turn records that endorse with publishes.
func storeAtV12(t *testing.T) string {
	t.Helper()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v12.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{12, string(frozen), nil})
	const cols = `provider, unit_key, kind, target, text, original, edited, origin_kind, turn_seq, intent, workflow, rule, rule_key, state,
		evidence_ref, evidence_permalink, evidence_at, error, reason, created_at, updated_at, rev, purpose, delivery, effects, origin_session, broadcast`
	rawExec(t, path,
		`INSERT INTO drafts (`+cols+`) VALUES
		('github', 'acme/api#7', 'reply', '', 'Thanks, fixed.', 'Thanks, fixed.', 0, 'turn', 3, 'address', 'address', '', '', 'held',
		 '', '', '', '', '', '2026-11-04T10:00:00.000000000Z', '2026-11-04T10:00:00.000000000Z', 0, 'reply', 'comment', '[]', 's-address', ''),
		('github', 'acme/api#7', 'reply', '', 'Ping @{github:octocat}', 'Ping', 1, 'auto', 0, '', 'github-nudge', 'github-nudge', 'github-nudge:acme/api#7', 'failed',
		 '', '', '', '422 Unprocessable Entity', '', '2026-11-04T11:00:00.000000000Z', '2026-11-04T11:05:00.000000000Z', 3, 'nudge', 'comment', '["task.nudged"]', '', ''),
		('slack', 'C0123/1.2', 'reply', '', 'Done.', 'Done.', 0, 'session', 0, '', '', '', '', 'sent',
		 'C0123/1.3', 'https://acme.slack.com/archives/C0123/p13', '2026-11-04T12:01:00.000000000Z', '', '', '2026-11-04T12:00:00.000000000Z', '2026-11-04T12:01:00.000000000Z', 2, 'reply', 'message', '[]', 's-desk', 'off'),
		('github', 'acme/api#7', 'reply', '', 'Shipped.', 'Shipped.', 0, 'user', 0, '', '', '', '', 'queued',
		 '', '', '', '', '', '2026-11-04T13:00:00.000000000Z', '2026-11-04T13:00:00.000000000Z', 1, 'reply', 'comment', '[]', '', '')`,
		`INSERT INTO gates (provider, unit_key, kind, subject, status, intents, intent, by_actor, opened_at, closed_at, session) VALUES
		('github', 'acme/api#7', 'draft', '1', 'open', '["approve","drop"]', '', '', '2026-11-04T10:00:00.000000000Z', '', 's-address'),
		('github', 'acme/api#7', 'draft', '2', 'approved', '["approve","drop"]', 'approve', 'person', '2026-11-04T11:00:00.000000000Z', '2026-11-04T11:01:00.000000000Z', ''),
		('slack', 'C0123/1.2', 'draft', '3', 'approved', '["approve","drop"]', 'approve', 'person', '2026-11-04T12:00:00.000000000Z', '2026-11-04T12:00:30.000000000Z', 's-desk'),
		('github', 'acme/api#7', 'draft', '4', 'approved', '["approve","drop"]', 'approve', 'person', '2026-11-04T13:00:00.000000000Z', '2026-11-04T13:00:10.000000000Z', '')`,
		`INSERT INTO turn_records (turn_seq, provider, unit_key, session, hash, at, record) VALUES
		(3, 'github', 'acme/api#7', 's-address', 'h1', '2026-11-04T10:00:00.000000000Z',
		 '{"question":{"kind":"confirm","text":"Post the reply?","publishes":true}}'),
		(4, 'github', 'acme/api#7', 's-address', 'h2', '2026-11-04T10:30:00.000000000Z',
		 '{"question":{"kind":"choice","text":"Which?","options":[{"id":"a","label":"Post","consequence":"posts it","publishes":true},{"id":"b","label":"Skip","consequence":"nothing"}]}}')`,
	)
	return path
}

func TestMigrationKeepsDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := storeAtV12(t)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	if v := versionOf(t, path); v != SchemaVersion {
		t.Fatalf("version %d", v)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Drafts()

	list, err := s.List(ctx, outbox.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	states := map[draft.State]int{}
	for _, d := range list {
		states[d.State]++
		if d.Payload != nil || d.Basis != "" || d.Hold {
			t.Errorf("draft %d gained payload %s basis %q hold %v", d.ID, d.Payload, d.Basis, d.Hold)
		}
	}
	if want := map[draft.State]int{draft.Held: 1, draft.Failed: 1, draft.Sent: 1, draft.Queued: 1}; !reflect.DeepEqual(states, want) {
		t.Fatalf("states %v, want %v", states, want)
	}
	failed, err := s.Get(ctx, 2)
	if err != nil || failed.Text != "Ping @{github:octocat}" || failed.Error != "422 Unprocessable Entity" || failed.Rev != 2 || failed.By != (draft.Actor{Kind: draft.ActorPerson}) ||
		!slices.Equal(failed.Effects, []string{"task.nudged"}) || failed.Origin.Rule != "github-nudge" {
		t.Fatalf("the failed draft %+v %v", failed, err)
	}
	sent, err := s.Get(ctx, 3)
	if err != nil || sent.Evidence.Permalink != "https://acme.slack.com/archives/C0123/p13" || sent.Broadcast != draft.BroadcastOff {
		t.Fatalf("the sent draft %+v %v", sent, err)
	}

	revs, err := s.Revisions(ctx, 2)
	created, updated := time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC), time.Date(2026, 11, 4, 11, 5, 0, 0, time.UTC)
	want := []draft.Revision{
		{Rev: 1, Text: "Ping", By: draft.Actor{Kind: draft.ActorRule}, At: created},
		{Rev: 2, Text: "Ping @{github:octocat}", By: draft.Actor{Kind: draft.ActorPerson}, At: updated},
	}
	if err != nil || !reflect.DeepEqual(revs, want) {
		t.Fatalf("the edited draft's revisions %+v %v, want %+v", revs, err, want)
	}
	if revs, err := s.Revisions(ctx, 1); err != nil || len(revs) != 1 || revs[0].By != (draft.Actor{Kind: draft.ActorSession, Session: "s-address"}) {
		t.Fatalf("the held draft's revisions %+v %v", revs, err)
	}

	gs := db.Gates(func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) })
	all, err := gs.Where(ctx, gates.Query{Kind: gate.KindDraft, Statuses: []gate.Status{gate.Open, gate.Approved}})
	if err != nil || len(all) != 4 {
		t.Fatalf("gates %+v %v", all, err)
	}
	wantRev := map[string]int64{"1": 1, "2": 2, "3": 1, "4": 1}
	wantSessions := map[string][]string{"1": {"s-address"}, "2": {}, "3": {"s-desk"}, "4": {}}
	for _, g := range all {
		if g.ApprovedRev != wantRev[g.Subject] || !slices.Equal(g.Sessions, wantSessions[g.Subject]) {
			t.Errorf("gate of draft %s: approved rev %d sessions %v, want %d %v", g.Subject, g.ApprovedRev, g.Sessions, wantRev[g.Subject], wantSessions[g.Subject])
		}
	}

	held, err := s.Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var heldGate gate.Gate
	for _, g := range all {
		if g.Subject == "1" {
			heldGate = g
		}
	}
	if heldGate.ApprovedRev != held.Rev || held.By != (draft.Actor{Kind: draft.ActorSession, Session: "s-address"}) {
		t.Fatalf("the held draft at rev %d by %+v cannot be approved by its gate at rev %d", held.Rev, held.By, heldGate.ApprovedRev)
	}
	if _, err := gs.Resolve(ctx, heldGate.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: "person"}); err != nil {
		t.Fatal(err)
	}
	if approved, err := gs.Get(ctx, heldGate.ID); err != nil || approved.ApprovedRev != held.Rev {
		t.Fatalf("approved %+v %v", approved, err)
	}
	for _, step := range []struct{ from, to draft.State }{{draft.Held, draft.Queued}, {draft.Queued, draft.Sending}, {draft.Sending, draft.Sent}} {
		held.State = step.to
		if held, err = s.Update(ctx, held, step.from); err != nil {
			t.Fatalf("%s to %s: %v", step.from, step.to, err)
		}
	}
	queued, err := s.Get(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	queued.State = draft.Sending
	if _, err := s.Update(ctx, queued, draft.Queued); err != nil {
		t.Fatalf("the queued draft: %v", err)
	}
	failed.State, failed.Error = draft.Held, ""
	if _, err := s.Update(ctx, failed, draft.Failed); err != nil {
		t.Fatalf("the failed draft back to held: %v", err)
	}

	books, err := New(db).Books(ctx, acmePull)
	if err != nil {
		t.Fatal(err)
	}
	recs := books[acmePull].Records
	if len(recs) != 2 || recs[0].Record.Question == nil || !recs[0].Record.Question.Publishes ||
		recs[1].Record.Question == nil || len(recs[1].Record.Question.Options) != 2 || !recs[1].Record.Question.Options[0].Publishes {
		t.Fatalf("records %+v", recs)
	}
}

func TestAPlanToVersion13IsNotReadableAtVersion12(t *testing.T) {
	t.Parallel()
	path := storeAtV12(t)
	p, err := current.plan(path, t.TempDir(), migrateOpts(path).Now())
	if err != nil || !p.Pending() || p.ReadableAfter() {
		t.Fatalf("%+v %v: a release at version 12 would ignore holds and revisions", p, err)
	}
}

func TestGateSessionsStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, path := openTemp(t)
	now := func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) }
	opened, err := db.Gates(now).Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: acmePull, Subject: "1", Intents: []string{"approve"},
		Session: "s-review", ApprovedRev: 1, Sessions: []string{"s-review", "s-address"}})
	if err != nil {
		t.Fatal(err)
	}
	none, err := db.Gates(now).Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: acmePull, Subject: "2", Intents: []string{"approve"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Gates(now).Get(ctx, opened.ID)
	if err != nil || !slices.Equal(got.Sessions, []string{"s-review", "s-address"}) || got.Session != "s-review" || got.ApprovedRev != 1 {
		t.Fatalf("after reopening %+v %v", got, err)
	}
	if got, err := reopened.Gates(now).Get(ctx, none.ID); err != nil || got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("a gate no session stamps reads %#v %v", got.Sessions, err)
	}
}

// heldWithGate is a held draft on u that no task holds, and its open gate at its rev.
func heldWithGate(t *testing.T, db *DB, u unit.Unit) (draft.Draft, gate.Gate) {
	t.Helper()
	return heldWithGateTo(t, db, u, "")
}

// heldWithGateTo is heldWithGate with the draft's target.
func heldWithGateTo(t *testing.T, db *DB, u unit.Unit, target string) (draft.Draft, gate.Gate) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	d, err := db.Drafts().Insert(ctx, draft.Draft{Unit: u, Kind: draft.Kind("reply"), Target: target, Text: "hi", Origin: draft.Origin{Kind: draft.OriginTurn, Session: "s-1"},
		State: draft.Held, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	g, err := db.Gates(func() time.Time { return at }).Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: u, Subject: strconv.FormatInt(int64(d.ID), 10),
		Intents: []string{"approve"}, ApprovedRev: d.Rev, Sessions: []string{"s-1"}})
	if err != nil {
		t.Fatal(err)
	}
	return d, g
}

// approvesAndSends approves gate g of draft id and takes the draft from held to sent, as the outbox does once
// the gate's ApprovedRev is the draft's rev.
func approvesAndSends(t *testing.T, db *DB, id draft.ID, g gate.Gate) {
	t.Helper()
	ctx := context.Background()
	gs := db.Gates(func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) })
	d, err := db.Drafts().Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := gs.Get(ctx, g.ID); err != nil || got.ApprovedRev != d.Rev {
		t.Fatalf("the draft is at rev %d and its gate approves rev %d %v", d.Rev, got.ApprovedRev, err)
	}
	approved, err := gs.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: "person"})
	if err != nil || approved.ApprovedRev != d.Rev {
		t.Fatalf("approved %+v %v", approved, err)
	}
	for _, step := range []struct{ from, to draft.State }{{draft.Held, draft.Queued}, {draft.Queued, draft.Sending}, {draft.Sending, draft.Sent}} {
		d.State = step.to
		if d, err = db.Drafts().Update(ctx, d, step.from); err != nil {
			t.Fatalf("%s to %s: %v", step.from, step.to, err)
		}
	}
}

func TestATaskAdoptingAHeldDraftKeepsItsApproval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	d, g := heldWithGate(t, db, acmePull)
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: "t-1", State: "inbox", Category: task.CategoryInbox,
		Units: []unit.Unit{acmePull}, Origin: task.OriginUser, Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
	adopted, err := db.Drafts().Get(ctx, d.ID)
	if err != nil || adopted.Task != "t-1" || adopted.Rev != d.Rev || adopted.Version == d.Version {
		t.Fatalf("the task did not adopt the draft as a write that keeps its content: %+v %v", adopted, err)
	}
	approvesAndSends(t, db, d.ID, g)
}

var (
	rehomedFrom = unit.Unit{Provider: "slack", Key: "T0123/C0123/1700000000.000200"}
	rehomedTo   = unit.Unit{Provider: "slack", Key: "T0123/C0123/seg/1700000000000100"}
)

func TestARehomedDraftIsApprovedAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, target := range map[string]string{"targeted": "thread:C0123/1700000000.000200", "untargeted": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db, _ := openTemp(t)
			d, g := heldWithGateTo(t, db, rehomedFrom, target)
			if n, err := db.RehomeDrafts(ctx, map[unit.Unit]unit.Unit{rehomedFrom: rehomedTo}); err != nil || n != 1 {
				t.Fatalf("moved %d %v", n, err)
			}
			moved, err := db.Drafts().Get(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := db.Gates(func() time.Time { return noteAt }).Get(ctx, g.ID)
			if err != nil || got.Unit != rehomedTo || got.ApprovedRev != g.ApprovedRev || moved.Rev != d.Rev+1 {
				t.Fatalf("a draft moved to rev %d and its gate on %s approves %d %v, want the old %d", moved.Rev, got.Unit, got.ApprovedRev, err, g.ApprovedRev)
			}
			revs, err := db.Drafts().Revisions(ctx, d.ID)
			if err != nil || len(revs) != 2 || revs[1].Rev != moved.Rev {
				t.Fatalf("the rehome's revision %+v %v", revs, err)
			}
		})
	}
}

func TestAChangeToWhatIsPostedLeavesTheOldApprovalBehind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, change := range map[string]func(*draft.Draft){
		"text":      func(d *draft.Draft) { d.Text = "hello" },
		"payload":   func(d *draft.Draft) { d.Payload = json.RawMessage(`{"event":"APPROVE"}`) },
		"basis":     func(d *draft.Draft) { d.Basis = "f00d" },
		"target":    func(d *draft.Draft) { d.Target = "thread:C0123/1.2" },
		"reaction":  func(d *draft.Draft) { d.Reaction = "eyes" },
		"broadcast": func(d *draft.Draft) { d.Broadcast = draft.BroadcastOn },
		"delivery":  func(d *draft.Draft) { d.Delivery = "review" },
		"kind":      func(d *draft.Draft) { d.Kind, d.Purpose = "nudge", "nudge" },
		"unit":      func(d *draft.Draft) { d.Unit = unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"} },
		"provider":  func(d *draft.Draft) { d.Unit = unit.Unit{Provider: "slack", Key: acmePull.Key} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db, _ := openTemp(t)
			d, g := heldWithGate(t, db, acmePull)
			change(&d)
			moved, err := db.Drafts().Update(ctx, d, draft.Held)
			if err != nil {
				t.Fatal(err)
			}
			got, err := db.Gates(func() time.Time { return noteAt }).Get(ctx, g.ID)
			if err != nil || got.ApprovedRev != g.ApprovedRev || got.ApprovedRev == moved.Rev {
				t.Fatalf("a change of %s to rev %d moved the gate's approval from %d to %d %v", name, moved.Rev, g.ApprovedRev, got.ApprovedRev, err)
			}
		})
	}
}

func TestAMoveThatPostsTheSameKeepsTheApproval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	d, g := heldWithGate(t, db, acmePull)
	d.Why, d.Hold, d.State = "because", true, draft.Queued
	moved, err := db.Drafts().Update(ctx, d, draft.Held)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := db.Gates(func() time.Time { return noteAt }).Get(ctx, g.ID); err != nil || got.ApprovedRev != moved.Rev {
		t.Fatalf("the draft moved to rev %d and its gate approves %d %v", moved.Rev, got.ApprovedRev, err)
	}
}

func TestAnAuthorNamedWithoutAChangeKeepsTheStoredOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	d, _ := heldWithGate(t, db, acmePull)
	d.By, d.State = draft.Actor{Kind: draft.ActorSession, Session: "s-other"}, draft.Queued
	moved, err := db.Drafts().Update(ctx, d, draft.Held)
	want := draft.Actor{Kind: draft.ActorSession, Session: "s-1"}
	if err != nil || moved.By != want {
		t.Fatalf("returned author %+v %v, want %+v", moved.By, err, want)
	}
	if got, err := db.Drafts().Get(ctx, d.ID); err != nil || got.By != want {
		t.Fatalf("stored author %+v %v, want %+v", got.By, err, want)
	}
}

func TestAnUpdateWithoutAnAuthorKeepsTheStoredOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	d, _ := heldWithGate(t, db, acmePull)
	session := draft.Actor{Kind: draft.ActorSession, Session: "s-1"}
	d.By, d.State = draft.Actor{}, draft.Queued
	moved, err := db.Drafts().Update(ctx, d, draft.Held)
	if err != nil || moved.By != session {
		t.Fatalf("a state move by no one returned author %+v %v", moved.By, err)
	}
	if got, err := db.Drafts().Get(ctx, d.ID); err != nil || got.By != session {
		t.Fatalf("a state move by no one stored author %+v %v", got.By, err)
	}
	moved.State, moved.By, moved.Text = draft.Held, draft.Actor{}, "edited"
	edited, err := db.Drafts().Update(ctx, moved, draft.Queued)
	if err != nil || edited.By != (draft.Actor{Kind: draft.ActorPerson}) {
		t.Fatalf("an edit by no one is a person's: %+v %v", edited.By, err)
	}
	if got, err := db.Drafts().Get(ctx, d.ID); err != nil || got.By != (draft.Actor{Kind: draft.ActorPerson}) {
		t.Fatalf("stored %+v %v", got.By, err)
	}
}

// TestVersion13GivesADraftItsVersionAndCause reads a failed draft's cause from the errors a version 12
// release wrote, leaves any other error as it is, and counts each draft's writes from its old rev.
func TestVersion13GivesADraftItsVersionAndCause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v12.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{12, string(frozen), nil})
	const failedRow = `INSERT INTO drafts (id, provider, unit_key, items, kind, text, original, origin_kind, state, created_at, updated_at, purpose, delivery, effects, rev, error)
		VALUES (%d, 'slack', 'C0123/1.2', '[]', 'reply', 'hi', 'hi', 'turn', 'failed', '2026-11-04T10:00:00.000000000Z', '2026-11-04T10:00:00.000000000Z', 'reply', 'message', '[]', %d, '%s')`
	want := map[int64]struct {
		rev   int64
		error string
		cause draft.Cause
	}{
		1: {0, "not confirmed after a restart", draft.CauseUnconfirmed},
		2: {3, "may have been posted: the call timed out", draft.CauseUnconfirmed},
		3: {5, "stale: whatever the API said", ""},
	}
	for id, w := range want {
		rawExec(t, path, fmt.Sprintf(failedRow, id, w.rev, w.error))
	}
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, w := range want {
		d, err := db.Drafts().Get(ctx, draft.ID(id))
		if err != nil || d.Cause != w.cause || d.Error != w.error || d.Rev != 1 || d.Version != w.rev+1 {
			t.Errorf("draft %d: rev %d version %d cause %q error %q %v, want rev 1 version %d %q %q", id, d.Rev, d.Version, d.Cause, d.Error, err, w.rev+1, w.cause, w.error)
		}
	}
	var n int
	if err := db.R().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d triggers %v, want none", n, err)
	}
}
