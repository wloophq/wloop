package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/brief"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestStoredFactsReadTheBriefTagsFilesAndTeams(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	inventorytest.Of(te.Store).UpsertPR(ctx, dinv.PR{Ref: r7, State: "OPEN", HeadOid: "h1", ReviewRequests: []string{"bob", "team:acme/platform"}, Reasons: []string{"author"}})
	id, err := te.Store.StartBriefRun(ctx, dinv.BriefRun{PR: r7, Trigger: brief.TriggerWanted, Version: brief.Version(brief.DefaultTaxonomy), AttemptOf: "h1", StartedAt: testNow})
	if err != nil {
		t.Fatal(err)
	}
	run := dinv.BriefRun{ID: id, PR: r7, Trigger: brief.TriggerWanted, Version: brief.Version(brief.DefaultTaxonomy), AttemptOf: "h1", HeadOid: "h1",
		StartedAt: testNow, EndedAt: testNow, Outcome: brief.OutcomeOK, Files: []string{"a.go", "db/1.sql"}, Tags: []string{"lang:go"},
		Brief: `{"goal":"G","context":"C","kind":"fix","size":"s","risk":"low","risk_why":"W","open_questions":["Q"],"tags":["lang:go"]}`}
	if err := te.Store.EndBriefRun(ctx, run, dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	st, err := storedFacts{store: te.Store, prs: te.reportedPRs(), briefs: true}.Facts(ctx, r7.Unit())
	if err != nil || st.Brief == nil {
		t.Fatalf("%+v %v", st, err)
	}
	b := *st.Brief
	if b.Goal != "G" || b.Kind != "fix" || b.RiskWhy != "W" || strings.Join(b.Tags, ",") != "lang:go" || strings.Join(b.Files, ",") != "a.go,db/1.sql" ||
		b.Run != id || b.HeadMoved || b.OlderFormat || strings.Join(st.Teams, ",") != "acme-platform" {
		t.Fatalf("%+v %v", b, st.Teams)
	}
	inventorytest.Of(te.Store).UpsertPR(ctx, dinv.PR{Ref: r7, State: "OPEN", HeadOid: "h2", Reasons: []string{"author"}})
	if st, _ := (storedFacts{store: te.Store, prs: te.reportedPRs(), briefs: true}).Facts(ctx, r7.Unit()); st.Brief == nil || !st.Brief.HeadMoved || st.Brief.OlderFormat {
		t.Fatalf("a moved head: %+v", st.Brief)
	}
	if st, _ := (storedFacts{store: te.Store, prs: te.reportedPRs()}).Facts(ctx, r7.Unit()); st.Brief != nil {
		t.Fatal("briefs off still read the brief")
	}
}

func TestABriefOfAnOlderFormatIsNotAMovedHead(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	inventorytest.Of(te.Store).UpsertPR(ctx, dinv.PR{Ref: r7, State: "OPEN", HeadOid: "h1", Reasons: []string{"author"}})
	id, _ := te.Store.StartBriefRun(ctx, dinv.BriefRun{PR: r7, Trigger: brief.TriggerBackground, Version: "b1-old", AttemptOf: "h1", StartedAt: testNow})
	if err := te.Store.EndBriefRun(ctx, dinv.BriefRun{ID: id, PR: r7, Trigger: brief.TriggerBackground, Version: "b1-old", AttemptOf: "h1", HeadOid: "h1",
		StartedAt: testNow, EndedAt: testNow, Outcome: brief.OutcomeOK, Brief: `{"goal":"G","kind":"fix","size":"s","risk":"low","open_questions":[],"tags":[]}`}, dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	st, err := storedFacts{store: te.Store, prs: te.reportedPRs(), briefs: true}.Facts(ctx, r7.Unit())
	if err != nil || st.Brief == nil || st.Brief.HeadMoved || !st.Brief.OlderFormat {
		t.Fatalf("%+v %v", st.Brief, err)
	}
}

func TestATurnWithBriefsOffSaysSo(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Brief.Enabled = false
	c, _, err := te.renderTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Unit: r7.Unit(), Payload: &turn.PR{}})
	if err != nil || c.Brief != nil || !strings.Contains(c.BriefMissing, "brief.enabled") {
		t.Fatalf("%q %v", c.BriefMissing, err)
	}
}

func TestAnUnreadableBriefIsAnError(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	inventorytest.Of(te.Store).UpsertPR(ctx, dinv.PR{Ref: r7, State: "OPEN", HeadOid: "h1", Reasons: []string{"author"}})
	id, _ := te.Store.StartBriefRun(ctx, dinv.BriefRun{PR: r7, Trigger: brief.TriggerWanted, Version: "v", AttemptOf: "h1", StartedAt: testNow})
	te.Store.EndBriefRun(ctx, dinv.BriefRun{ID: id, PR: r7, Trigger: brief.TriggerWanted, Version: "v", AttemptOf: "h1", StartedAt: testNow, EndedAt: testNow,
		Outcome: brief.OutcomeOK, Brief: "{not json"}, dinv.BriefRecord{})
	if _, err := (storedFacts{store: te.Store, prs: te.reportedPRs(), briefs: true}).Facts(ctx, r7.Unit()); err == nil {
		t.Fatal("an unreadable brief read as none")
	}
}
