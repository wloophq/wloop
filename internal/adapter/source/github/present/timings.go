package present

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// quantiles are the nearest-rank p50 and p90 of ds.
func quantiles(ds []time.Duration) api.Quantiles {
	q := api.Quantiles{Count: len(ds)}
	if len(ds) == 0 {
		return q
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	rank := func(p float64) int64 {
		n := int(p*float64(len(s)) + 0.999999999)
		return s[min(max(n, 1), len(s))-1].Milliseconds()
	}
	q.P50MS, q.P90MS = rank(0.5), rank(0.9)
	return q
}

// me is the viewer as a timings report knows them: the login and the
// teams, as <org>/<slug>.
type me struct {
	login string
	teams []string
}

func (m me) known() bool { return m.login != "" }

func (m me) is(login string) bool { return m.known() && strings.EqualFold(login, m.login) }

// asked tells a review request, by its reviewer, made of me or of a team of
// mine.
func (m me) asked(reviewer string) bool {
	if m.is(reviewer) {
		return true
	}
	team, ok := strings.CutPrefix(reviewer, "team:")
	return ok && slices.ContainsFunc(m.teams, func(t string) bool { return strings.EqualFold(t, team) })
}

// milestones are a pull request's first times, from its events; the first
// request is the first one made of me.
func milestones(p dinv.PR, evs []dinv.Event, m me) api.PRTimes {
	t := api.PRTimes{PR: p.Ref.URL(), Title: p.Title, Author: p.Author, Opened: p.CreatedAt, Merged: p.MergedAt}
	first := func(dst *time.Time, at time.Time) {
		if dst.IsZero() || at.Before(*dst) {
			*dst = at
		}
	}
	for _, e := range evs {
		switch e.Kind {
		case dinv.KindOpened:
			first(&t.Opened, e.At)
		case dinv.KindFollowed:
			first(&t.Followed, e.At)
		case dinv.KindReviewRequest:
			if m.asked(e.Data) {
				first(&t.FirstRequest, e.At)
			}
		case dinv.KindReview:
			if m.is(e.Actor) {
				first(&t.FirstReview, e.At)
			}
			if strings.HasPrefix(e.Data, "APPROVED") {
				first(&t.Approved, e.At)
			}
		case dinv.KindMerged:
			first(&t.Merged, e.At)
		}
	}
	return t
}

// inLoop is how long a pull request was followed since recording began:
// the first followed event after it to the unfollowed after that.
func inLoop(evs []dinv.Event, recorded time.Time) (start, end time.Time) {
	for _, e := range evs {
		switch {
		case e.Kind == dinv.KindFollowed && start.IsZero() && e.At.After(recorded):
			start = e.At
		case e.Kind == dinv.KindUnfollowed && !start.IsZero() && end.IsZero() && e.At.After(start):
			end = e.At
		}
	}
	return start, end
}

// counter tells a turn the report counts: none withdrawn, replaced, on the
// probe's owner, or of an intent whose manifest says it reports nothing.
func counter(reports func(intent string) bool) func(t dinv.TimedTurn) bool {
	return func(t dinv.TimedTurn) bool {
		return t.State != string(turn.Withdrawn) && t.State != string(turn.Replaced) && t.PR.Owner != api.ProbeOwner && (reports == nil || reports(t.Intent))
	}
}

// reportsOf tells the intents whose turns the report counts; one the
// registry no longer knows still counts.
func reportsOf(reg *dintent.Registry) func(intent string) bool {
	return func(intent string) bool {
		d, ok := reg.Get(dintent.Name(intent))
		return !ok || d.Reports
	}
}

// recordedRun tells a brief run made since recording began.
func recordedRun(r dinv.BriefRun, recorded time.Time) bool {
	return !r.StartedAt.Before(recorded)
}

// briefWaits are, per automated first turn, the wait from its pull
// request's brief_wanted to the turn's send.
func briefWaits(d dinv.TimingsData, isCounted func(dinv.TimedTurn) bool) []time.Duration {
	var out []time.Duration
	for _, p := range d.PRs {
		var wanted time.Time
		for _, e := range d.Events[p.Ref] {
			if e.Kind == dinv.KindBriefWanted && (wanted.IsZero() || e.At.Before(wanted)) {
				wanted = e.At
			}
		}
		if wanted.IsZero() {
			continue
		}
		for _, t := range d.Turns {
			if t.PR == p.Ref && isCounted(t) && t.Trigger == string(turn.Automated) && !t.SentAt.Before(wanted) {
				out = append(out, t.SentAt.Sub(wanted))
				break
			}
		}
	}
	return out
}

// timings derives the report of the window since since, up to now, from
// its record.
func timings(d dinv.TimingsData, since, now time.Time, m me, reports func(intent string) bool) api.TimingsReport {
	counted := counter(reports)
	rep := api.TimingsReport{Since: since, RecordedSince: d.RecordedSince, Viewer: m.login, Milestones: map[string]api.Quantiles{}, Turns: map[string]api.Quantiles{},
		Tokens: map[string]api.TokenCounts{}, PRs: []api.PRTimes{}, TurnList: []api.TurnTimes{}}
	in := func(t time.Time) bool { return !t.IsZero() && !t.Before(since) }
	spans := map[string][]time.Duration{}
	span := func(key string, from, to time.Time) {
		if in(from) && in(to) && !to.Before(from) {
			spans[key] = append(spans[key], to.Sub(from))
		}
	}
	turnsOf, runsOf := map[pr.Ref]int{}, map[pr.Ref]int{}
	for _, t := range d.Turns {
		if counted(t) {
			turnsOf[t.PR]++
		}
	}
	for _, r := range d.Runs {
		if recordedRun(r, d.RecordedSince) {
			runsOf[r.PR]++
		}
	}
	merged := map[pr.Ref]time.Time{}
	for _, p := range d.PRs {
		if p.Ref.Owner == api.ProbeOwner {
			continue
		}
		evs := d.Events[p.Ref]
		t := milestones(p, evs, m)
		if in(t.Merged) {
			merged[p.Ref] = t.Merged
		}
		mine := m.is(p.Author)
		span(api.MilestoneLeadTime, firstPush(evs), t.Merged)
		if !mine && !t.FirstReview.IsZero() && !t.FirstRequest.IsZero() {
			span(api.MilestoneReviewWait, t.FirstRequest, t.FirstReview)
		}
		span(api.MilestoneToApproval, t.Opened, t.Approved)
		span(api.MilestoneToMerge, t.Opened, t.Merged)
		if mine {
			span(api.MilestoneMineToMerge, t.Opened, t.Merged)
			span(api.MilestoneMineLead, firstPush(evs), t.Merged)
		}
		if start, end := inLoop(evs, d.RecordedSince); end.After(start) {
			span(api.MilestoneInLoop, start, end)
		}
		t.Turns, t.Briefs = turnsOf[p.Ref], runsOf[p.Ref]
		if t.Turns > 0 || t.Briefs > 0 || in(t.Merged) || in(t.FirstReview) || in(t.Approved) {
			rep.PRs = append(rep.PRs, t)
		}
	}
	for key, ds := range spans {
		rep.Milestones[key] = quantiles(ds)
	}
	rep.Waiting = waiting(d, since, now, m)
	rep.ChangeFailure = changeFailure(d, merged, now)
	slices.SortFunc(rep.PRs, func(a, b api.PRTimes) int { return cmp.Compare(a.PR, b.PR) })

	output := map[int64]int64{}
	for _, u := range d.Tokens {
		output[u.TurnSeq] += u.Output
	}
	turnSpans := map[string][]time.Duration{}
	agentOf := map[int64]string{}
	for _, t := range d.Turns {
		if !counted(t) {
			continue
		}
		agentOf[t.Seq] = t.Agent
		tt := api.TurnTimes{PR: t.PR.URL(), Seq: t.Seq, Intent: t.Intent, Trigger: t.Trigger, State: t.State, Agent: t.Agent,
			Sent: t.SentAt, Started: t.StartedAt, Ended: t.EndedAt, OutputTokens: output[t.Seq]}
		if !t.StartedAt.IsZero() {
			turnSpans[api.TurnStartWait] = append(turnSpans[api.TurnStartWait], t.StartedAt.Sub(t.SentAt))
		}
		if from := cmp.Or(t.StartedAt, t.SentAt); !t.EndedAt.IsZero() {
			turnSpans[t.Intent] = append(turnSpans[t.Intent], t.EndedAt.Sub(from))
			tt.DurationMS = t.EndedAt.Sub(from).Milliseconds()
		}
		rep.TurnList = append(rep.TurnList, tt)
	}
	turnSpans[api.TurnBriefWait] = briefWaits(d, counted)
	for key, ds := range turnSpans {
		if len(ds) > 0 {
			rep.Turns[key] = quantiles(ds)
		}
	}
	for _, u := range d.Tokens {
		agent, ok := agentOf[u.TurnSeq]
		if !ok {
			continue
		}
		key := strings.TrimSpace(agent + " " + u.Model)
		c := rep.Tokens[key]
		c.Input, c.Output, c.CacheRead, c.CacheWrite = c.Input+u.Input, c.Output+u.Output, c.CacheRead+u.CacheRead, c.CacheWrite+u.CacheWrite
		rep.Tokens[key] = c
	}

	var waits []time.Duration
	for _, i := range d.Intakes {
		if !i.StartingAt.IsZero() {
			waits = append(waits, i.StartingAt.Sub(i.AcceptedAt))
		}
	}
	rep.Intakes = quantiles(waits)

	var calls []time.Duration
	dropped := map[string]int{}
	for _, r := range d.Runs {
		if !recordedRun(r, d.RecordedSince) {
			rep.Briefs.Unrecorded++
			continue
		}
		switch r.Outcome {
		case "ok":
			if r.CallMS != nil {
				calls = append(calls, time.Duration(*r.CallMS)*time.Millisecond)
			}
		case "reused":
			rep.Briefs.Reused++
		case "running", "interrupted":
		default:
			rep.Briefs.Failed++
		}
		if r.CostUSD != nil {
			rep.Briefs.CostUSD += *r.CostUSD
		}
		for _, t := range r.DroppedTags {
			dropped[t]++
		}
	}
	rep.Briefs.Call, rep.Briefs.Outages = quantiles(calls), d.Outages
	for _, t := range slices.Sorted(maps.Keys(dropped)) {
		rep.Dropped = append(rep.Dropped, fmt.Sprintf("%s (%d)", t, dropped[t]))
	}
	if len(d.Tags) > 0 {
		rep.Tags = map[string][]api.TagCount{}
		for _, t := range d.Tags {
			rep.Tags[t.Key] = append(rep.Tags[t.Key], api.TagCount{Value: t.Value, Count: t.Count})
		}
	}
	return rep
}

// timings is the report of the days before now.
func (p *Presenter) timings(ctx context.Context, days int) (api.TimingsReport, error) {
	now := p.h.Now()
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	d, err := p.d.Store.Timings(ctx, since)
	if err != nil {
		return api.TimingsReport{}, err
	}
	var id src.Identity
	if p.h.Viewer != nil {
		if id, err = p.h.Viewer(ctx); err != nil {
			p.h.Logf("timings: viewer: %v", err)
		}
	}
	reports := func(string) bool { return true }
	if set := p.h.Settings(); set.Loaded != nil {
		reports = reportsOf(set.Loaded.Registry)
	}
	return timings(d, since, now, me{login: readmodel.Login(unit.GitHub, id), teams: id.Teams}, reports), nil
}
