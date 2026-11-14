package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

const (
	postedFact        = "slack.waited_posted"
	reactionsFact     = "slack.waited_reactions"
	unmentionableFact = "slack.unmentionable"
)

// movesRig is whereRig whose unit's plugin tells who moved since an ask and whom a message cannot mention.
func movesRig(t *testing.T, facts workflow.Facts) *draftRig {
	t.Helper()
	r, _ := whereRig(t)
	r.deps.People = map[unit.ProviderID]builtin.PeopleFacts{"slack": {Posted: postedFact, Reactions: reactionsFact, Unmentionable: unmentionableFact}}
	all := workflow.Facts{"slack.ended": false, membersFact: []string{meP.Key, ana.Key, bruno.Key}}
	for k, v := range facts {
		all[k] = v
	}
	r.units.Put(draftThread, all)
	return r
}

// proposedFacts are the draft facts of the one held draft of the rig's nudge workflow on the thread, proposed
// to Bruno over items.
func (r *draftRig) proposedFacts(its ...string) workflow.Facts {
	r.t.Helper()
	ctx := context.Background()
	if _, err := r.svc.Propose(ctx, outbox.Proposal{Unit: draftThread, Purpose: "nudge", Text: "@{" + bruno.Key + "}, any news?", Items: its,
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "nudges", Rule: "nudges", Key: draftThread.String()}}); err != nil {
		r.t.Fatal(err)
	}
	pds, err := draftWithdrawals{d: r.deps}.Proposed(ctx, "")
	if err != nil || len(pds) != 1 {
		r.t.Fatalf("proposed %+v, %v", pds, err)
	}
	return pds[0].Facts
}

func TestANudgeToSomeoneAMessageCannotMentionIsCounted(t *testing.T) {
	t.Parallel()
	team := items.Person{Key: "group:the data team", Name: "the data team"}
	r := movesRig(t, workflow.Facts{postedFact: []string{}, reactionsFact: []string{}, unmentionableFact: []string{team.Key}})
	r.items.set(draftThread, openItem("i1", "The table", r.now.Add(-48*time.Hour), team))
	ctx := context.Background()
	if _, err := r.svc.Propose(ctx, outbox.Proposal{Unit: draftThread, Purpose: "nudge", Text: "@{" + team.Key + "}, any news?", Items: []string{"i1"},
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "nudges", Rule: "nudges", Key: draftThread.String()}}); err != nil {
		t.Fatal(err)
	}
	pds, err := draftWithdrawals{d: r.deps}.Proposed(ctx, "")
	if err != nil || len(pds) != 1 || pds[0].Facts["draft.people_unmentionable"] != 1 {
		t.Fatalf("proposed %+v %v, want the team named by text counted unmentionable", pds, err)
	}
}

func TestAStaleItemsAgeIsTheNewestAsksInWorkingDays(t *testing.T) {
	t.Parallel()
	r := movesRig(t, nil)
	old := openItem("i1", "The budget", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), bruno)
	old.Origin.At = old.CreatedAt
	fresh := openItem("i2", "The table", time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), bruno)
	r.items.set(draftThread, old, fresh)
	if got := r.proposedFacts("i1", "i2")["draft.items_age_days"]; got != 3 {
		t.Fatalf("items_age_days %v, want 3 working days since Friday's ask", got)
	}
}

func TestAReopenedItemIsAsYoungAsItsReopening(t *testing.T) {
	t.Parallel()
	r := movesRig(t, nil)
	it := openItem("i1", "The budget", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), bruno)
	it.ReopenedAt = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	r.items.set(draftThread, it)
	if got := r.proposedFacts("i1")["draft.items_age_days"]; got != 1 {
		t.Fatalf("items_age_days %v, want 1 working day since it reopened", got)
	}
}

func TestItemsAgeDaysMatchesWorkdayStepsAndIsCheapForAnAncientAsk(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("BRT", -3*60*60)
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, loc)
	for h := 0; h < 24*30; h += 5 {
		asked := now.Add(-time.Duration(h) * time.Hour)
		want := 0
		for !task.AddWorkdays(asked, want+1, loc).After(now) {
			want++
		}
		if got := itemsAgeDays([]items.Item{{CreatedAt: asked}}, now); got != want {
			t.Fatalf("asked %v: %d working days, want %d", asked, got, want)
		}
	}
	monday2000, monday2026 := time.Date(2000, 1, 3, 10, 0, 0, 0, loc), time.Date(2026, 10, 12, 10, 0, 0, 0, loc)
	weeks := int(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC).Sub(time.Date(2000, 1, 3, 0, 0, 0, 0, time.UTC)).Hours()) / 24 / 7
	if got := itemsAgeDays([]items.Item{{CreatedAt: monday2000}}, monday2026); got != weeks*5 {
		t.Fatalf("an ask of 2000: %d working days, want %d", got, weeks*5)
	}
}

func TestANudgeLeavesOutWhomAMessageCannotMentionAndKeepsTheOthers(t *testing.T) {
	t.Parallel()
	team := items.Person{Key: "group:the data team", Name: "the data team"}
	r := movesRig(t, workflow.Facts{postedFact: []string{}, reactionsFact: []string{}, unmentionableFact: []string{team.Key}})
	r.wf.Steps[0].Args["leave_unmentionable"] = "true"
	log := &lines{}
	r.s.d.Log = log.add
	r.items.set(draftThread, openItem("i1", "The table", r.now.Add(-48*time.Hour), team), openItem("i2", "The budget", r.now.Add(-48*time.Hour), bruno))
	r.sweepSteps(draftThread)
	if !log.has("leaves out " + team.Key) {
		t.Errorf("log %q, want the team left out named", log.all)
	}
	ds := held(r.allDrafts())
	if len(ds) != 1 || strings.Contains(ds[0].Text, team.Key) || !strings.Contains(ds[0].Text, bruno.Key) || !slices.Equal(ds[0].Items, []string{"i2"}) {
		t.Fatalf("drafts %+v, want one to Bruno listing his item, the team left out", ds)
	}
}

// rfc is t as the plugin writes a move's time.
func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// nudgeTask makes t-1 hold its items, nudged last at nudged (zero for never), the subject of proposedTaskFacts.
func (r *draftRig) nudgeTask(nudged time.Time, its ...string) {
	r.t.Helper()
	e := task.Task{ID: "t-1", State: "waiting", Category: task.CategoryWaiting, Units: []unit.Unit{draftThread}}
	for _, id := range its {
		e.Items = append(e.Items, task.ItemRef{Unit: draftThread, Item: id})
	}
	if !nudged.IsZero() {
		e.SetProp(task.PropLastNudge, task.Value{Time: nudged}, task.WriterWorkflow, nudged)
	}
	r.tasks.mu.Lock()
	r.tasks.m[e.ID] = e
	r.tasks.mu.Unlock()
}

// proposedTaskFacts are the draft facts of a nudge to Bruno over its, proposed on task t-1.
func (r *draftRig) proposedTaskFacts(its ...string) workflow.Facts {
	r.t.Helper()
	ctx := context.Background()
	if _, err := r.svc.Propose(ctx, outbox.Proposal{Unit: draftThread, Task: "t-1", Purpose: "nudge", Text: "@{" + bruno.Key + "}, any news?", Items: its,
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "nudges", Rule: "nudges", Key: draftThread.String()}}); err != nil {
		r.t.Fatal(err)
	}
	pds, err := draftWithdrawals{d: r.deps}.Proposed(ctx, "")
	if err != nil || len(pds) != 1 {
		r.t.Fatalf("proposed %+v, %v", pds, err)
	}
	return pds[0].Facts
}

func TestANudgedPersonsMovesCountSinceTheLaterOfTheAskAndTheLastNudge(t *testing.T) {
	t.Parallel()
	asked := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	replied := asked.Add(24 * time.Hour)
	for _, c := range []struct {
		name    string
		nudged  time.Time
		posted  []string
		reacted []string
		post    time.Time
		react   time.Time
	}{
		{"Bruno replied after the ask", time.Time{}, []string{"i1 " + bruno.Key + " " + rfc(replied)}, nil, replied, time.Time{}},
		{"Bruno replied before the last nudge", replied.Add(time.Hour), []string{"i1 " + bruno.Key + " " + rfc(replied)}, nil, time.Time{}, time.Time{}},
		{"only Ana replied", time.Time{}, []string{"i1 " + ana.Key + " " + rfc(replied)}, nil, time.Time{}, time.Time{}},
		{"Bruno reacted with an hourglass on the ask", time.Time{}, nil, []string{"i1 " + bruno.Key + " " + rfc(asked) + " hourglass"}, time.Time{}, asked},
		{"Bruno only reacted with eyes", time.Time{}, nil, []string{"i1 " + bruno.Key + " " + rfc(asked) + " eyes"}, time.Time{}, time.Time{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := movesRig(t, workflow.Facts{postedFact: c.posted, reactionsFact: c.reacted, unmentionableFact: []string{}})
			it := openItem("i1", "The budget", asked, bruno)
			it.Origin.At = asked
			r.items.set(draftThread, it)
			r.nudgeTask(c.nudged, "i1")
			got := r.proposedTaskFacts("i1")
			if got["draft.people_posted_at"] != c.post || got["draft.people_reacted_at"] != c.react {
				t.Fatalf("posted_at %v, reacted_at %v; want %v, %v", got["draft.people_posted_at"], got["draft.people_reacted_at"], c.post, c.react)
			}
		})
	}
}

func TestAMoveOnOneItemDoesNotCountForAnother(t *testing.T) {
	t.Parallel()
	asked := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	r := movesRig(t, workflow.Facts{postedFact: []string{"i1 " + bruno.Key + " " + rfc(asked.Add(time.Hour))}, reactionsFact: []string{}, unmentionableFact: []string{}})
	r.items.set(draftThread, openItem("i1", "The budget", asked, bruno), openItem("i2", "The table", asked, bruno))
	r.nudgeTask(time.Time{}, "i1", "i2")
	if got := r.proposedTaskFacts("i1", "i2")["draft.people_posted_at"]; got != (time.Time{}) {
		t.Fatalf("posted_at %v, want zero: Bruno moved on the budget, not on the table", got)
	}
}

func TestAMoveBeforeAReopeningDoesNotCount(t *testing.T) {
	t.Parallel()
	asked := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	r := movesRig(t, workflow.Facts{postedFact: []string{"i1 " + bruno.Key + " " + rfc(asked.Add(time.Hour))}, reactionsFact: []string{}, unmentionableFact: []string{}})
	it := openItem("i1", "The budget", asked, bruno)
	it.ReopenedAt = asked.Add(48 * time.Hour)
	r.items.set(draftThread, it)
	r.nudgeTask(time.Time{}, "i1")
	if got := r.proposedTaskFacts("i1")["draft.people_posted_at"]; got != (time.Time{}) {
		t.Fatalf("posted_at %v, want zero: Bruno's post predates the reopening", got)
	}
}

func TestANudgeMentionsWhomTheProfileDoesNotLeaveOut(t *testing.T) {
	t.Parallel()
	team := items.Person{Key: "group:the data team", Name: "the data team"}
	r := movesRig(t, workflow.Facts{postedFact: []string{}, reactionsFact: []string{}, unmentionableFact: []string{team.Key}})
	r.items.set(draftThread, openItem("i1", "The table", r.now.Add(-48*time.Hour), team))
	r.sweepSteps(draftThread)
	if ds := held(r.allDrafts()); len(ds) != 1 || !strings.Contains(ds[0].Text, team.Key) {
		t.Fatalf("drafts %+v, want the team mentioned: the step does not leave the unmentionable out", ds)
	}
}

func TestWorkdaysBetweenReadsTheWallClockOnADSTDay(t *testing.T) {
	t.Parallel()
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	from, to := time.Date(2023, 4, 27, 1, 15, 0, 0, cairo), time.Date(2023, 4, 28, 1, 30, 0, 0, cairo)
	want := 0
	for !task.AddWorkdays(from, want+1, cairo).After(to) {
		want++
	}
	if got := workdaysBetween(from, to); got != want || want != 1 {
		t.Fatalf("Thursday 01:15 to Friday 01:30 across Cairo's spring forward: %d working days, want %d (1)", got, want)
	}
}
