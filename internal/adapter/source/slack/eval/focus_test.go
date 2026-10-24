package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	slackadapter "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/core/turns"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

func dmFixture() Fixture {
	return Fixture{ID: "01-dm", Kind: "dm_segment", Split: SplitHoldout, ChannelID: "D0000000009", ChannelType: "im",
		FocusTS: "1792586400.000100",
		Context: []Message{
			{TS: "1792580000.000100", UserID: "U0000000002", UserName: "Carol", Text: "the offsite is moved to march"},
			{TS: "1792580100.000100", UserID: "U0000000001", UserName: "Vera", Text: "noted"},
		},
		Messages: []Message{
			{TS: "1792586400.000100", UserID: "U0000000002", UserName: "Carol", Text: "can you book the room for it?"},
			{TS: "1792587000.000200", UserID: "U0000000001", UserName: "Vera", Text: "booking it today"},
		}}
}

func dmSet(t *testing.T, fs ...Fixture) Set {
	t.Helper()
	dir := t.TempDir()
	for _, f := range fs {
		if err := writeJSON(filepath.Join(dir, f.ID+".json"), f); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(filepath.Join(dir, viewerFile), map[string]string{"id": "U0000000001", "name": "Vera"}); err != nil {
		t.Fatal(err)
	}
	return loadSet(t, dir)
}

func TestDMContextAndFocusReachOnlyTheExtractor(t *testing.T) {
	f := dmFixture()
	set := dmSet(t, f)
	a := &scripted{set: set}
	o := options(t, a, set)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	stages := a.byStage()
	ledger := stages["ledger"][0].Prompt
	for _, want := range []string{extract.ContextHeading, "[c1 ts=1792580000.000100", extract.SegmentStarts, "[m1 ts=1792586400.000100", "] focus new Carol", extract.SegmentEnds} {
		if !strings.Contains(ledger, want) {
			t.Errorf("the extractor prompt lacks %q:\n%s", want, ledger)
		}
	}

	plain := f
	plain.Context, plain.FocusTS = nil, ""
	pset := dmSet(t, plain)
	pa := &scripted{set: pset}
	if _, err := Run(context.Background(), options(t, pa, pset)); err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{"oracle", "judge"} {
		if got, want := stages[st][0].Prompt, pa.byStage()[st][0].Prompt; got != want {
			t.Errorf("the %s sees context or focus; its prompt must stay the plain one:\n%s", st, got)
		}
	}
}

func TestEscalateOverCharsSendsLongUnitsToFrontier(t *testing.T) {
	long := dmFixture()
	long.ID = "02-long"
	long.ChannelID = "D0000000008"
	long.Context = nil
	for i := range long.Messages {
		long.Messages[i].TS = strings.Replace(long.Messages[i].TS, "17925", "17926", 1)
		long.Messages[i].Text += strings.Repeat(" with many details", 40)
	}
	long.FocusTS = long.Messages[0].TS
	set := dmSet(t, dmFixture(), long)
	a := &scripted{set: set}
	o := options(t, a, set)
	o.EscalateOverChars = 1000
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	tiers := map[string]turns.Tier{}
	for _, q := range a.byStage()["ledger"] {
		tiers[a.fixtureOf(q.Prompt).ID] = q.Tier
		if q.MaxCostUSD != ExtractorCallUSD {
			t.Errorf("an escalated call lost its cap: %v", q.MaxCostUSD)
		}
	}
	if tiers["01-dm"] != turns.TierFast || tiers["02-long"] != turns.TierFrontier {
		t.Fatalf("tiers %v", tiers)
	}
}

func TestExportKeepsFocusAndContextOfADMSegment(t *testing.T) {
	o := exportStore(t)
	ctx := context.Background()
	st := o.Facts.(*slackadapter.Store)
	if _, err := o.Segments.(slackadapter.Segments).Apply(ctx, exTeam, exDM, []dslack.Span{{FromTS: exTS(97), ToTS: exTS(99)}, {FromTS: exTS(100)}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ctx, nil, dslack.Facts{Team: exTeam, DMs: []dslack.DMWindow{{Conversation: exDM, Latest: exTS(101), Messages: []dslack.Message{
		{TS: exTS(97), User: exOther, Text: "first earlier message"},
		{TS: exTS(98), User: exViewer, Text: "second earlier message"},
		{TS: exTS(99), User: exOther, Text: "third earlier message"},
	}}}}, "", exportNow); err != nil {
		t.Fatal(err)
	}
	o.Since = time.Unix(1792500000+100, 0)
	o.Before, o.ContextMessages, o.ContextChars = st.MessagesBefore, 2, 1000
	if _, err := Export(ctx, o); err != nil {
		t.Fatal(err)
	}
	for _, f := range loadSet(t, o.Out).Fixtures {
		if f.Kind != "dm_segment" {
			continue
		}
		if f.FocusTS != exTS(100) || len(f.Messages) != 2 || len(f.Context) != 2 || f.Context[0].TS != exTS(98) || f.Context[1].TS != exTS(99) ||
			f.Context[0].UserName != "Vera" {
			t.Fatalf("dm fixture %+v", f)
		}
		return
	}
	t.Fatal("no dm fixture")
}
