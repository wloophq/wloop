package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var (
	meP    = items.Person{Key: "slack:U000ME", Name: "Me"}
	bruno  = items.Person{Key: "slack:U000BRUNO", Name: "Bruno"}
	unitWF = "@{{range $i, $p := .People}}{{if $i}} @{{end}}{{\"{\"}}{{$p.Key}}}{{end}}, any news?{{range .Items}}\n- {{.Line}}{{end}}"
)

// nudgeRig proposes one stalled nudge per conversation, withdrawn as the bundled one is.
func nudgeRig(t *testing.T) *draftRig {
	t.Helper()
	wf := draftWF("nudges", "purpose", "nudge", "items", "owed")
	wf.Withdraw = automationtest.MatchFunc(func(v workflow.Vars) bool {
		return v.Facts["draft.person_waited"] != true || v.Facts["draft.items_owed"] == 0 || v.Facts["draft.person_asks_you"] == true
	}, "draft.person_waited", "draft.items_owed", "draft.person_asks_you")
	r := newDraftRig(t, wf, unitWF)
	r.deps.Me = func() task.Me { return task.Me{Keys: []string{meP.Key}} }
	r.units.Put(draftThread, workflow.Facts{"slack.ended": false})
	return r
}

func asking(id, summary string, from items.Person, created time.Time) items.Item {
	it := openItem(id, summary, created, meP)
	it.Requester = from
	return it
}

func TestANudgeToSeveralPeopleIsOneDraftMentioningAll(t *testing.T) {
	t.Parallel()
	r := nudgeRig(t)
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-96*time.Hour), ana),
		openItem("i2", "The budget", r.now.Add(-72*time.Hour), bruno), openItem("i3", "Mine", r.now.Add(-72*time.Hour), meP))
	r.run(draftThread)
	ds := r.drafts()
	if len(ds) != 1 {
		t.Fatalf("%d drafts, want one for the conversation", len(ds))
	}
	d := ds[0]
	if !strings.HasPrefix(d.Text, "@{"+ana.Key+"} @{"+bruno.Key+"}, any news?") || strings.Contains(d.Text, meP.Key) {
		t.Errorf("text %q", d.Text)
	}
	if !slices.Equal(d.Items, []string{"i1", "i2"}) || d.Origin.Key != draftThread.String() {
		t.Errorf("items %v, key %q", d.Items, d.Origin.Key)
	}
}

func TestAPersonWhoAsksYouIsLeftOutOfTheNudge(t *testing.T) {
	t.Parallel()
	r := nudgeRig(t)
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-96*time.Hour), ana),
		openItem("i2", "The budget", r.now.Add(-72*time.Hour), bruno), asking("i3", "Your review", bruno, r.now.Add(-24*time.Hour)))
	r.run(draftThread)
	ds := r.drafts()
	if len(ds) != 1 || strings.Contains(ds[0].Text, bruno.Key) || !strings.Contains(ds[0].Text, ana.Key) || !slices.Equal(ds[0].Items, []string{"i1"}) {
		t.Fatalf("drafts %+v, want one to Ana listing her item only", ds)
	}
}

func TestANudgeEveryoneOfWhomAsksYouIsNotProposed(t *testing.T) {
	t.Parallel()
	r := nudgeRig(t)
	r.items.set(draftThread, openItem("i1", "The budget", r.now.Add(-72*time.Hour), bruno), asking("i2", "Your review", bruno, r.now.Add(-24*time.Hour)))
	if got := r.check(draftThread); got != "its withdraw when holds" {
		t.Fatalf("refusal %q", got)
	}
}

func TestAPerUnitNudgeSupersedesHeldPerPersonNudges(t *testing.T) {
	t.Parallel()
	r := nudgeRig(t)
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-96*time.Hour), ana), openItem("i2", "The budget", r.now.Add(-72*time.Hour), bruno))
	ctx := context.Background()
	var old []draft.ID
	for _, p := range []items.Person{ana, bruno} {
		d, err := r.svc.Propose(ctx, outbox.Proposal{Unit: draftThread, Purpose: "nudge", Text: "@{" + p.Key + "}, any news?",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "nudges", Rule: "nudges", Key: draftThread.String() + ":" + p.Key}})
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, d.ID)
	}
	r.run(draftThread)
	var held int
	for _, d := range r.drafts() {
		switch {
		case slices.Contains(old, d.ID):
			if d.State != draft.Dropped || d.Reason != draft.WithdrawSuperseded {
				t.Errorf("per-person draft %d: %s %q, want dropped superseded", d.ID, d.State, d.Reason)
			}
		case d.State == draft.Held:
			held++
			if d.Origin.Key != draftThread.String() {
				t.Errorf("new draft keyed %q", d.Origin.Key)
			}
		}
	}
	if held != 1 {
		t.Errorf("%d held per-unit drafts, want one", held)
	}
}

func TestTheWithdrawSweepReadsAPerUnitDraftsPeople(t *testing.T) {
	t.Parallel()
	r := nudgeRig(t)
	anas, brunos := openItem("i1", "The deck", r.now.Add(-96*time.Hour), ana), openItem("i2", "The budget", r.now.Add(-72*time.Hour), bruno)
	r.items.set(draftThread, anas, brunos)
	r.run(draftThread)
	ctx := context.Background()
	facts := func() workflow.Facts {
		t.Helper()
		ps, err := draftWithdrawals{d: r.deps}.Proposed(ctx, "nudges")
		if err != nil || len(ps) != 1 {
			t.Fatalf("proposed %+v %v", ps, err)
		}
		return ps[0].Facts
	}
	anas.State = items.Resolved
	r.items.set(draftThread, anas, brunos)
	if f := facts(); f["draft.person_waited"] != true || f["draft.items_owed"] != 1 {
		t.Errorf("Bruno's item still open: %v", f)
	}
	brunos.State = items.Resolved
	r.items.set(draftThread, anas, brunos)
	if f := facts(); f["draft.items_owed"] != 0 {
		t.Errorf("all resolved: %v", f)
	}
}

func TestDraftPeopleReadsTheKeyOrTheMentions(t *testing.T) {
	t.Parallel()
	perPerson := draft.Draft{Unit: draftThread, Origin: draft.Origin{Key: draftThread.String() + ":" + ana.Key}, Original: "@{" + bruno.Key + "}"}
	if got := draftPeople(perPerson, draftThread); !slices.Equal(got, []string{ana.Key}) {
		t.Errorf("per person: %v", got)
	}
	perUnit := draft.Draft{Unit: draftThread, Origin: draft.Origin{Key: draftThread.String()},
		Original: "@{" + ana.Key + "} @{" + bruno.Key + "} cc @{" + ana.Key + "}"}
	if got := draftPeople(perUnit, draftThread); !slices.Equal(got, []string{ana.Key, bruno.Key}) {
		t.Errorf("per unit: %v", got)
	}
}
