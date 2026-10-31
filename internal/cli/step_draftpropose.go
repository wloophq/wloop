package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// draftProposeStep adapts draft.propose: Check renders the text and holds per
// workflow, subject and person from the drafts; Run proposes in the Outbox.
var draftProposeStep = stepEntry{check: (*stepSet).draftProposeCheck, run: (*stepSet).draftProposeRun}

// draftOutbox is what draft.propose and the withdrawals need of the Outbox
// (*outbox.Service).
type draftOutbox interface {
	Propose(ctx context.Context, p outbox.Proposal) (draft.Draft, error)
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
	Withdraw(ctx context.Context, id draft.ID, reason string) (draft.Draft, error)
}

type draftTasks interface {
	Task(ctx context.Context, id task.ID) (task.Task, error)
}

type draftItems interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// draftDeps are what draft.propose and the workflows' withdrawals read and
// act through.
type draftDeps struct {
	Outbox func() draftOutbox // nil until the Outbox is served
	Tasks  draftTasks
	Items  draftItems
	// Workflow is the loaded manifest of a workflow, before the config.
	Workflow func(name string) (workflow.Workflow, bool)
	Config   func() config.Config
	Now      func() time.Time
	// Enabled says whether a provider's plugin is on; nil: every one is.
	Enabled func(unit.ProviderID) bool
	// Codecs compare a person's keys; nil compares them byte for byte.
	Codecs unit.KeyComparer
}

var errNoDraftDeps = errors.New("draft.propose needs the daemon's Outbox")

func (d *draftDeps) outbox() (draftOutbox, error) {
	if d == nil || d.Outbox == nil {
		return nil, errNoDraftDeps
	}
	o := d.Outbox()
	if o == nil {
		return nil, errNoDraftDeps
	}
	return o, nil
}

// draftArgs are draft.propose's arguments with their defaults.
type draftArgs struct {
	purpose, text, reaction, items, per, target string
	delivery                                    draft.Delivery
	effects                                     []string
	repeatAfter, repeatAfterSent                time.Duration
	forever                                     bool
}

const defaultDraftText = "text.md.tmpl"

func parseDraftArgs(c stepCall, cfg config.Config) (draftArgs, error) {
	a := draftArgs{purpose: strings.TrimSpace(c.arg("purpose")), delivery: draft.Delivery(cmp.Or(c.arg("delivery"), builtin.DraftDeliveries[0])),
		text: cmp.Or(c.arg("text"), defaultDraftText), reaction: strings.Trim(strings.TrimSpace(c.arg("reaction")), ":"),
		items: cmp.Or(c.arg("items"), "all"), per: cmp.Or(c.arg("per"), builtin.DraftPer[0]), target: cmp.Or(c.arg("target"), builtin.DraftTargets[0])}
	if a.purpose == "" {
		return a, errors.New("draft.propose names no purpose")
	}
	if a.text != path.Base(a.text) || strings.HasPrefix(a.text, ".") {
		return a, fmt.Errorf("draft.propose's text %q is not a file of the workflow's directory", a.text)
	}
	a.effects = c.list("effects")
	var err error
	if v := c.arg("repeat_after"); v != "" {
		if a.repeatAfter, err = repeatDuration(v, cfg); err != nil {
			return a, fmt.Errorf("draft.propose's repeat_after: %w", err)
		}
	}
	a.repeatAfterSent = a.repeatAfter
	switch v := c.arg("repeat_after_sent"); {
	case v == builtin.RepeatForever:
		a.forever = true
	case v != "":
		if a.repeatAfterSent, err = repeatDuration(v, cfg); err != nil {
			return a, fmt.Errorf("draft.propose's repeat_after_sent: %w", err)
		}
	}
	return a, nil
}

// draftCandidate is one draft a draft.propose step would propose: per
// subject, or per person the subject waits on.
type draftCandidate struct {
	p       outbox.Proposal
	person  string
	refusal string
	// timed says the refusal is a hold that ends with time.
	timed bool
}

// draftSubject is a step's subject as its drafts see it: the unit they post
// on, and the task when the subject is one.
type draftSubject struct {
	subject, unit unit.Unit
	task          *task.Task
	items         []items.Item
}

func (s *stepSet) draftProposeCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	cands, err := s.draftCandidates(ctx, c)
	if err != nil {
		return "", err
	}
	var refusals []string
	for _, cd := range cands {
		if cd.refusal == "" {
			return "", nil
		}
		refusals = append(refusals, cd.refusal)
	}
	if len(refusals) == 0 {
		return "nothing to propose", nil
	}
	return strings.Join(slices.Compact(refusals), "; "), nil
}

func (s *stepSet) draftProposeRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	cands, err := s.draftCandidates(ctx, c)
	if err != nil {
		return "", err
	}
	ob, err := s.d.Drafts.outbox()
	if err != nil {
		return "", err
	}
	var ids []string
	timed := false
	for _, cd := range cands {
		if cd.refusal != "" {
			timed = timed || cd.timed
			continue
		}
		d, err := ob.Propose(ctx, cd.p)
		switch {
		case errors.Is(err, outbox.ErrDuplicate):
			continue
		case errors.Is(err, outbox.ErrDisabled):
			return "", automation.Refused{Reason: err.Error()}
		case err != nil:
			return "", err
		}
		ids = append(ids, fmt.Sprint(d.ID))
	}
	detail := "nothing new to propose"
	if len(ids) > 0 {
		detail = "proposed draft " + strings.Join(ids, ", ")
	}
	if timed {
		return "", automation.Deferred{Reason: detail + "; a person's draft is held for a while"}
	}
	return detail, nil
}

// draftCandidates are the drafts the step's workflow would propose on its
// subject now, each with the refusal that holds it.
func (s *stepSet) draftCandidates(ctx context.Context, c stepCall) ([]draftCandidate, error) {
	deps := s.d.Drafts
	ob, err := deps.outbox()
	if err != nil {
		return nil, err
	}
	a, err := parseDraftArgs(c, deps.Config())
	if err != nil {
		return nil, err
	}
	w, ok := deps.Workflow(c.why.Workflow)
	if !ok {
		return nil, fmt.Errorf("draft.propose: workflow %s is not loaded", c.why.Workflow)
	}
	sub, err := deps.subject(ctx, c.u)
	if err != nil {
		return nil, err
	}
	for _, name := range a.effects {
		if i := slices.IndexFunc(builtin.Effects, func(e builtin.Effect) bool { return e.Name == name }); i >= 0 &&
			builtin.Effects[i].Provider != "" && builtin.Effects[i].Provider != sub.unit.Provider {
			return []draftCandidate{{refusal: fmt.Sprintf("%s applies to %s drafts only", name, builtin.Effects[i].Provider)}}, nil
		}
	}
	tmpl, err := draftTemplate(deps.Config(), w.Name, a.text, a.delivery)
	if err != nil {
		return nil, err
	}
	now := deps.Now()
	l := s.e.loadedIntents()
	policy := draftApproval(deps.Config(), w, briefExtrasOf(l), intentload.DefaultDraftApproval(l.Gates))
	var subject *draft.Subject
	if w.Withdraw != nil || len(policy.Rules) > 0 {
		read, err := subjectFacts(ctx, s.d.Units, s.d.Keep, c.u, w.Name)
		if err != nil {
			return nil, err
		}
		read.Slot = w.Slot
		subject = &read
	}
	var facts workflow.Facts
	if tmpl != nil {
		switch facts, err = s.facts(ctx, c.u); {
		case errors.Is(err, automation.ErrUnitGone):
			facts = workflow.Facts{}
		case err != nil:
			return nil, err
		}
	}
	var out []draftCandidate
	for _, person := range sub.people(a.per) {
		its := selectItems(sub, a.items, person, now, deps.Codecs)
		cd := draftCandidate{person: person.Key}
		key := sub.unit.String()
		if a.per == "person" {
			key += ":" + person.Key
		}
		cd.p = outbox.Proposal{Unit: sub.unit, Items: itemIDs(its), Purpose: a.purpose, Delivery: a.delivery, Reaction: a.reaction,
			Effects: a.effects, RepeatAfter: a.repeatAfter, Policy: &policy, Subject: subject,
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: w.Name, Rule: w.Name, Key: key}}
		if sub.task != nil {
			cd.p.Task = string(sub.task.ID)
		}
		switch {
		case len(its) == 0:
			cd.refusal = "no " + a.items + " items"
		case a.target == "item":
			if cd.p.Target = its[0].Origin.Permalink; cd.p.Target == "" && s.e.pluginTakes(its[0].Origin.Ref) {
				cd.p.Target = its[0].Origin.Ref
			}
			if cd.p.Target == "" && slices.Contains(builtin.DraftEmojiDeliveries, string(a.delivery)) {
				cd.refusal = "its item has no permalink to react to"
			}
		}
		if cd.refusal == "" {
			cd.refusal, cd.timed, err = holdOf(ctx, ob, deps.Tasks, cd.p, person.Key, a, now)
			if err != nil {
				return nil, err
			}
		}
		if cd.refusal == "" && w.Withdraw != nil {
			cd.refusal, err = withdrawnAtOnce(w, subject.Facts, draftFactsOf(sub, person.Key, cd.p.Items), deps.Config, now)
			if err != nil {
				return nil, err
			}
		}
		if cd.refusal == "" {
			if cd.p.Text, err = renderDraft(tmpl, draftTextData{Workflow: w.Name, Purpose: a.purpose, Facts: facts}, sub, person, its); err != nil {
				return nil, err
			}
		}
		out = append(out, cd)
	}
	return out, nil
}

// subject reads what a step's subject u holds: for a task, the unit its
// first item is on, else its first unit, with that unit's items and the
// task; else u's items.
func (d *draftDeps) subject(ctx context.Context, u unit.Unit) (draftSubject, error) {
	sub := draftSubject{subject: u, unit: u}
	if id, ok := task.IDOf(u); ok {
		e, err := d.Tasks.Task(ctx, id)
		if err != nil {
			return sub, err
		}
		sub.task = &e
		switch {
		case len(e.Items) > 0:
			sub.unit = e.Items[0].Unit
		case len(e.Units) > 0:
			sub.unit = e.Units[0]
		default:
			return sub, fmt.Errorf("task %s has no unit to post on", e.ID)
		}
	}
	its, err := d.Items.Items(ctx, sub.unit)
	if err != nil {
		return sub, err
	}
	sub.items = slices.Clone(its)
	slices.SortStableFunc(sub.items, func(a, b items.Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return sub, nil
}

// people are the persons a per-person step proposes to: those the open
// items of the subject wait on, a task's those it holds; one empty person
// for a per-subject step.
func (s draftSubject) people(per string) []items.Person {
	if per != "person" {
		return []items.Person{{}}
	}
	var out []items.Person
	its := s.items
	if s.task != nil {
		its = s.held()
	}
	for _, it := range its {
		for _, p := range it.WaitingOn {
			if it.State == items.Open && !slices.ContainsFunc(out, func(q items.Person) bool { return q.Key == p.Key }) {
				out = append(out, p)
			}
		}
	}
	return out
}

// owedPool are the open items a draft to person may list as owed: those
// the subject holds, and of a task's unit those waiting on person that
// other tasks hold, so one nudge covers what person owes there.
func (s draftSubject) owedPool(person items.Person) []items.Item {
	out := s.held()
	if s.task == nil || person.Key == "" {
		return out
	}
	for _, it := range s.items {
		if it.State == items.Open && it.Unit == s.unit && !slices.ContainsFunc(out, func(h items.Item) bool { return h.ID == it.ID }) &&
			slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool { return w.Key == person.Key }) {
			out = append(out, it)
		}
	}
	return out
}

// held are the open items of the subject: those its task holds, or every
// open item of its unit.
func (s draftSubject) held() []items.Item {
	var out []items.Item
	for _, it := range s.items {
		if it.State == items.Open && it.Unit == s.unit && (s.task == nil || slices.Contains(s.task.Items, task.ItemRef{Unit: it.Unit, Item: it.ID})) {
			out = append(out, it)
		}
	}
	return out
}

func selectItems(s draftSubject, which string, person items.Person, now time.Time, codecs unit.KeyComparer) []items.Item {
	var out []items.Item
	switch which {
	case "owed":
		return owedBy(person, s.owedPool(person), codecs)
	case "resolved":
		for _, it := range s.items {
			if it.State == items.Resolved && it.Unit == s.unit {
				out = append(out, it)
			}
		}
	case "unowned", "unowned_aged":
		for _, it := range s.items {
			if !unownedItem(it, s.unit) {
				continue
			}
			if which == "unowned_aged" && (it.CreatedAt.IsZero() || task.AddWorkdays(it.CreatedAt, 1, now.Location()).After(now)) {
				continue
			}
			out = append(out, it)
		}
	default:
		for _, it := range s.items {
			if it.Unit == s.unit {
				out = append(out, it)
			}
		}
	}
	return out
}

func unownedItem(it items.Item, u unit.Unit) bool {
	return it.Unit == u && it.State == items.Open && len(it.Assignees)+len(it.WaitingOn) == 0 && cmp.Or(it.Basis, items.NoBasis) == items.NoBasis
}

// owedBy are the items of its waiting on p, keys compared through codecs;
// all of them when none names p, a waiting the user set by hand.
func owedBy(p items.Person, its []items.Item, codecs unit.KeyComparer) []items.Item {
	id := items.Identity{Keys: append([]string{p.Key}, p.Aliases...), Codecs: codecs}
	var out []items.Item
	for _, it := range its {
		if slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool { return w.Is(id) }) {
			out = append(out, it)
		}
	}
	if len(out) == 0 {
		return its
	}
	return out
}

func itemIDs(its []items.Item) []string {
	out := make([]string, len(its))
	for i, it := range its {
		out[i] = it.ID
	}
	return out
}

// draftApproval is the approval policy of the drafts w's steps propose:
// workflowApproval's over no intent, else def, gates.toml's default for a
// draft gate.
func draftApproval(cfg config.Config, w workflow.Workflow, extras func(unit.ProviderID) expr.Env, def draft.Approval) draft.Policy {
	if p := workflowApproval(cfg, w, nil, extras, def); p != nil {
		return *p
	}
	return draft.Policy{Default: def}
}

// workflowApproval is the approval policy of w's drafts and of the turns it
// starts of an intent whose block is intent: the config's
// workflows.<name>.gates.approval laid over the manifest's, else over
// intent, else over def, field by field; nil when none is set.
func workflowApproval(cfg config.Config, w workflow.Workflow, intent *draft.Policy, extras func(unit.ProviderID) expr.Env, def draft.Approval) *draft.Policy {
	base := cmp.Or(w.DraftApproval, intent)
	if p, _ := configPolicy(w, base, cfg.Workflows[w.Name].GateApproval(), extras, def); p != nil {
		return p
	}
	return base
}

// configPolicy is a, the config's gates approval of w, laid over base (nil:
// def) for its draft gates, its rules compiled over the unit slot's facts
// and those extras gives for w's provider (nil gives none); nil when a is.
// One that does not load is refused, with the error, and holds every draft.
func configPolicy(w workflow.Workflow, base *draft.Policy, a *config.ApprovalConfig, extras func(unit.ProviderID) expr.Env, def draft.Approval) (*draft.Policy, error) {
	if a == nil {
		return nil, nil
	}
	p, err := intentload.OverlayDraftApproval(base, def, overlay(*a), workflowEnvs(w, extras))
	if err != nil {
		return draft.Fixed(draft.Approval{Mode: draft.Wait}), fmt.Errorf("workflows.%s.gates.approval: %w", w.Name, err)
	}
	return &p, nil
}

// holdOf says why a draft of the same workflow, unit and person holds p, its person read off the draft's key
// in either shape draftPerson reads, or, with no person, of the same key:
// one open or failed, one sent within repeat_after_sent (or ever, with
// forever), one otherwise settled within hold; timed when only the time
// since a settled draft holds it. A withdrawn draft holds nothing, but one withdrawn as its task ended: while that
// task stays ended it holds a proposal of no task or of that task, and once reopened it holds as a settled one.
func holdOf(ctx context.Context, ob draftOutbox, tasks draftTasks, p outbox.Proposal, person string, a draftArgs, now time.Time) (string, bool, error) {
	prior, err := ob.List(ctx, outbox.Filter{Unit: &p.Unit, Rule: p.Origin.Rule})
	if err != nil {
		return "", false, err
	}
	refusal, timed := "", false
	for _, d := range prior {
		if d.Origin.Kind != draft.OriginAuto || d.Origin.Workflow != p.Origin.Workflow {
			continue
		}
		if person == "" && d.Origin.Key != p.Origin.Key || person != "" && draftPerson(d.Origin.Key, d.Unit, draftSubjectUnit(d)) != person {
			continue
		}
		held := fmt.Sprintf("held by draft %d (%s)", d.ID, d.State)
		if d.State == draft.Dropped && d.Reason == draft.WithdrawEnded && (p.Task == "" || p.Task == d.Task) {
			if ended, err := taskEnded(ctx, tasks, task.ID(d.Task)); err != nil || ended {
				return held, false, err
			}
		}
		switch {
		case slices.Contains(draft.Open, d.State), d.State == draft.Failed, d.State == draft.Sent && a.forever:
			return held, false, nil
		case d.State == draft.Dropped && (d.Reason == draft.WithdrawAssigned || d.Reason == draft.WithdrawMoot):
		case d.State == draft.Sent && now.Sub(d.UpdatedAt) < a.repeatAfterSent, d.State != draft.Sent && now.Sub(d.UpdatedAt) < a.repeatAfter:
			if refusal == "" {
				refusal, timed = held, true
			}
		}
	}
	return refusal, timed, nil
}

// taskEnded says whether task id is done or dropped; a task no longer stored has ended too.
func taskEnded(ctx context.Context, tasks draftTasks, id task.ID) (bool, error) {
	if id == "" || tasks == nil {
		return false, nil
	}
	return coretasks.Ended(tasks.Task(ctx, id))
}

// withdrawnAtOnce refuses a draft the workflow's [withdraw] when would
// withdraw as soon as it is proposed, so the pair never cycles.
func withdrawnAtOnce(w workflow.Workflow, subject, drafted workflow.Facts, cfg func() config.Config, now time.Time) (string, error) {
	facts := workflow.Facts{}
	for _, f := range []workflow.Facts{subject, drafted} {
		for k, v := range f {
			facts[k] = v
		}
	}
	ok, err := w.Withdraw.Match(workflow.Vars{Facts: facts, Config: configFacts(cfg()), Now: now})
	if err != nil || !ok {
		return "", err
	}
	return "its withdraw when holds", nil
}

// draftFactsOf are the draft slot's facts of a draft on sub for person
// over the items ids.
func draftFactsOf(sub draftSubject, person string, ids []string) workflow.Facts {
	var mine []items.Item
	for _, it := range sub.items {
		if slices.Contains(ids, it.ID) {
			mine = append(mine, it)
		}
	}
	waited := person == ""
	if !waited && sub.task != nil {
		waited = slices.ContainsFunc(sub.owedPool(items.Person{Key: person}), func(it items.Item) bool {
			return slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return p.Key == person })
		})
	}
	held, owned := 0, 0
	heldNow := sub.owedPool(items.Person{Key: person})
	for _, it := range mine {
		if len(it.Assignees)+len(it.WaitingOn) > 0 || it.State != items.Open {
			owned++
		}
		switch {
		case person == "" && unownedItem(it, sub.unit):
			held++
		case person != "" && slices.ContainsFunc(heldNow, func(h items.Item) bool { return h.ID == it.ID }):
			held++
		}
		if !waited && sub.task == nil && it.State == items.Open &&
			slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return p.Key == person }) {
			waited = true
		}
	}
	return workflow.Facts{
		"draft.person_waited":  waited,
		"draft.items_owed":     held,
		"draft.items_assigned": owned,
		"draft.unit_open":      sub.task == nil || sub.task.Open(),
	}
}

// draftTextData is what a draft's template reads: item text is data, never
// template code. Facts are the step's subject's facts by name, none once
// the subject is gone.
type draftTextData struct {
	Workflow, Purpose, Unit, Task string
	Person                        draftTextPerson
	Items                         []draftTextItem
	Facts                         workflow.Facts
}

type draftTextPerson struct{ Key, Name string }

type draftTextItem struct {
	ID, Summary, Line, Resolution, State, Permalink string
}

// draftTemplate is the workflow's text template through the profile layers;
// nil for an emoji delivery that names no template of its own, so a
// reaction carries no text even where the workflow's message text exists.
func draftTemplate(cfg config.Config, workflowName, text string, d draft.Delivery) (*template.Template, error) {
	if slices.Contains(builtin.DraftEmojiDeliveries, string(d)) && text == defaultDraftText {
		return nil, nil
	}
	rel := path.Join("workflows", workflowName, text)
	r, err := profile.Resolve(profile.Chain(cfg), rel)
	if err != nil {
		return nil, fmt.Errorf("draft.propose: %w", err)
	}
	t, err := template.New(rel).Option("missingkey=error").Parse(string(r.Body))
	if err != nil {
		return nil, fmt.Errorf("draft.propose: %s: %w", r.Path, err)
	}
	return t, nil
}

// renderDraft renders t over data, filled with sub's unit and task, person and its items.
func renderDraft(t *template.Template, data draftTextData, sub draftSubject, person items.Person, its []items.Item) (string, error) {
	if t == nil {
		return "", nil
	}
	data.Unit, data.Person = sub.unit.String(), draftTextPerson{Key: person.Key, Name: person.Name}
	if sub.task != nil {
		data.Task = string(sub.task.ID)
	}
	for _, it := range its {
		data.Items = append(data.Items, draftTextItem{ID: it.ID, Summary: it.Summary, Line: strings.Join(strings.Fields(it.Summary), " "),
			Resolution: string(it.Resolution), State: string(it.State), Permalink: it.Origin.Permalink})
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("draft.propose: rendering %s: %w", t.Name(), err)
	}
	return b.String(), nil
}
