package daemon

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestTimingsReportQuantiles(t *testing.T) {
	var ds []time.Duration
	for _, v := range []int64{14300, 6200, 18400, 11300, 13200} {
		ds = append(ds, time.Duration(v)*time.Millisecond)
	}
	if q := quantiles(ds); q != (api.Quantiles{Count: 5, P50MS: 13200, P90MS: 18400}) {
		t.Fatalf("%+v", q)
	}
	if q := quantiles(nil); q != (api.Quantiles{}) {
		t.Fatalf("empty: %+v", q)
	}
	if q := quantiles(ds[:1]); q != (api.Quantiles{Count: 1, P50MS: 14300, P90MS: 14300}) {
		t.Fatalf("one: %+v", q)
	}
	ms := func(v int64) *int64 { return &v }
	d := dinv.TimingsData{Runs: []dinv.BriefRun{
		{Outcome: "ok", CallMS: ms(6200)}, {Outcome: "ok", CallMS: ms(11300)}, {Outcome: "ok", CallMS: ms(13200)},
		{Outcome: "ok", CallMS: ms(14300)}, {Outcome: "ok", CallMS: ms(18400)}, {Outcome: "timeout", CallMS: ms(30000)}, {Outcome: "reused"},
	}, Outages: 1}
	rep := timings(d, t0.Add(-30*24*time.Hour), t0, me{login: "me"}, nil)
	if rep.Briefs.Call != (api.Quantiles{Count: 5, P50MS: 13200, P90MS: 18400}) || rep.Briefs.Failed != 1 || rep.Briefs.Reused != 1 || rep.Briefs.Outages != 1 {
		t.Fatalf("briefs %+v", rep.Briefs)
	}
}

func TestTimingsMilestonesFromEvents(t *testing.T) {
	theirs := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	mine := github.Ref{Owner: "acme", Repo: "api", Number: 9}
	ev := func(ref github.Ref, kind, actor, data string, at time.Time) dinv.Event {
		return dinv.Event{PR: ref, Kind: kind, Actor: actor, Data: data, At: at}
	}
	opened := t0.Add(-10 * 24 * time.Hour)
	d := dinv.TimingsData{
		PRs: []dinv.PR{{Ref: theirs, Title: "Batch export", Author: "ana", CreatedAt: opened}, {Ref: mine, Author: "me", CreatedAt: opened}},
		Events: map[github.Ref][]dinv.Event{
			theirs: {
				ev(theirs, dinv.KindOpened, "ana", "", opened),
				ev(theirs, dinv.KindFollowed, "", "review-requested", opened.Add(time.Hour)),
				ev(theirs, dinv.KindReviewRequest, "ana", "me", opened.Add(time.Hour)),
				ev(theirs, dinv.KindReview, "bob", "COMMENTED:c1", opened.Add(2*time.Hour)),
				ev(theirs, dinv.KindReview, "me", "APPROVED:c1", opened.Add(4*time.Hour)),
				ev(theirs, dinv.KindReview, "me", "APPROVED:c2", opened.Add(5*time.Hour)),
				ev(theirs, dinv.KindMerged, "ana", "m1", opened.Add(26*time.Hour)),
				ev(theirs, dinv.KindUnfollowed, "", "review-requested", opened.Add(26*time.Hour)),
			},
			mine: {
				ev(mine, dinv.KindOpened, "me", "", opened),
				ev(mine, dinv.KindMerged, "me", "m2", opened.Add(48*time.Hour)),
			},
		},
		Turns: []dinv.TimedTurn{{Seq: 1, PR: theirs, Intent: "review", State: "done", SentAt: opened.Add(time.Hour), StartedAt: opened.Add(time.Hour + time.Minute), EndedAt: opened.Add(time.Hour + 11*time.Minute)}},
	}
	rep := timings(d, t0.Add(-30*24*time.Hour), t0, me{login: "me"}, nil)
	if len(rep.PRs) != 2 {
		t.Fatalf("prs %+v", rep.PRs)
	}
	got := rep.PRs[0]
	if got.PR != theirs.URL() || !got.Opened.Equal(opened) || !got.FirstRequest.Equal(opened.Add(time.Hour)) || !got.FirstReview.Equal(opened.Add(4*time.Hour)) ||
		!got.Approved.Equal(opened.Add(4*time.Hour)) || !got.Merged.Equal(opened.Add(26*time.Hour)) || !got.Followed.Equal(opened.Add(time.Hour)) || got.Turns != 1 {
		t.Fatalf("milestones %+v", got)
	}
	for key, want := range map[string]time.Duration{
		api.MilestoneReviewWait:  3 * time.Hour,
		api.MilestoneToApproval:  4 * time.Hour,
		api.MilestoneMineToMerge: 48 * time.Hour,
		api.MilestoneInLoop:      25 * time.Hour,
	} {
		if q := rep.Milestones[key]; q.Count != 1 || q.P50MS != want.Milliseconds() {
			t.Errorf("%s: %+v, want %v", key, q, want)
		}
	}
	if q := rep.Milestones[api.MilestoneToMerge]; q.Count != 2 {
		t.Errorf("to merge %+v", q)
	}
	if q := rep.Turns["review"]; q.Count != 1 || q.P50MS != (10*time.Minute).Milliseconds() {
		t.Errorf("review turns %+v", q)
	}
	if q := rep.Turns[api.TurnStartWait]; q.Count != 1 || q.P50MS != time.Minute.Milliseconds() {
		t.Errorf("start wait %+v", q)
	}
	d.Turns = nil
	old := timings(d, t0, t0, me{login: "me"}, nil)
	if len(old.PRs) != 0 || old.Milestones[api.MilestoneToMerge].Count != 0 {
		t.Fatalf("milestones before the window count: %+v", old)
	}
}

func TestTimingsTokensByModel(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	d := dinv.TimingsData{
		Turns: []dinv.TimedTurn{
			{Seq: 1, PR: ref, Intent: "review", State: "done", SentAt: t0},
			{Seq: 2, PR: ref, Intent: "address", State: "settled", SentAt: t0},
			{Seq: 3, PR: ref, Intent: "review", State: "withdrawn", SentAt: t0},
		},
		Tokens: []dinv.TurnTokens{
			{TurnSeq: 1, Model: "opus", Input: 10, Output: 100, CacheRead: 1000, CacheWrite: 50},
			{TurnSeq: 1, Model: "haiku", Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4},
			{TurnSeq: 2, Model: "opus", Input: 5, Output: 50, CacheRead: 500, CacheWrite: 25},
			{TurnSeq: 3, Model: "opus", Input: 1000, Output: 1000, CacheRead: 1000, CacheWrite: 1000},
		},
		Intakes: []dinv.TimedIntake{{AcceptedAt: t0, StartingAt: t0.Add(14 * time.Second)}, {AcceptedAt: t0}},
	}
	rep := timings(d, t0.Add(-time.Hour), t0, me{login: "me"}, nil)
	if rep.Tokens["opus"] != (api.TokenCounts{Input: 15, Output: 150, CacheRead: 1500, CacheWrite: 75}) || rep.Tokens["haiku"] != (api.TokenCounts{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}) {
		t.Fatalf("tokens %+v", rep.Tokens)
	}
	if rep.Intakes != (api.Quantiles{Count: 1, P50MS: 14000, P90MS: 14000}) {
		t.Fatalf("intakes %+v", rep.Intakes)
	}
}

func TestTimingsEndpoint(t *testing.T) {
	s := newServer(t)
	if err := s.store().SetMeta(context.Background(), inventory.MetaTimingsSince, "2026-10-01T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	var rep api.TimingsReport
	if code := get(t, s, api.AdminTimings+"?since=7", &rep); code != 200 {
		t.Fatalf("%d", code)
	}
	if !rep.RecordedSince.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !rep.Since.Equal(s.deps.Now().Add(-7*24*time.Hour)) {
		t.Fatalf("report %+v", rep)
	}
	for _, bad := range []string{"0", "x", "400"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", api.AdminTimings+"?since="+bad, nil))
		if rec.Code != 400 {
			t.Errorf("since=%s: %d", bad, rec.Code)
		}
	}
}

func TestTimingsWaitingOnMeAndOthers(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	since := t0.Add(-24 * time.Hour)
	sit := func(data string, at time.Time) dinv.Event {
		return dinv.Event{PR: ref, Kind: dinv.KindSituation, Data: data, At: at}
	}
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: ref}}, Events: map[github.Ref][]dinv.Event{ref: {
		sit("review:me", since.Add(-2*time.Hour)),
		sit("changes:author", since.Add(3*time.Hour)),
		sit("loop_working:loop", since.Add(5*time.Hour)),
		sit("review:me, bia", since.Add(6*time.Hour)),
		{PR: ref, Kind: dinv.KindUnfollowed, At: since.Add(10 * time.Hour)},
		sit("review:bia", since.Add(20*time.Hour)),
	}}}
	rep := timings(d, since, t0, me{login: "me"}, nil)
	want := map[string]time.Duration{api.WaitingOnMe: 3*time.Hour + 4*time.Hour, api.WaitingOnOthers: 2*time.Hour + 4*time.Hour, api.WaitingOnLoop: time.Hour}
	for party, total := range want {
		if got := rep.Waiting[party]; got.TotalMS != total.Milliseconds() || got.PRs != 1 {
			t.Errorf("%s: %+v, want %v", party, got, total)
		}
	}
	if got := rep.Waiting[api.WaitingOnMe]; got.Spans.Count != 2 || got.Spans.P90MS != (4*time.Hour).Milliseconds() || got.OpenMS != 0 {
		t.Errorf("on me %+v", got)
	}
	if got := rep.Waiting[api.WaitingOnOthers]; got.Spans.Count != 1 || got.Spans.P90MS != (2*time.Hour).Milliseconds() || got.OpenMS != (4*time.Hour).Milliseconds() {
		t.Errorf("on others, the open span counts only so far: %+v", got)
	}
}

func TestTimingsLeadTimeFromTheFirstPush(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	pushed := t0.Add(-5 * 24 * time.Hour)
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: ref, CreatedAt: pushed.Add(time.Hour)}}, Events: map[github.Ref][]dinv.Event{ref: {
		{PR: ref, Kind: dinv.KindPush, Key: "c2", At: pushed.Add(2 * time.Hour)},
		{PR: ref, Kind: dinv.KindPush, Key: "c1", At: pushed},
		{PR: ref, Kind: dinv.KindMerged, At: t0},
	}}}
	rep := timings(d, t0.Add(-7*24*time.Hour), t0, me{login: "me"}, nil)
	if q := rep.Milestones[api.MilestoneLeadTime]; q.Count != 1 || q.P50MS != (5*24*time.Hour).Milliseconds() {
		t.Fatalf("lead time %+v", q)
	}
	if q := timings(d, t0.Add(-24*time.Hour), t0, me{login: "me"}, nil).Milestones[api.MilestoneLeadTime]; q.Count != 0 {
		t.Fatalf("a lead time that began before the window counts: %+v", q)
	}
}

func TestTimingsChangeFailure(t *testing.T) {
	pr := func(n int, created time.Time) dinv.PR {
		return dinv.PR{Ref: github.Ref{Owner: "acme", Repo: "api", Number: n}, CreatedAt: created}
	}
	merged := t0.Add(-20 * 24 * time.Hour)
	failed, clean, unbriefed, fix, late, elsewhere := pr(1, merged.Add(-time.Hour)), pr(2, merged.Add(-time.Hour)), pr(3, merged.Add(-time.Hour)),
		pr(4, merged.Add(24*time.Hour)), pr(5, merged.Add(20*24*time.Hour)), pr(6, merged.Add(time.Hour))
	elsewhere.Ref.Repo = "web"
	mergedAt := func(p dinv.PR) []dinv.Event {
		return []dinv.Event{{PR: p.Ref, Kind: dinv.KindMerged, At: merged}}
	}
	d := dinv.TimingsData{
		PRs:    []dinv.PR{failed, clean, unbriefed, fix, late, elsewhere},
		Events: map[github.Ref][]dinv.Event{failed.Ref: mergedAt(failed), clean.Ref: mergedAt(clean), unbriefed.Ref: mergedAt(unbriefed)},
		PRTags: map[github.Ref][]string{fix.Ref: {"change:fix"}, late.Ref: {"change:revert"}, elsewhere.Ref: {"change:fix"}, clean.Ref: {"change:fix", "lang:go"}},
		PRFiles: map[github.Ref][]string{failed.Ref: {"api/export.go"}, clean.Ref: {"api/import.go"}, fix.Ref: {"api/export.go"},
			late.Ref: {"api/import.go"}, elsewhere.Ref: {"api/import.go"}},
	}
	got := timings(d, t0.Add(-30*24*time.Hour), t0, me{login: "me"}, nil).ChangeFailure
	if got.Merged != 3 || got.Briefed != 2 || got.Failed != 1 || got.Tagged != 1 || got.Days != FailureDays || got.Definition == "" {
		t.Fatalf("%+v", got)
	}
}

func TestASpanThatBeganBeforeTheWindowIsLeftOut(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	asked := t0.Add(-90 * 24 * time.Hour)
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: ref, Author: "ana", CreatedAt: asked.Add(-time.Hour)}}, Events: map[github.Ref][]dinv.Event{ref: {
		{PR: ref, Kind: dinv.KindReviewRequest, Data: "me", At: asked},
		{PR: ref, Kind: dinv.KindReview, Actor: "me", Data: "APPROVED:c1", At: t0.Add(-time.Hour)},
		{PR: ref, Kind: dinv.KindMerged, At: t0},
	}}}
	rep := timings(d, t0.Add(-30*24*time.Hour), t0, me{login: "me"}, nil)
	for _, key := range []string{api.MilestoneReviewWait, api.MilestoneToApproval, api.MilestoneToMerge} {
		if q := rep.Milestones[key]; q.Count != 0 {
			t.Errorf("%s: %+v", key, q)
		}
	}
	if len(rep.PRs) != 1 {
		t.Fatalf("the pull request with a merge in the window is not listed: %+v", rep.PRs)
	}
}
