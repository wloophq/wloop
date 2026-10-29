package brief

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/github"
)

func TestInputSHAIgnoresCheckboxTicks(t *testing.T) {
	open := Input{Title: "t", Body: "Plan\n- [ ] one\n  * [ ] two\n1. [ ] three\nliteral [ ] in prose"}
	ticked := Input{Title: "t", Body: "Plan\n- [x] one\n  * [X] two\n1. [x] three\nliteral [ ] in prose"}
	if InputSHA("h1", "", open) != InputSHA("h1", "", ticked) {
		t.Fatal("ticking a task changed the hash")
	}
	prose := Input{Title: "t", Body: "Plan\n- [ ] one\n  * [ ] two\n1. [ ] three\nliteral [x] in prose"}
	if InputSHA("h1", "", open) == InputSHA("h1", "", prose) {
		t.Fatal("a [x] in prose was read as a box")
	}
}

func TestInputSHAChangesWithTheHead(t *testing.T) {
	in := Input{Title: "t", Body: "b", Files: []string{"a.go (+1 -0)"}}
	if InputSHA("h1", "", in) == InputSHA("h2", "", in) {
		t.Fatal("a new head kept the hash")
	}
}

func TestInputSHAChangesWithTheIssue(t *testing.T) {
	a := Input{Title: "t", Body: "b", Issue: "bug\n\nit breaks"}
	b := a
	b.Issue = "bug\n\nit breaks twice"
	if InputSHA("h1", "", a) == InputSHA("h1", "", b) {
		t.Fatal("a new issue kept the hash")
	}
	c := a
	c.Files = []string{"a.go (+1 -0)"}
	if InputSHA("h1", "", a) == InputSHA("h1", "", c) {
		t.Fatal("new files kept the hash")
	}
}

func (r *rig) editedBody(t *testing.T, head string) {
	t.Helper()
	r.now = r.now.Add(time.Minute)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	pr, _, err := inventorytest.Of(r.st).PR(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	pr.BodyEditedAt, pr.HeadOid = r.now, head
	if err := inventorytest.Of(r.st).UpsertPR(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(BodyQuiet + time.Minute)
}

func TestAnUnchangedInputReusesTheBrief(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	r.src.in = &Input{Body: "- [ ] step"}
	r.run.res = &Result{Brief: Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low", Tags: []string{"change:fix"}}, Model: "haiku", CostUSD: ptr(0.01)}
	r.q.Run(ctx)
	first, _, _ := r.st.BriefRecord(ctx, ref)
	if r.run.calls != 1 || first.Run == 0 || first.InputSHA == "" {
		t.Fatalf("first brief: %d calls, %+v", r.run.calls, first)
	}
	r.src.in = &Input{Body: "- [x] step"}
	r.editedBody(t, "h1")
	diffs := len(r.src.diffs)
	r.q.Run(ctx)
	rec, _, _ := r.st.BriefRecord(ctx, ref)
	if r.run.calls != 1 || len(r.src.facts) != 2 || len(r.src.diffs) != diffs {
		t.Fatalf("%d Runner, %d Facts, %d new Diff calls", r.run.calls, len(r.src.facts), len(r.src.diffs)-diffs)
	}
	if rec.Run == first.Run || rec.Run != rec.LastRun || rec.Brief != first.Brief || rec.InputSHA != first.InputSHA {
		t.Fatalf("reused record %+v, first %+v", rec, first)
	}
	run, _, _ := r.st.BriefRun(ctx, rec.Run)
	if run.Outcome != OutcomeReused || run.ReusedFrom != first.Run || run.CostUSD != nil || run.Tokens != nil {
		t.Fatalf("run %+v", run)
	}
	tags, _ := r.st.Tags(ctx, ref)
	if len(tags) != 1 || tags[0] != "change:fix" {
		t.Fatal("the reused brief lost its tags")
	}
	if st, _ := r.q.Status(ctx); st.Ready != 1 || st.Pending != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestAChangedInputIsBriefedAgain(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.src.in = &Input{Body: "one"}
	r.q.Run(ctx)
	r.src.in = &Input{Body: "two"}
	r.editedBody(t, "h1")
	r.q.Run(ctx)
	if r.run.calls != 2 {
		t.Fatalf("%d Runner calls", r.run.calls)
	}
}

func TestAChangedHeadIsBriefedAgain(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.src.in = &Input{Body: "one"}
	r.q.Run(ctx)
	r.editedBody(t, "h2")
	r.q.Run(ctx)
	if r.run.calls != 2 {
		t.Fatalf("%d Runner calls", r.run.calls)
	}
}

func TestReuseNeedsTheCurrentVersion(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.src.in = &Input{Body: "one"}
	r.q.Run(ctx)
	r.q.d.Taxonomy = func(github.Ref) string { return DefaultTaxonomy + "\n- extra: a new taxonomy" }
	if rd := r.ready(t, refN(1), false); rd.State != Ready || rd.Brief == nil {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	runs := runsOf(t, r.st)
	if last := runs[len(runs)-1]; r.run.calls != 2 || last.Trigger != TriggerRefresh || last.Outcome != OutcomeOK || last.ReusedFrom != 0 {
		t.Fatalf("%d calls, last run %+v", r.run.calls, last)
	}
}

func TestABriefMadeWithoutTheDiffIsNeverReused(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	r.src.in = &Input{Body: "one"}
	r.src.diffErr = errors.New("gh api: HTTP 502")
	r.q.Run(ctx)
	if rec, _, _ := r.st.BriefRecord(ctx, ref); rec.Run == 0 || rec.InputSHA != "" {
		t.Fatalf("record %+v", rec)
	}
	r.src.diffErr = nil
	r.editedBody(t, "h1")
	r.q.Run(ctx)
	if r.run.calls != 2 {
		t.Fatalf("%d Runner calls", r.run.calls)
	}
	if rec, _, _ := r.st.BriefRecord(ctx, ref); rec.InputSHA == "" {
		t.Fatalf("the brief with the diff has no hash: %+v", rec)
	}
}
