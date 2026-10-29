package factsgithub

import (
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workday"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/plugins/source/value"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// facts are ref's pr facts at now: those the plugin reported, its
// planner's among them, under the host's own.
func (v *view) facts(ref pr.Ref, now time.Time) workflow.Facts {
	f := workflow.Facts{"pr.repo": ref.Owner + "/" + ref.Repo, "pr.repo_name": ref.Repo, "pr.owner": ref.Owner, "pr.author": "", "pr.visibility": "", "pr.ended": false,
		"pr.slug": ref.String(), "pr.association": "", "pr.labels": []string{}, "pr.last_review.exists": false, "pr.last_review.verdict": "",
		"pr.last_review.decision": "", "pr.nudge.who": []string{}, "pr.nudge.why": "", "pr.has_workspace": false,
		"pr.review.action": "", "pr.review.reason": "", "pr.review.observed": false, "pr.first.due": false, "pr.first.reason": "",
		"pr.address.due": false, "pr.address.reason": "", "pr.address.observed": false, "pr.address.reviewers": []string{},
		"pr.address.checks": []string{}, "pr.address.conflict": false, "pr.fingerprint": ""}
	rep := v.reportOf(ref)
	if rep != nil {
		for _, x := range rep.Facts {
			f[x.Name] = value.Fact(x.Value)
		}
	}
	row, ok := v.byRef[ref]
	if ok {
		o := v.overrides[ref]
		f["pr.finished_for"] = finishedFor(row, now)
		f["pr.held"], f["pr.adopted"] = o.Hold, o.Adopted
		f["pr.muted"] = o.MuteUntilStateChange || o.MutedUntil.After(now)
		f["pr.mute.refusal"] = o.MuteRefusal(now)
		if rep != nil {
			sit, nudge := v.situation(*rep, row, v.eventsOf(ref), o, now)
			f["pr.nudge.who"], f["pr.nudge.why"] = nonNil(nudge.who), nudge.why
			f["pr.stalled"] = sit.Stalled
			f["pr.waits_on_user"] = sit.WaitingOn == "me" || (sit.WaitingOn == "author" && strings.EqualFold(row.Author, v.viewer) && v.viewer != "")
			f["pr.nudge.refusal"] = nudge.refusal
		}
	}
	b := v.books[ref.Unit()]
	f["pr.open_turn"], f["pr.open_turn_seq"] = "", 0
	if t, ok := openTurn(b); ok {
		f["pr.open_turn"], f["pr.open_turn_seq"] = string(t.State), int(t.Seq)
	}
	f["pr.orphan_answer_id"] = orphanAnswer(b)
	return f
}

// runs says whether a round the facts f plan would run without the user:
// due and not only observed.
func runs(f workflow.Facts) bool {
	is := func(name string) bool { b, _ := f[name].(bool); return b }
	action, _ := f["pr.review.action"].(string)
	return (action == "follow-up" && !is("pr.review.observed")) || is("pr.first.due") || (is("pr.address.due") && !is("pr.address.observed"))
}

// finishedFor is the working time since the pull request ended, its newer
// of merged and closed; zero while open.
func finishedFor(row dinv.PR, now time.Time) time.Duration {
	if row.State == dgh.StateOpen {
		return 0
	}
	ended := row.MergedAt
	if row.ClosedAt.After(ended) {
		ended = row.ClosedAt
	}
	if ended.IsZero() {
		return 0
	}
	return workday.Between(ended, now)
}

// nudgeOf is whom a nudge on a pull request would address, why, and what
// nudge.prepare would refuse it with.
type nudgeOf struct {
	who          []string
	why, refusal string
}

func (v *view) situation(rep prrow.Report, row dinv.PR, evs []dinv.Event, o dinv.Override, now time.Time) (overlay.Situation, nudgeOf) {
	var tf *prrow.TurnFact
	var unstarted map[string]bool
	if b, ok := v.books[row.Ref.Unit()]; ok {
		if f, ok := prrow.TurnFacts(map[unit.Unit]turn.Book{row.Ref.Unit(): b}, v.registry)[row.Ref]; ok {
			tf = &f
		}
		unstarted = prrow.Unstarted(b)
	}
	res, unmuted := prrow.SituationOf(rep, tf, o, evs, now)
	who, why := prrow.NudgeTargets(apiRow(unmuted), row.Author, v.viewer, prrow.IgnoreOf(v.cfg))
	return res, nudgeOf{who: who, why: why, refusal: prrow.NudgeRefusal(row.Ref, who, why, unmuted.Since, evs, unstarted, now)}
}

// apiRow is the row of situation s, as nudge.targets reads it.
func apiRow(s overlay.Situation) api.Row {
	return api.Row{Kind: api.Kind(s.Kind), Summary: api.Summary{WaitsOn: s.WaitingOn, Reason: s.Reason, Since: s.Since}}
}

// orphanAnswer is the id of an answer still waiting whose turn ended; 0 when
// none.
func orphanAnswer(b turn.Book) int {
	for _, a := range b.Answers {
		if a.Route != turn.Queued && a.Route != turn.Pending {
			continue
		}
		if i := slices.IndexFunc(b.Turns, func(t turn.Turn) bool { return t.Seq == a.TurnSeq }); i >= 0 && !b.Turns[i].State.Open() {
			return int(a.ID)
		}
	}
	return 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
