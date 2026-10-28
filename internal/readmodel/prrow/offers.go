package prrow

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/situation"
)

// RenudgeAfter is how long a wait that a nudge did not move stays nudged.
const RenudgeAfter = 24 * time.Hour

// NudgeMaintainers is whom a nudge asks on the user's own pull request with
// no reviewer: the skill mentions nobody, or the one code owner.
const NudgeMaintainers = "the maintainers (no reviewer requested)"

// intakeAction is what an open intake's payload says of the action it asks.
type intakeAction struct {
	Action string         `json:"action"`
	Inputs map[string]any `json:"inputs,omitempty"`
}

// openIntakes are the open intakes of ref, the oldest first.
func (d Deriver) openIntakes(ref github.Ref) []catalog.Intake {
	var out []catalog.Intake
	for _, i := range d.Intakes[ref] {
		var p intakeAction
		_ = json.Unmarshal(i.Request, &p)
		out = append(out, catalog.Intake{Intake: api.Intake{Seq: i.Seq, Intent: string(i.Intent), Source: i.Source, State: string(i.State)},
			Action: p.Action, Inputs: p.Inputs, FromSession: i.Source == intake.SourceSession})
	}
	return out
}

// FactsOf gathers the facts of a row of the inventory; unmuted is the
// situation without the user's mute, which a nudge reads.
func (d Deriver) FactsOf(pr dinv.PR, evs []dinv.Event, o dinv.Override, r api.Row, p Payload, unmuted api.Row) catalog.Facts {
	f := catalog.Facts{
		PR: pr.Ref.Unit(), Owner: pr.Ref.Owner, Repo: pr.Ref.Repo, Row: r, Author: p.Author, Workspace: p.Workspace, Override: p.Override, Tracked: true, Open: situation.Kind(r.Kind) != situation.KindDone, HeldByCap: situation.Kind(r.Kind) == situation.KindHeldByCap,
		Mine:        d.Viewer != "" && strings.EqualFold(pr.Author, d.Viewer),
		Muted:       o.MuteUntilStateChange || o.MutedUntil.After(d.Now),
		MutedStored: o.MuteUntilStateChange || !o.MutedUntil.IsZero(),
		Draft:       pr.IsDraft, CrossRepository: pr.IsFork, State: pr.State, Base: pr.BaseBranch,
		ReviewRequestedMe: slices.ContainsFunc(pr.ReviewRequests, func(who string) bool { return strings.EqualFold(who, d.Viewer) }),
		GateOn:            d.GateOn, BriefOn: d.BriefOn,
		Config: catalogConfig(d.Cfg.For(pr.Ref.Owner, pr.Ref.Repo)),
		Asked:  d.Asked, Intents: d.Loaded,
	}
	if t, ok := d.Turns[pr.Ref]; ok {
		f.Question = t.Question != nil
	}
	if d.Forceable != nil {
		f.Forceable = d.Forceable(pr.Ref.Unit())
	}
	if ev, ok := dinv.ActiveBlock(evs, d.Viewer); ok && situation.Kind(r.Kind) == situation.KindBlocked {
		b, keyed := inventory.StartBlockOf(port.Event{Unit: pr.Ref.Unit(), Kind: ev.Kind, Key: ev.Key, Data: ev.Data, At: ev.At})
		f.StartBlocked = keyed
		if b.Question == turns.QuestionTrust {
			f.TrustFolder, f.TrustHomes = cmp.Or(b.Root, b.Folder), b.Homes
		}
	}
	f.Intakes = d.openIntakes(pr.Ref)
	f.NudgeWho, f.NudgeWhy = NudgeTargets(unmuted, pr.Author, d.Viewer, d.Ignore)
	f.NudgeRefusal = NudgeRefusal(pr.Ref, f.NudgeWho, f.NudgeWhy, unmuted.Summary.Since, evs, d.Turns[pr.Ref].Unstarted, d.Now)
	return f
}

func catalogConfig(c config.Config) catalog.Config {
	return catalog.Config{AddressReviews: c.GitHub.AddressReviews, AddressChecks: c.GitHub.AddressChecks,
		FollowupsWithoutWorkspace: c.GitHub.FollowupsWithoutWorkspace, AddressWithoutWorkspace: c.GitHub.AddressWithoutWorkspace}
}

// UntrackedRow is the row of a pull request the inventory lacks, from a
// GitHub read: open from its state, mine from its author, nothing else.
func (d Deriver) UntrackedRow(c dgh.Candidate, o dinv.Override) api.Row {
	r := api.Row{Unit: c.Ref.Unit(), Link: cmp.Or(c.URL, c.Ref.URL()), Label: c.Ref.String(), Title: c.Title, Views: []string{}}
	p := Payload{Author: c.Author, Repo: c.Ref.Owner + "/" + c.Ref.Repo, Head: c.HeadBranch, Reasons: []string{},
		Workspace: api.Workspace{AgentStates: []string{}}, Override: Override(o), Viewer: d.Viewer}
	open := c.State == dgh.StateOpen
	if !open {
		r.Kind, r.Summary.WaitsOn, r.Summary.Reason = api.Kind(situation.KindDone), "nobody", strings.ToLower(c.State)
	}
	f := catalog.Facts{PR: c.Ref.Unit(), Owner: c.Ref.Owner, Repo: c.Ref.Repo, Row: r, Author: p.Author, Workspace: p.Workspace, Override: p.Override, Open: open, Mine: d.Viewer != "" && strings.EqualFold(c.Author, d.Viewer),
		Draft: c.IsDraft, State: c.State, Base: c.BaseBranch, GateOn: d.GateOn, BriefOn: d.BriefOn,
		ReviewRequestedMe: slices.ContainsFunc(c.ReviewRequests, func(who string) bool { return strings.EqualFold(who, d.Viewer) }),
		Config:            catalogConfig(d.Cfg.For(c.Ref.Owner, c.Ref.Repo)),
		Asked:             d.Asked, Intents: d.Loaded,
	}
	if t, ok := d.Turns[c.Ref]; ok {
		f.Question = t.Question != nil
	}
	f.Intakes = d.openIntakes(c.Ref)
	f.NudgeRefusal = fmt.Sprintf("%s is not tracked; track it before a nudge", c.Ref)
	r.Actions = catalog.States(f)
	r.SpecsVersion = d.SpecsVersion
	author := []string{c.Author}
	if f.Mine {
		author = []string{"me", c.Author}
	}
	r.Facets = map[string][]string{FacetRepo: {p.Repo}, FacetAuthor: author, FacetStatus: {"none"}, FacetCI: {"none"}}
	return d.Seal(r, p)
}

// NudgeRefusal is the text a nudge is refused with now, "" when it would
// go; unstarted are the turns that ended before their agent started them,
// whose nudged events do not count.
func NudgeRefusal(ref github.Ref, who []string, why string, since time.Time, evs []dinv.Event, unstarted map[string]bool, now time.Time) string {
	if len(who) == 0 {
		return fmt.Sprintf("%s %s; a nudge asks someone else for the next step", ref, why)
	}
	for _, e := range slices.Backward(evs) {
		if again := e.At.Add(RenudgeAfter); e.Kind == dinv.KindNudged && !e.At.Before(since) && now.Before(again) && !unstarted[e.Key] {
			const stamp = "2006-01-02 15:04"
			return fmt.Sprintf("%s: already nudged %s at %s; again after %s", ref, e.Data, e.At.Local().Format(stamp), again.Local().Format(stamp))
		}
	}
	return ""
}

// NudgeTargets is who a nudge mentions, or why it mentions nobody: r's
// kind, waits-on and reason, author its pull request's author.
func NudgeTargets(r api.Row, author, viewer string, ignore []string) ([]string, string) {
	switch situation.Kind(r.Kind) {
	case situation.KindDone:
		return nil, "is " + r.Summary.Reason
	case situation.KindLoopWorking, situation.KindLoopWaiting, situation.KindLoopStopped, situation.KindHeldByCap:
		return nil, "waits on the review loop (" + r.Summary.Reason + ")"
	case situation.KindNoReviewer:
		if viewer != "" && strings.EqualFold(author, viewer) {
			return []string{NudgeMaintainers}, ""
		}
	}
	var people, teams, skipped []string
	ghost := false
	for who := range strings.SplitSeq(r.Summary.WaitsOn, ", ") {
		if who == "author" {
			if author == "" {
				ghost = true
				continue
			}
			who = author
		}
		switch {
		case who == "" || who == "me" || who == "nobody" || strings.EqualFold(who, viewer):
			continue
		case strings.HasSuffix(who, "[bot]"):
			skipped = append(skipped, who+", a bot")
			continue
		case slices.ContainsFunc(ignore, func(s string) bool { return strings.EqualFold(s, who) }):
			skipped = append(skipped, who+", which automated_reviewers or address_skip_reviewers lists")
			continue
		}
		if team, ok := strings.CutPrefix(who, "team:"); ok {
			teams = append(teams, "@"+team)
			continue
		}
		people = append(people, "@"+who)
	}
	// A team is mentioned only when no person is requested: mentioning it
	// notifies every member.
	switch {
	case len(people) > 0:
		return people, ""
	case len(teams) > 0:
		return teams, ""
	case len(skipped) > 0:
		return nil, "waits on " + strings.Join(skipped, " and ")
	case ghost:
		return nil, "waits on an author GitHub no longer knows"
	}
	return nil, "waits on you (" + r.Summary.Reason + ")"
}

func (d Deriver) specs() []api.ActionSpec {
	if d.Specs != nil {
		return d.Specs
	}
	return catalog.Specs(d.Loaded)
}

// notificationActions are the row's enabled actions a notification
// offers, the answer's one-tap choices and reply first; one that asks for a
// confirmation is left out, since a notification cannot ask.
func (d Deriver) notificationActions(r api.Row) []api.AttentionAction {
	specs := d.specs()
	var choices, rest []api.AttentionAction
	for _, st := range r.Actions {
		i := slices.IndexFunc(specs, func(s api.ActionSpec) bool { return s.ID == st.ID })
		if !st.Enabled || i < 0 || !slices.Contains(specs[i].Surfaces, catalog.SurfaceNotification) {
			continue
		}
		spec := specs[i]
		if st.ID == "answer" {
			if r.Question != nil {
				choices = append(choices, answerChoices(r.Question)...)
			}
			choices = append(choices, actionReply)
			continue
		}
		if st.Confirm != "" {
			continue
		}
		rest = append(rest, api.AttentionAction{ID: st.ID, Title: cmp.Or(st.Label, spec.Label), TextInput: spec.Input != nil && spec.Input.Kind == "text", Basis: st.Basis})
	}
	return append(append([]api.AttentionAction{}, choices...), rest...)
}
