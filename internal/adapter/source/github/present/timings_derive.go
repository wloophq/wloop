package present

import (
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/situation"
)

// FailureDays is how long after a merge a fix or a revert of the same files
// counts as that change failing.
const FailureDays = 14

var failureTags = []string{"change:fix", "change:revert"}

// waitParty is whom a situation, "<kind>:<waiting-on>", waits on; "" for a
// pull request that waits on nobody, done or muted. On my own pull request
// the author is me.
func waitParty(data string, mine bool) string {
	kind, on, _ := strings.Cut(data, ":")
	who := strings.Split(on, ", ")
	switch {
	case kind == string(situation.KindDone) || kind == string(situation.KindMuted):
		return ""
	case slices.Contains(who, "me") || mine && slices.Contains(who, "author"):
		return api.WaitingOnMe
	case strings.HasPrefix(kind, "loop_") || kind == string(situation.KindRoundPending) || kind == string(situation.KindHeldByCap) || kind == string(situation.KindBlocked):
		return api.WaitingOnLoop
	case on == "" || on == "nobody":
		return ""
	}
	return api.WaitingOnOthers
}

// waitSpans are the spans a pull request waited on each party within the
// window, from each situation event to the next or to its unfollowed
// event, and the one still open at now, by party.
func waitSpans(evs []dinv.Event, since, now time.Time, mine bool) (closed map[string][]time.Duration, open map[string]time.Duration) {
	closed, open = map[string][]time.Duration{}, map[string]time.Duration{}
	party, from := "", time.Time{}
	end := func(at time.Time, stillOpen bool) {
		if party != "" && !from.IsZero() {
			start, stop := maxTime(from, since), minTime(at, now)
			switch {
			case !stop.After(start):
			case stillOpen:
				open[party] += stop.Sub(start)
			default:
				closed[party] = append(closed[party], stop.Sub(start))
			}
		}
		party, from = "", time.Time{}
	}
	for _, e := range evs {
		switch e.Kind {
		case dinv.KindSituation:
			end(e.At, false)
			party, from = waitParty(e.Data, mine), e.At
		case dinv.KindUnfollowed:
			end(e.At, false)
		}
	}
	end(now, true)
	return closed, open
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func waiting(d dinv.TimingsData, since, now time.Time, m me) map[string]api.WaitShare {
	spans := map[string][]time.Duration{}
	open := map[string]time.Duration{}
	prs := map[string]int{}
	for _, p := range d.PRs {
		closed, still := waitSpans(d.Events[p.Ref], since, now, m.is(p.Author))
		for _, party := range []string{api.WaitingOnMe, api.WaitingOnOthers, api.WaitingOnLoop} {
			ds, ok := closed[party]
			o, isOpen := still[party]
			if !ok && !isOpen {
				continue
			}
			spans[party] = append(spans[party], ds...)
			open[party] += o
			prs[party]++
		}
	}
	out := map[string]api.WaitShare{}
	for party, n := range prs {
		total := open[party]
		for _, d := range spans[party] {
			total += d
		}
		out[party] = api.WaitShare{TotalMS: total.Milliseconds(), OpenMS: open[party].Milliseconds(), PRs: n, Spans: quantiles(spans[party])}
	}
	return out
}

// firstPush is the time of a pull request's first push event.
func firstPush(evs []dinv.Event) time.Time {
	var at time.Time
	for _, e := range evs {
		if e.Kind == dinv.KindPush && (at.IsZero() || e.At.Before(at)) {
			at = e.At
		}
	}
	return at
}

func tagged(tags []string) bool {
	return slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(failureTags, t) })
}

// sharedFile tells a file most changes touch, which says nothing of one
// change fixing another.
func sharedFile(path string) bool {
	base := strings.ToLower(path[strings.LastIndex(path, "/")+1:])
	switch base {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "poetry.lock",
		"gemfile.lock", "uv.lock", "composer.lock", "pipfile.lock", "requirements.txt":
		return true
	}
	return strings.HasPrefix(base, "changelog")
}

func changedFiles(files []string) []string {
	return slices.DeleteFunc(slices.Clone(files), sharedFile)
}

// changeFailure counts, among the window's merges at least FailureDays
// old whose brief listed their changed files, those a later pull request
// of the same repository, opened within FailureDays of the merge and tagged
// change:fix or change:revert by its brief, also changed, shared files
// such as go.mod, lockfiles and changelogs aside. Only pull requests the loop
// briefed take part, as the failed change or the fix.
func changeFailure(d dinv.TimingsData, merged map[pr.Ref]time.Time, now time.Time) api.ChangeFailure {
	cf := api.ChangeFailure{Days: FailureDays,
		Definition: "among briefed pull requests merged at least 14 days ago, those whose changed files (go.mod, lockfiles and changelogs aside) a briefed pull request tagged change:fix or change:revert, opened within 14 days of the merge, also changed"}
	var fixes []dinv.PR
	for _, p := range d.PRs {
		if tagged(d.PRTags[p.Ref]) && len(changedFiles(d.PRFiles[p.Ref])) > 0 {
			fixes = append(fixes, p)
		}
	}
	settled := now.Add(-FailureDays * 24 * time.Hour)
	for _, p := range d.PRs {
		at, ok := merged[p.Ref]
		if !ok {
			continue
		}
		cf.Merged++
		if tagged(d.PRTags[p.Ref]) {
			cf.Tagged++
		}
		files := changedFiles(d.PRFiles[p.Ref])
		if len(files) == 0 {
			continue
		}
		if at.After(settled) {
			cf.Recent++
			continue
		}
		cf.Briefed++
		limit := at.Add(FailureDays * 24 * time.Hour)
		if slices.ContainsFunc(fixes, func(f dinv.PR) bool {
			return f.Ref != p.Ref && f.Ref.Owner == p.Ref.Owner && f.Ref.Repo == p.Ref.Repo &&
				!f.CreatedAt.Before(at) && !f.CreatedAt.After(limit) &&
				slices.ContainsFunc(changedFiles(d.PRFiles[f.Ref]), func(file string) bool { return slices.Contains(files, file) })
		}) {
			cf.Failed++
		}
	}
	return cf
}
