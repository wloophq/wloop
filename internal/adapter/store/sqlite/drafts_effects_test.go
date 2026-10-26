package sqlite

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.DraftEffects", "draftEffects.Unapplied")
	closed := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/2.2"}
	writes = append(writes,
		writeCase{
			method: "draftEffects.MarkApplied",
			seed: func(t *testing.T, e *noteEnv) {
				if _, err := e.db.Drafts().Insert(context.Background(), sampleDraft(slackThread, noteAt)); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error { return e.db.DraftEffects().MarkApplied(ctx, 1, noteAt) },
		},
		writeCase{
			method: "ExtractionStore.MarkClosed",
			call:   func(ctx context.Context, e *noteEnv) error { return e.db.Extraction().MarkClosed(ctx, closed, noteAt) },
			topics: port.TopicSourceEvents, units: []unit.Unit{closed},
		},
	)
}

func TestUnappliedAreTheSentDraftsThatDeclareEffects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	for _, c := range []struct {
		state   draft.State
		effects []string
	}{{draft.Sent, []string{"entry.nudged"}}, {draft.Sent, nil}, {draft.Proposed, []string{"unit.closed"}}, {draft.Sent, []string{"unit.closed"}}} {
		d := sampleDraft(slackThread, noteAt)
		d.State, d.Effects = c.state, c.effects
		if _, err := db.Drafts().Insert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.DraftEffects().Unapplied(ctx)
	if err != nil || len(got) != 2 || got[0].ID != 1 || got[1].ID != 4 {
		t.Fatalf("unapplied %+v %v", got, err)
	}
	if err := db.DraftEffects().MarkApplied(ctx, 1, noteAt); err != nil {
		t.Fatal(err)
	}
	if err := db.DraftEffects().MarkApplied(ctx, 1, noteAt.Add(1)); err != nil {
		t.Fatal(err)
	}
	if d, _ := db.Drafts().Get(ctx, 1); !d.EffectsAppliedAt.Equal(noteAt.UTC()) {
		t.Fatalf("applied at %v", d.EffectsAppliedAt)
	}
	if got, _ := db.DraftEffects().Unapplied(ctx); len(got) != 1 || got[0].ID != 4 {
		t.Fatalf("unapplied after the mark %+v", got)
	}
}

func TestAClosureMarkSurvivesExtractionPasses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	u := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/3.3"}
	x := db.Extraction()
	if err := x.Save(ctx, ExtractionSave{States: map[unit.Unit]ExtractionState{u: {LastTS: "1", At: noteAt}}}); err != nil {
		t.Fatal(err)
	}
	if err := x.MarkClosed(ctx, u, noteAt); err != nil {
		t.Fatal(err)
	}
	if err := x.Save(ctx, ExtractionSave{States: map[unit.Unit]ExtractionState{u: {LastTS: "2", ClosurePosted: false, At: noteAt}}}); err != nil {
		t.Fatal(err)
	}
	st, err := x.States(ctx, []unit.Unit{u})
	if err != nil || !st[u].ClosureMarked || st[u].ClosurePosted || st[u].LastTS != "2" {
		t.Fatalf("%+v %v", st[u], err)
	}
}
