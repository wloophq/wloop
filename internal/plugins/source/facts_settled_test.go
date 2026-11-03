package source_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// listedDrafts are drafts listed as the store filters them, counting the lists.
type listedDrafts struct {
	ds    []draft.Draft
	lists int
}

func (l *listedDrafts) List(_ context.Context, f outbox.Filter) ([]draft.Draft, error) {
	l.lists++
	var out []draft.Draft
	for _, d := range l.ds {
		if f.Unit != nil && d.Unit != *f.Unit || f.Workflow != "" && d.Origin.Workflow != f.Workflow ||
			len(f.States) > 0 && !slices.Contains(f.States, d.State) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (l *listedDrafts) SettledAt(ctx context.Context, wf string) (map[unit.Unit]time.Time, error) {
	ds, err := l.List(ctx, outbox.Filter{Workflow: wf, States: draft.SettledStates})
	out := map[unit.Unit]time.Time{}
	for _, d := range ds {
		for _, u := range draft.Subjects(d) {
			if d.UpdatedAt.After(out[u]) {
				out[u] = d.UpdatedAt
			}
		}
	}
	return out, err
}

func TestSettledAtIsTheSameInAPassWhichReadsAWorkflowsSettledDraftsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	a, b, c := unit.Unit{Provider: "acme", Key: "a"}, unit.Unit{Provider: "acme", Key: "b"}, unit.Unit{Provider: "acme", Key: "c"}
	put := func(u unit.Unit, wf string, s draft.State, at time.Time) draft.Draft {
		return draft.Draft{Unit: u, State: s, UpdatedAt: at, Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: wf}}
	}
	drafts := &listedDrafts{ds: []draft.Draft{
		put(a, "nudge", draft.Sent, at), put(a, "nudge", draft.Dropped, at.Add(time.Hour)), put(a, "nudge", draft.Held, at.Add(2*time.Hour)),
		put(b, "nudge", draft.Sent, at.Add(3*time.Hour)), put(b, "close", draft.Sent, at.Add(4*time.Hour)),
	}}
	f := psource.SettledFacts{Drafts: drafts, Source: &manifest.Source{SourceVocabulary: manifest.SourceVocabulary{
		Slot: "acme", Facts: []manifest.Fact{{Name: "acme." + psource.SettledFact}}}}}
	want := map[unit.Unit]time.Time{a: at.Add(time.Hour), b: at.Add(3 * time.Hour), c: {}}
	settled := func(ctx context.Context, u unit.Unit) time.Time {
		t.Helper()
		got, err := f.Scope(ctx, "nudge", u, workflow.Facts{})
		if err != nil {
			t.Fatal(err)
		}
		return got["acme.settled_at"].(time.Time)
	}
	pass := coretasks.WithPass(ctx, &coretasks.Pass{})
	for u, w := range want {
		if got := settled(ctx, u); !got.Equal(w) {
			t.Errorf("%s alone: settled_at %v, want %v", u, got, w)
		}
	}
	drafts.lists = 0
	for u, w := range want {
		if got := settled(pass, u); !got.Equal(w) {
			t.Errorf("%s in a pass: settled_at %v, want %v", u, got, w)
		}
	}
	if drafts.lists != 1 {
		t.Errorf("a pass read the workflow's settled drafts %d times for %d units", drafts.lists, len(want))
	}
}
