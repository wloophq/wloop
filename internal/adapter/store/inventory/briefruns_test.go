package inventory

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/tag"
	"github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

var runAt = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func fullRun() dinv.BriefRun {
	return dinv.BriefRun{PR: ref, Trigger: "wanted", Intake: 4, Version: "b2-abc", AttemptOf: "b2-abc h1 2026-10-18T11:00:00Z 3", HeadOid: "h1",
		BodyEditedAt: runAt.Add(-time.Hour), ChangedFiles: 3, StartedAt: runAt}
}

func endOK(r dinv.BriefRun, id int64) dinv.BriefRun {
	r.ID, r.EndedAt = id, runAt.Add(9*time.Second)
	r.InputMS, r.CallMS, r.AgentMS = ptr[int64](800), ptr[int64](8200), ptr[int64](7900)
	r.Outcome, r.Model, r.CostUSD = "ok", "haiku", ptr(0.011)
	r.Tokens = &usage.Tokens{Input: 1200, Output: 300, CacheRead: 9000, CacheWrite: 40}
	r.DiffBytes, r.DiffCut = ptr(51200), true
	r.Brief = `{"goal":"x","tags":["change:fix","area:store"]}`
	r.Files, r.Tags, r.DroppedTags = []string{"a.go", "b.go"}, []string{"change:fix", "area:store"}, []string{"vibe:good"}
	return r
}

func eventsOf(t *testing.T, s *Store, kind string) []dinv.Event {
	t.Helper()
	evs, err := s.Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(evs, func(e dinv.Event) bool { return e.Kind != kind })
}

func TestBriefRunRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.StartBriefRun(ctx, fullRun())
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := s.BriefRuns(ctx, runAt); err != nil || len(runs) != 1 || runs[0].Outcome != "running" || runs[0].EndedAt != (time.Time{}) {
		t.Fatalf("a started run: %+v %v", runs, err)
	}
	ended := endOK(fullRun(), id)
	if err := s.EndBriefRun(ctx, ended, dinv.BriefRecord{AttemptOf: ended.AttemptOf}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.BriefRuns(ctx, runAt)
	listed := ended
	listed.Brief, listed.Files = "", nil
	if err != nil || len(runs) != 1 || !reflect.DeepEqual(runs[0], listed) {
		t.Fatalf("got %+v %v\nwant %+v", runs, err, listed)
	}
	if one, ok, err := s.BriefRun(ctx, id); err != nil || !ok || !reflect.DeepEqual(one, ended) {
		t.Fatalf("run %d: %+v %v %v", id, one, ok, err)
	}
	if _, ok, err := s.BriefRun(ctx, id+1); err != nil || ok {
		t.Fatalf("an unknown run: %v %v", ok, err)
	}
	if evs := eventsOf(t, s, dinv.KindBriefEnded); len(evs) != 1 || evs[0].Key != "1" || evs[0].Data != "ok" || !evs[0].At.Equal(ended.EndedAt) {
		t.Fatalf("events %+v", evs)
	}
	rec, ok, err := s.BriefRecord(ctx, ref)
	if err != nil || !ok || rec.Brief != ended.Brief || rec.Model != "haiku" || rec.CostUSD != 0.011 || !rec.CreatedAt.Equal(ended.EndedAt) ||
		!rec.AttemptedAt.Equal(runAt) || rec.Version != "b2-abc" || rec.HeadOid != "h1" || rec.ChangedFiles != 3 || !rec.BodyEditedAt.Equal(ended.BodyEditedAt) ||
		rec.Run != id || rec.LastRun != id || rec.Error != "" || rec.AttemptOf != ended.AttemptOf {
		t.Fatalf("record %+v %v %v", rec, ok, err)
	}
	if tags, err := s.Tags(ctx, ref); err != nil || !slices.Equal(tags, []string{"area:store", "change:fix"}) {
		t.Fatalf("tags %v %v", tags, err)
	}
}

func TestUnmeasuredDurationsStayNil(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.StartBriefRun(ctx, fullRun())
	if err != nil {
		t.Fatal(err)
	}
	r := fullRun()
	r.ID, r.EndedAt, r.Outcome, r.Error = id, runAt.Add(time.Second), "unavailable", "gh: not logged in"
	if err := s.EndBriefRun(ctx, r, dinv.BriefRecord{Failures: 1}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.BriefRuns(ctx, time.Time{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("%+v %v", runs, err)
	}
	got := runs[0]
	if got.InputMS != nil || got.CallMS != nil || got.AgentMS != nil || got.CostUSD != nil || got.Tokens != nil || got.DiffBytes != nil {
		t.Fatalf("unmeasured fields: %+v", got)
	}
	var nulls int
	if err := s.db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM brief_runs WHERE input_ms IS NULL AND call_ms IS NULL AND agent_ms IS NULL AND cost_usd IS NULL
		AND input_tokens IS NULL AND output_tokens IS NULL AND cache_read_tokens IS NULL AND cache_write_tokens IS NULL AND input_bytes IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("NULL columns: %d %v", nulls, err)
	}
	if rec, _, _ := s.BriefRecord(ctx, ref); rec.Error != "gh: not logged in" || rec.Brief != "" || rec.Failures != 1 || rec.Run != 0 || rec.LastRun != id {
		t.Fatalf("record %+v", rec)
	}
}

func TestBriefRunReadsOneRunByID(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	r := fullRun()
	r.Files = []string{"a.go", "b.go"}
	id, _ := s.StartBriefRun(ctx, r)
	if err := s.EndBriefRun(ctx, endOK(r, id), dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.BriefRun(ctx, id)
	if err != nil || !ok || got.ID != id || !slices.Equal(got.Files, []string{"a.go", "b.go"}) {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if _, ok, err := s.BriefRun(ctx, id+1); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestAFailedRunKeepsTheBriefAndTags(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	good, _ := s.StartBriefRun(ctx, fullRun())
	if err := s.EndBriefRun(ctx, endOK(fullRun(), good), dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	later := fullRun()
	later.StartedAt, later.AttemptOf = runAt.Add(time.Hour), "b2-abc h2 2026-10-18T11:00:00Z 9"
	bad, _ := s.StartBriefRun(ctx, later)
	later.ID, later.EndedAt, later.Outcome, later.Error, later.Tags = bad, later.StartedAt.Add(time.Minute), "timeout", "", []string{"change:revert"}
	if err := s.EndBriefRun(ctx, later, dinv.BriefRecord{Failures: 2, AttemptOf: later.AttemptOf}); err != nil {
		t.Fatal(err)
	}
	rec, _, err := s.BriefRecord(ctx, ref)
	if err != nil || rec.Brief != `{"goal":"x","tags":["change:fix","area:store"]}` || rec.Model != "haiku" || rec.Error != "timeout" || !rec.AttemptedAt.Equal(later.StartedAt) ||
		rec.Failures != 2 || rec.AttemptOf != later.AttemptOf || rec.Run != good || rec.LastRun != bad {
		t.Fatalf("record %+v %v", rec, err)
	}
	if tags, _ := s.Tags(ctx, ref); !slices.Equal(tags, []string{"area:store", "change:fix"}) {
		t.Fatalf("tags %v", tags)
	}
	next, _ := s.StartBriefRun(ctx, fullRun())
	ok := endOK(fullRun(), next)
	ok.Tags = []string{"change:revert"}
	if err := s.EndBriefRun(ctx, ok, dinv.BriefRecord{Failures: 2}); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := s.BriefRecord(ctx, ref); rec.Failures != 0 || rec.Error != "" || rec.Run != next {
		t.Fatalf("after a success %+v", rec)
	}
	if tags, _ := s.Tags(ctx, ref); !slices.Equal(tags, []string{"change:revert"}) {
		t.Fatalf("tags after a success %v", tags)
	}
}

func TestInterruptBriefRuns(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	done, _ := s.StartBriefRun(ctx, fullRun())
	if err := s.EndBriefRun(ctx, endOK(fullRun(), done), dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	s.StartBriefRun(ctx, fullRun())
	s.StartBriefRun(ctx, fullRun())
	at := runAt.Add(time.Hour)
	if n, err := s.InterruptBriefRuns(ctx, at); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	runs, _ := s.BriefRuns(ctx, time.Time{})
	var outcomes []string
	for _, r := range runs {
		outcomes = append(outcomes, r.Outcome)
		if r.Outcome == "interrupted" && !r.EndedAt.Equal(at) {
			t.Fatalf("ended %v", r.EndedAt)
		}
	}
	if !slices.Equal(outcomes, []string{"ok", "interrupted", "interrupted"}) {
		t.Fatalf("outcomes %v", outcomes)
	}
	if evs := eventsOf(t, s, dinv.KindBriefEnded); len(evs) != 3 || evs[1].Data != "interrupted" || evs[2].Key != "3" {
		t.Fatalf("events %+v", evs)
	}
	if n, err := s.InterruptBriefRuns(ctx, at); err != nil || n != 0 {
		t.Fatalf("again: %d %v", n, err)
	}
}

func TestWantBriefSetsOnce(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.WantBrief(ctx, ref, runAt); err != nil {
		t.Fatal(err)
	}
	if err := s.WantBrief(ctx, ref, runAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rec, ok, err := s.BriefRecord(ctx, ref); err != nil || !ok || !rec.WantedAt.Equal(runAt) || rec.Brief != "" || rec.Error != "" {
		t.Fatalf("%+v %v %v", rec, ok, err)
	}
	if evs := eventsOf(t, s, dinv.KindBriefWanted); len(evs) != 1 || !evs[0].At.Equal(runAt) {
		t.Fatalf("events %+v", evs)
	}
}

func TestClearWanted(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.WantBrief(ctx, ref, runAt)
	if err := s.ClearWanted(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := s.BriefRecord(ctx, ref); !rec.WantedAt.IsZero() {
		t.Fatalf("still wanted %+v", rec)
	}
	later := runAt.Add(time.Hour)
	s.WantBrief(ctx, ref, later)
	if rec, _, _ := s.BriefRecord(ctx, ref); !rec.WantedAt.Equal(later) {
		t.Fatalf("wanted again %+v", rec)
	}
	if evs := eventsOf(t, s, dinv.KindBriefWanted); len(evs) != 2 {
		t.Fatalf("events %+v", evs)
	}
}

func TestGiveUpWritesItsEventOnce(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	st := dinv.BriefRecord{Failures: 3, GaveUpAt: runAt.Add(time.Minute), GaveUpReason: "3 failures: timeout", RetryAt: runAt.Add(time.Hour)}
	for range 2 {
		id, _ := s.StartBriefRun(ctx, fullRun())
		r := fullRun()
		r.ID, r.EndedAt, r.Outcome, r.Error = id, runAt.Add(time.Minute), "timeout", "the call timed out"
		if err := s.EndBriefRun(ctx, r, st); err != nil {
			t.Fatal(err)
		}
	}
	if evs := eventsOf(t, s, dinv.KindBriefGaveUp); len(evs) != 1 || evs[0].Data != "3 failures: timeout" || !evs[0].At.Equal(st.GaveUpAt) {
		t.Fatalf("events %+v", evs)
	}
	if rec, _, _ := s.BriefRecord(ctx, ref); !rec.GaveUpAt.Equal(st.GaveUpAt) || rec.GaveUpReason != st.GaveUpReason || !rec.RetryAt.Equal(st.RetryAt) {
		t.Fatalf("record %+v", rec)
	}
}

func TestRefreshKeepsTheRuns(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, _ := s.StartBriefRun(ctx, fullRun())
	if err := s.EndBriefRun(ctx, endOK(fullRun(), id), dinv.BriefRecord{Failures: 1, GaveUpAt: runAt, GaveUpReason: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshBrief(ctx, ref); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.BriefRecord(ctx, ref)
	if err != nil || !ok || rec.Run != 0 || rec.Brief != "" || rec.Failures != 0 || !rec.GaveUpAt.IsZero() || rec.GaveUpReason != "" || rec.LastRun != id {
		t.Fatalf("%+v %v %v", rec, ok, err)
	}
	if run, ok, _ := s.BriefRun(ctx, id); !ok || run.Brief == "" || len(run.Tags) != 2 {
		t.Fatalf("run %+v", run)
	}
}

func TestRefreshDropsOnlyItsPullRequestsTags(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	other := github.Ref{Owner: "acme", Repo: "web", Number: 2}
	for _, pr := range []github.Ref{ref, other} {
		r := fullRun()
		r.PR = pr
		id, _ := s.StartBriefRun(ctx, r)
		ended := endOK(r, id)
		if err := s.EndBriefRun(ctx, ended, dinv.BriefRecord{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RefreshBrief(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if tags, err := s.Tags(ctx, ref); err != nil || len(tags) != 0 {
		t.Fatalf("tags after a refresh %v %v", tags, err)
	}
	if tags, err := s.Tags(ctx, other); err != nil || len(tags) != 2 {
		t.Fatalf("the other pull request's tags %v %v", tags, err)
	}
}

func TestTagVocabularyPutsTheOwnerFirst(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	n := 0
	tagged := func(owner string, tags ...string) {
		n++
		r := fullRun()
		r.PR = github.Ref{Owner: owner, Repo: "r", Number: n}
		id, _ := s.StartBriefRun(ctx, r)
		ended := endOK(r, id)
		ended.Tags = tags
		if err := s.EndBriefRun(ctx, ended, dinv.BriefRecord{}); err != nil {
			t.Fatal(err)
		}
	}
	tagged("acme", "lang:go", "domain:billing", "change:fix")
	tagged("acme", "lang:python")
	for range 3 {
		tagged("other", "lang:rust", "lang:python", "tool:docker")
	}
	got, err := s.TagVocabulary(ctx, "acme", []string{"lang", "domain", "framework"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]tag.Count{
		"lang":   {{Value: "python", Count: 4}, {Value: "go", Count: 1}},
		"domain": {{Value: "billing", Count: 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got, _ := s.TagVocabulary(ctx, "nobody", []string{"lang"}, 30); !reflect.DeepEqual(got["lang"], []tag.Count{{Value: "python", Count: 4}, {Value: "rust", Count: 3}, {Value: "go", Count: 1}}) {
		t.Fatalf("without the owner: %+v", got)
	}
}

func TestBriefRecordsOfEveryPR(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	other := github.Ref{Owner: "acme", Repo: "web", Number: 2}
	id, _ := s.StartBriefRun(ctx, fullRun())
	if err := s.EndBriefRun(ctx, endOK(fullRun(), id), dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	s.WantBrief(ctx, other, runAt)
	all, err := s.BriefRecords(ctx)
	if err != nil || len(all) != 2 || all[ref].Brief == "" || all[ref].Run != id || !all[other].WantedAt.Equal(runAt) || all[other].PR != other {
		t.Fatalf("%+v %v", all, err)
	}
	some, err := s.BriefRecords(ctx, other, github.Ref{Owner: "acme", Repo: "api", Number: 99})
	if err != nil || len(some) != 1 || some[other].PR != other {
		t.Fatalf("records of two refs: %+v %v", some, err)
	}
	if _, ok, err := s.BriefRecord(ctx, github.Ref{Owner: "acme", Repo: "api", Number: 99}); ok || err != nil {
		t.Fatalf("unknown: %v %v", ok, err)
	}
}

func briefFixture(t *testing.T, s *Store, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		r := github.Ref{Owner: "acme", Repo: "api", Number: i}
		state := "OPEN"
		if i%10 == 0 {
			state = "MERGED"
		}
		upsert(t, s, dinv.PR{Ref: r, State: state, HeadOid: "h1", ChangedFiles: i, Reasons: []string{"repo"}, UpdatedAt: runAt.Add(-time.Duration(i) * time.Minute),
			BodyEditedAt: runAt.Add(-time.Hour)})
		if i%3 == 0 {
			continue
		}
		run := fullRun()
		run.PR = r
		id, err := s.StartBriefRun(ctx, run)
		if err != nil {
			t.Fatal(err)
		}
		end := endOK(run, id)
		st := dinv.BriefRecord{AttemptOf: end.AttemptOf}
		if i%3 == 2 {
			end.Outcome, end.Error, end.Brief, end.Tags = "failed", "boom", "", nil
			st.Failures, st.RetryAt = 1, runAt.Add(time.Minute)
		}
		if i%7 == 0 {
			if err := s.WantBrief(ctx, r, runAt); err != nil {
				t.Fatal(err)
			}
			st.WantedAt = runAt
		}
		if err := s.EndBriefRun(ctx, end, st); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBriefStatesSelectsNoBriefJSON(t *testing.T) {
	s := open(t)
	ctx, sc := metertest.Start(t, context.Background())
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"repo"}})
	run := fullRun()
	id, err := s.StartBriefRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	end := endOK(run, id)
	end.Brief = strings.Repeat("x", 100<<10)
	if err := s.EndBriefRun(ctx, end, dinv.BriefRecord{AttemptOf: end.AttemptOf}); err != nil {
		t.Fatal(err)
	}
	before := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "query"})
	states, err := s.BriefStates(ctx)
	if err != nil || len(states) != 1 {
		t.Fatalf("%+v %v", states, err)
	}
	if got := states[0]; got.Rec.Brief != "" || got.Rec.Run != id || !got.Has {
		t.Fatalf("the record carries %d bytes of brief, run %d: %+v", len(got.Rec.Brief), got.Rec.Run, got.Rec)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "query"}) - before; n != 1 {
		t.Fatalf("BriefStates ran %d queries, want 1, the briefs': the pull requests are the GitHub plugin's reports\n%s", n, metertest.Report(sc.Tally()))
	}
	if full, _, _ := s.BriefRecord(ctx, ref); len(full.Brief) != 100<<10 {
		t.Fatalf("the stored brief is %d bytes", len(full.Brief))
	}
}

func TestBriefStatesMatchesListPRsAndBriefRecords(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	briefFixture(t, s, 70)
	states, err := s.BriefStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prs, err := (*s.prs.Load()).List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := s.BriefRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != len(prs) || len(prs) != 63 {
		t.Fatalf("%d states, %d open pull requests", len(states), len(prs))
	}
	withRecord := 0
	for i, st := range states {
		if !reflect.DeepEqual(st.PR, prs[i]) {
			t.Fatalf("pull request %d:\n got %+v\nwant %+v", i, st.PR, prs[i])
		}
		want, has := recs[st.PR.Ref]
		want.Brief = ""
		if !has {
			want = dinv.BriefRecord{PR: st.PR.Ref}
		} else {
			withRecord++
		}
		if st.Has != has || !reflect.DeepEqual(st.Rec, want) {
			t.Fatalf("record of %s:\n got %+v (has %v)\nwant %+v (has %v)", st.PR.Ref, st.Rec, st.Has, want, has)
		}
	}
	if withRecord == 0 || withRecord == len(states) {
		t.Fatalf("the fixture has %d records of %d", withRecord, len(states))
	}
}

func TestEndBriefRunReusedIsOneTransaction(t *testing.T) {
	s := open(t)
	ctx, sc := metertest.Start(t, context.Background())
	good, _ := s.StartBriefRun(ctx, fullRun())
	first := endOK(fullRun(), good)
	first.InputSHA = "sha1"
	if err := s.EndBriefRun(ctx, first, dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	reuse := fullRun()
	reuse.StartedAt = runAt.Add(time.Hour)
	id, _ := s.StartBriefRun(ctx, reuse)
	reuse.ID, reuse.EndedAt, reuse.Outcome, reuse.InputSHA, reuse.ReusedFrom = id, reuse.StartedAt.Add(time.Second), "reused", "sha1", good
	before := metertest.Count(sc, metertest.Limit{Res: meter.Tx})
	if err := s.EndBriefRun(ctx, reuse, dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx}) - before; n != 1 {
		t.Fatalf("%d transactions for one reused end", n)
	}
	rec, _, _ := s.BriefRecord(ctx, ref)
	if rec.Run != id || rec.LastRun != id || rec.Brief != first.Brief || rec.Model != "haiku" || rec.InputSHA != "sha1" || rec.Error != "" {
		t.Fatalf("record %+v", rec)
	}
	run, _, _ := s.BriefRun(ctx, id)
	if run.Brief != first.Brief || run.ReusedFrom != good || run.InputSHA != "sha1" || !slices.Equal(run.Tags, first.Tags) || run.CostUSD != nil {
		t.Fatalf("run %+v", run)
	}
	if tags, err := s.Tags(ctx, ref); err != nil || !slices.Equal(tags, slices.Sorted(slices.Values(first.Tags))) {
		t.Fatalf("the reused run's brief record has the tags %v: %v", tags, err)
	}
	if evs := eventsOf(t, s, dinv.KindBriefEnded); len(evs) != 2 || evs[1].Data != "reused" {
		t.Fatalf("events %+v", evs)
	}
}

func TestReusingARunWithoutABriefFails(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, _ := s.StartBriefRun(ctx, fullRun())
	r := fullRun()
	r.ID, r.EndedAt, r.Outcome, r.ReusedFrom = id, runAt.Add(time.Second), "reused", 99
	if err := s.EndBriefRun(ctx, r, dinv.BriefRecord{}); err == nil || !strings.Contains(err.Error(), "no brief") {
		t.Fatalf("%v", err)
	}
}
