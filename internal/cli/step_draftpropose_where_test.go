package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var (
	dmWithAna = unit.Unit{Provider: "slack", Key: "T000AAA/D000ANA/seg/s1"}
	carla     = items.Person{Key: "slack:U000CARLA", Name: "Carla"}
)

const (
	membersFact = "slack.members"
	dmText      = "Hi {{.Person.Name}}, any news?{{range .Items}}\n> {{.Line}}{{end}}"
)

// whereRig nudges a conversation's members in it and everyone else the items wait on in their direct
// conversation, as the bundled Slack nudge does.
func whereRig(t *testing.T) (*draftRig, *lines) {
	t.Helper()
	moot := automationtest.MatchFunc(func(v workflow.Vars) bool {
		return v.Facts["draft.person_waited"] != true || v.Facts["draft.items_owed"] == 0 || v.Facts["draft.person_asks_you"] == true
	}, "draft.person_waited", "draft.items_owed", "draft.person_asks_you")
	wf := draftWF("nudges", "purpose", "nudge", "items", "owed", "to", "members")
	wf.Withdraw = moot
	wf.WithdrawRules = []workflow.WithdrawRule{{When: automationtest.MatchFunc(func(v workflow.Vars) bool {
		n, _ := v.Facts["draft.people_outside"].(int)
		return n > 0
	}, "draft.people_outside"), Reason: draft.WithdrawOutside}}
	dm := draftWF("nudges-dm", "purpose", "nudge", "items", "owed", "per", "person", "to", "non_members", "target", "person", "text", "dm.md.tmpl")
	dm.Withdraw = moot
	r := newDraftRig(t, wf, unitWF)
	r.others = []workflow.Workflow{dm}
	r.deps.Workflow = func(name string) (workflow.Workflow, bool) {
		i := slices.IndexFunc(r.workflows(), func(w workflow.Workflow) bool { return w.Name == name })
		if i < 0 {
			return workflow.Workflow{}, false
		}
		return r.workflows()[i], true
	}
	writeFileOf(t, r, dm.Name, "dm.md.tmpl", dmText)
	log := &lines{}
	r.s.d.Log = log.add
	r.deps.Me = func() task.Me { return task.Me{Keys: []string{meP.Key}} }
	r.deps.Members = map[unit.ProviderID]string{"slack": membersFact}
	r.deps.Facts = r.units.Facts
	r.units.Put(dmWithAna, workflow.Facts{"slack.ended": false, membersFact: []string{meP.Key, ana.Key}})
	r.units.Put(draftThread, workflow.Facts{"slack.ended": false, membersFact: []string{meP.Key, ana.Key, bruno.Key}})
	return r, log
}

type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, fmt.Sprintf(format, args...))
}

func (l *lines) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.ContainsFunc(l.all, func(x string) bool { return strings.Contains(x, s) })
}

func writeFileOf(t *testing.T, r *draftRig, workflowName, name, body string) {
	t.Helper()
	wd := filepath.Join(r.cfg.Profile, "workflows", workflowName)
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *draftRig) workflows() []workflow.Workflow { return append([]workflow.Workflow{r.wf}, r.others...) }

// sweepSteps passes each of the rig's workflows over u as a sweep does: a step that refuses stops its run.
func (r *draftRig) sweepSteps(u unit.Unit) {
	r.t.Helper()
	ctx := context.Background()
	for _, w := range r.workflows() {
		why := workflow.Why{Workflow: w.Name}
		for _, st := range w.Steps {
			refusal, err := r.s.Check(ctx, st, u, why, automation.CheckOpts{})
			if err != nil {
				r.t.Fatal(err)
			}
			if refusal != "" {
				break
			}
			_, err = r.s.Run(ctx, st, u, why, automation.RunOpts{})
			var deferred automation.Deferred
			if err != nil && !errors.As(err, &deferred) {
				r.t.Fatal(err)
			}
		}
	}
}

// allDrafts are the drafts of every workflow of the rig.
func (r *draftRig) allDrafts() []draft.Draft {
	r.t.Helper()
	var out []draft.Draft
	for _, w := range r.workflows() {
		ds, err := r.svc.List(context.Background(), outbox.Filter{Workflow: w.Name})
		if err != nil {
			r.t.Fatal(err)
		}
		out = append(out, ds...)
	}
	return out
}

func dmItem(id, summary string, created time.Time, waiting ...items.Person) items.Item {
	it := openItem(id, summary, created, waiting...)
	it.Unit, it.Origin = dmWithAna, unit.Anchor{Unit: dmWithAna, Permalink: "https://example.test/" + id}
	return it
}

func held(ds []draft.Draft) []draft.Draft {
	return slices.DeleteFunc(slices.Clone(ds), func(d draft.Draft) bool { return d.State != draft.Held })
}

func TestANudgeForSomeoneOutsideADMGoesToTheirOwnDM(t *testing.T) {
	t.Parallel()
	r, _ := whereRig(t)
	r.items.set(dmWithAna, dmItem("i1", "Bruno to write the study of the queue", r.now.Add(-96*time.Hour), bruno))
	r.sweepSteps(dmWithAna)
	ds := held(r.allDrafts())
	if len(ds) != 1 {
		t.Fatalf("drafts %+v, want the one to Bruno's DM alone", ds)
	}
	d := ds[0]
	if d.Target != bruno.Key || d.Unit != dmWithAna || d.Origin.Key != dmWithAna.String()+":"+bruno.Key {
		t.Errorf("draft to %q on %s keyed %q, want Bruno's DM proposed on the DM with Ana", d.Target, d.Unit, d.Origin.Key)
	}
	if !strings.Contains(d.Text, "Hi Bruno") || !strings.Contains(d.Text, "> Bruno to write the study of the queue") || strings.Contains(d.Text, "https://") {
		t.Errorf("text %q, want the item quoted with no link to the DM Bruno cannot open", d.Text)
	}
	r.sweepSteps(dmWithAna)
	if got := held(r.allDrafts()); len(got) != 1 {
		t.Errorf("a second sweep left %d held drafts, want the one", len(got))
	}
}

func TestANudgeForTheDMsPeerStaysInTheDM(t *testing.T) {
	t.Parallel()
	r, _ := whereRig(t)
	r.items.set(dmWithAna, dmItem("i1", "The deck", r.now.Add(-96*time.Hour), ana))
	r.sweepSteps(dmWithAna)
	ds := held(r.allDrafts())
	if len(ds) != 1 || ds[0].Target != "" || !strings.HasPrefix(ds[0].Text, "@{"+ana.Key+"}, any news?") {
		t.Fatalf("drafts %+v, want one to Ana in the DM", ds)
	}
}

func TestADMWaitingOnItsPeerAndAnotherSplitsTheNudge(t *testing.T) {
	t.Parallel()
	r, _ := whereRig(t)
	r.items.set(dmWithAna, dmItem("i1", "The deck", r.now.Add(-96*time.Hour), ana), dmItem("i2", "The study", r.now.Add(-72*time.Hour), bruno))
	r.sweepSteps(dmWithAna)
	ds := held(r.allDrafts())
	var inDM, toBruno []draft.Draft
	for _, d := range ds {
		if d.Target == "" {
			inDM = append(inDM, d)
		} else {
			toBruno = append(toBruno, d)
		}
	}
	if len(inDM) != 1 || strings.Contains(inDM[0].Text, bruno.Key) || !slices.Equal(inDM[0].Items, []string{"i1"}) {
		t.Errorf("DM drafts %+v, want one to Ana listing her item", inDM)
	}
	if len(toBruno) != 1 || toBruno[0].Target != bruno.Key || !slices.Equal(toBruno[0].Items, []string{"i2"}) {
		t.Errorf("drafts to Bruno %+v, want one listing his item", toBruno)
	}
}

func TestAChannelNudgeMentionsAMemberAsBefore(t *testing.T) {
	t.Parallel()
	r, _ := whereRig(t)
	r.items.set(draftThread, openItem("i1", "The budget", r.now.Add(-72*time.Hour), bruno))
	r.sweepSteps(draftThread)
	ds := held(r.allDrafts())
	if len(ds) != 1 || ds[0].Target != "" || !strings.HasPrefix(ds[0].Text, "@{"+bruno.Key+"}, any news?") {
		t.Fatalf("drafts %+v, want one mentioning Bruno in the thread", ds)
	}
}

func TestAHeldDraftAddressingSomeoneOutsideTheDMIsWithdrawn(t *testing.T) {
	t.Parallel()
	r, _ := whereRig(t)
	r.items.set(dmWithAna, dmItem("i1", "The study", r.now.Add(-96*time.Hour), bruno))
	ctx := context.Background()
	old, err := r.svc.Propose(ctx, outbox.Proposal{Unit: dmWithAna, Purpose: "nudge", Text: "@{" + bruno.Key + "}, any news?", Items: []string{"i1"},
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "nudges", Rule: "nudges", Key: dmWithAna.String()}})
	if err != nil {
		t.Fatal(err)
	}
	pds, err := draftWithdrawals{d: r.deps}.Proposed(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pds) != 1 || pds[0].ID != int64(old.ID) || pds[0].Facts["draft.people_outside"] != 1 {
		t.Fatalf("proposed %+v, want the draft counting Bruno outside", pds)
	}
	ok, err := r.wf.WithdrawRules[0].When.Match(workflow.Vars{Facts: pds[0].Facts})
	if err != nil || !ok {
		t.Fatalf("the outside rule matched %v, %v", ok, err)
	}
}

func TestAHeldDraftInBrunosDMIsNotWithdrawnAsOutside(t *testing.T) {
	t.Parallel()
	r, _ := whereRig(t)
	r.items.set(dmWithAna, dmItem("i1", "The study", r.now.Add(-96*time.Hour), bruno))
	r.sweepSteps(dmWithAna)
	ds := held(r.allDrafts())
	if len(ds) != 1 {
		t.Fatalf("drafts %+v", ds)
	}
	pds, err := draftWithdrawals{d: r.deps}.Proposed(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pds) != 1 || pds[0].Facts["draft.people_outside"] != 0 {
		t.Fatalf("proposed %+v, want Bruno's DM draft counting nobody outside", pds)
	}
}

func TestAnUnknownMembershipAddressesNobodyItCannotPlace(t *testing.T) {
	t.Parallel()
	r, log := whereRig(t)
	r.units.Put(dmWithAna, workflow.Facts{"slack.ended": false})
	r.items.set(dmWithAna, dmItem("i1", "The deck", r.now.Add(-96*time.Hour), ana), dmItem("i2", "The study", r.now.Add(-72*time.Hour), carla))
	r.sweepSteps(dmWithAna)
	for _, d := range r.allDrafts() {
		t.Errorf("draft %d to %q: %q, want none while who reads the DM is unknown", d.ID, d.Target, d.Text)
	}
	if !log.has("who can read " + dmWithAna.String() + " is unknown") {
		t.Errorf("log %q, want the unknown membership logged", log.all)
	}
}

func TestAStepToAllAddressesEveryoneWhateverTheMembers(t *testing.T) {
	t.Parallel()
	r := nudgeRig(t)
	r.deps.Members = map[unit.ProviderID]string{"slack": membersFact}
	r.units.Put(draftThread, workflow.Facts{"slack.ended": false, membersFact: []string{meP.Key}})
	r.items.set(draftThread, openItem("i1", "The budget", r.now.Add(-72*time.Hour), bruno))
	r.run(draftThread)
	if ds := r.drafts(); len(ds) != 1 || !strings.Contains(ds[0].Text, bruno.Key) {
		t.Fatalf("drafts %+v, want the nudge to Bruno as before", ds)
	}
}
