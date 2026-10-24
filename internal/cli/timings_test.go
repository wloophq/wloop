package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestTimingsPrintsRecordedSince(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	if code := te.run("timings", "--since", "7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); strings.Contains(out, "recorded since: unknown") || !strings.Contains(out, "recorded since ") {
		t.Fatalf("a fresh store does not say when recording began:\n%s", out)
	}
	since := testNow.Add(-3 * 24 * time.Hour)
	if err := te.Store.SetMeta(context.Background(), inventory.MetaTimingsSince, since.UTC().Format("2006-01-02T15:04:05.000000000Z")); err != nil {
		t.Fatal(err)
	}
	te.out.Reset()
	if code := te.run("timings", "--since", "7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{
		brand.Command + " timings over the last 7 days",
		"recorded since " + since.Local().Format("2006-01-02 15:04") + "; the days before have no turn, brief or request times",
		"asked of me -> my first review",
		"call of an ok brief",
		"turn tokens by agent and model",
		"--prs and --turns list them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n"); n > 40 {
		t.Errorf("%d lines by default:\n%s", n, out)
	}
	te.out.Reset()
	if code := te.run("timings", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	var rep api.TimingsReport
	if err := json.Unmarshal(te.out.Bytes(), &rep); err != nil || !rep.RecordedSince.Equal(since) {
		t.Fatalf("%v %+v", err, rep)
	}
	if code := te.run("timings", "--since", "0"); code != 1 {
		t.Fatalf("since 0: exit %d", code)
	}
}

func timingsFixture() api.TimingsReport {
	opened := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	return api.TimingsReport{Since: opened, RecordedSince: opened, Viewer: "me",
		Waiting: map[string]api.WaitShare{
			api.WaitingOnMe:   {TotalMS: (7 * time.Hour).Milliseconds(), OpenMS: (2 * time.Hour).Milliseconds(), PRs: 3, Spans: api.Quantiles{Count: 4, P50MS: (time.Hour).Milliseconds(), P90MS: (3 * time.Hour).Milliseconds()}},
			api.WaitingOnLoop: {TotalMS: (3 * time.Hour).Milliseconds(), OpenMS: (3 * time.Hour).Milliseconds(), PRs: 1},
		},
		ChangeFailure: api.ChangeFailure{Merged: 10, Briefed: 8, Failed: 2, Tagged: 3, Recent: 1, Days: 14, Definition: "the definition"},
		Milestones:    map[string]api.Quantiles{api.MilestoneReviewWait: {Count: 2, P50MS: (3*time.Hour + 10*time.Minute).Milliseconds(), P90MS: (52 * time.Hour).Milliseconds()}},
		Turns: map[string]api.Quantiles{"review": {Count: 4, P50MS: (6 * time.Minute).Milliseconds(), P90MS: (18 * time.Minute).Milliseconds()},
			api.TurnStartWait: {Count: 4, P50MS: 6500, P90MS: 9000}, api.TurnBriefWait: {Count: 1, P50MS: 19000, P90MS: 19000}},
		Briefs:  api.BriefTimings{Call: api.Quantiles{Count: 5, P50MS: 13200, P90MS: 18400}, Failed: 1, CostUSD: 0.42, Outages: 1, Unrecorded: 99},
		Tokens:  map[string]api.TokenCounts{"claude claude-opus-4": {Input: 1200, Output: 300_000, CacheRead: 50_100_000, CacheWrite: 2_000_000}},
		Tags:    map[string][]api.TagCount{"change": {{Value: "feature", Count: 12}, {Value: "fix", Count: 5}}},
		Dropped: []string{"change:refactorr (2)"},
		PRs: []api.PRTimes{{PR: "https://github.com/acme/api/pull/7", Title: "Batch export of every invoice the customer ever asked for", Opened: opened, FirstRequest: opened.Add(time.Hour),
			FirstReview: opened.Add(4 * time.Hour), Merged: opened.Add(50 * time.Hour), Turns: 1, Briefs: 2}},
		TurnList: []api.TurnTimes{{PR: "https://github.com/acme/api/pull/7", Seq: 12, Intent: "review", Trigger: "automated", State: "done", Agent: "claude",
			Sent: opened, Started: opened.Add(6500 * time.Millisecond), Ended: opened.Add(40 * time.Second), DurationMS: 33500, OutputTokens: 19900}},
	}
}

func TestTimingsPrintsTheSummary(t *testing.T) {
	var b strings.Builder
	printTimings(&b, timingsFixture(), timingsView{days: 30})
	out := b.String()
	if strings.Contains(out, viewerUnknown) || strings.Contains(out, "Batch export") || strings.Contains(out, "#12 review") {
		t.Errorf("the default output lists more than the summary:\n%s", out)
	}
	for _, want := range []string{
		fmt.Sprintf("  %-38s %10s %10s %5d", "asked of me -> my first review", "3h 10m", "2d 4h", 2),
		"mine: first push -> merged",
		"in " + brand.Name + ", since recording began",
		"  me                                          7h 0m      2h 0m     3      1h 0m      3h 0m",
		"  the author or other reviewers                   -          -     0          -          -",
		"  the review loop                             3h 0m      3h 0m     1          -          -",
		"\nwaits ",
		fmt.Sprintf("  %-38s %10s %10s %5d", "turn sent -> started", "6.5 s", "9.0 s", 4),
		fmt.Sprintf("  %-38s %10s %10s %5d", "brief wanted -> automated turn sent", "19.0 s", "19.0 s", 1),
		"briefs since recording began",
		"13.2 s     18.4 s     5",
		"1 failed, $0.42, 1 outage of the breaker; 99 runs from before recording left out",
		"claude claude-opus-4                         1.2k     300.0k        50.1M         2.0M",
		"change failure: 2 of 8 (25%) the definition; 10 merged in the window, 1 too recent to tell, 3 tagged change:fix or change:revert themselves",
		"1 pull requests and 1 turns in the window: --prs and --turns list them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestTimingsListsPullRequestsTurnsAndTags(t *testing.T) {
	rep := timingsFixture()
	opened := rep.PRs[0].Opened
	var b strings.Builder
	printTimings(&b, rep, timingsView{days: 30, prs: true, turns: true, tags: true})
	out := b.String()
	for _, want := range []string{
		"acme/api#7  Batch export of every invoice the custo… · opened " + opened.Local().Format("2006-01-02 15:04") + " · asked of me +1h 0m · my review +4h 0m · merged +2d 2h · 1 turn, 2 briefs",
		"acme/api#7 #12 review (automated) · sent " + opened.Local().Format("2006-01-02 15:04") + " · done · ran 33.5 s · started +6.5 s · 19.9k output tokens · claude",
		"change: feature 12, fix 5",
		"dropped spellings: change:refactorr (2)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestTimingsSaysWhenTheViewerIsUnknown(t *testing.T) {
	var b strings.Builder
	printTimings(&b, api.TimingsReport{Since: testNow}, timingsView{days: 30})
	out := b.String()
	for _, label := range []string{"asked of me -> my first review", "mine: opened -> merged", "mine: first push -> merged"} {
		if !strings.Contains(out, "  "+label+strings.Repeat(" ", 38-len(label))+" "+viewerUnknown) {
			t.Errorf("%s:\n%s", label, out)
		}
	}
	if strings.Count(out, viewerUnknown) != 3 {
		t.Errorf("unknown viewer lines:\n%s", out)
	}
	if !strings.Contains(out, "change failure: unknown: no briefed pull request merged at least 0 days ago") {
		t.Errorf("%s", out)
	}
}

func TestDoctorRepoWithAPullRequestUsesItsTags(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	user := t.TempDir()
	block := filepath.Join(user, "tags", "change=refac", "intent=review.md")
	os.MkdirAll(filepath.Dir(block), 0o700)
	os.WriteFile(block, []byte("Behavior must not change."), 0o600)
	te.cfg.Profile, te.cfg.Brief.Enabled = user, true
	if err := te.Store.UpsertPR(context.Background(), dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", BaseBranch: "main", URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	seedBriefOf(t, te.Store, r7, "change:refac")
	agentDaemon(t, te)
	if code := te.run("profile", "show", r7.URL(), "--kind", "review"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	out := te.out.String()
	for _, want := range []string{
		"facts of acme/api#7: author ana; association -; base main; brief: feature, size m, risk medium; tags change:refac",
		"user     " + block + "   tag change=refac   applies",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	te.out.Reset()
	if code := te.run("profile", "show", "acme/api", "--kind", "review"); code != 0 || !strings.Contains(te.out.String(), "tag change=refac   needs the brief") {
		t.Fatalf("exit %d: %s", code, te.out)
	}
}

func TestTheDaemonsFactsCarryTheBriefTextToTheCommandLine(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Brief.Enabled = true
	ctx := context.Background()
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", BaseBranch: "main", URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	seedBriefOf(t, te.Store, r7)
	f, err := te.prFacts(te.cfg)(ctx, r7.Unit())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f)
	var wire api.Facts
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	st, _ := factsTurn{wire}.Facts(ctx, r7)
	if b := st.Brief; b == nil || b.Goal != "Export in batches" || b.Context != "exports time out" || b.RiskWhy != "touches the export" {
		t.Fatalf("%+v", st.Brief)
	}
}

func TestDoctorRepoWithAPullRequestWithoutTheDaemon(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	if code := te.run("profile", "show", r7.URL(), "--kind", "review"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "brief facts unavailable: the daemon is not running") || !strings.Contains(out, "bundled  embedded intent=review.md") {
		t.Fatalf("%s", out)
	}
}
