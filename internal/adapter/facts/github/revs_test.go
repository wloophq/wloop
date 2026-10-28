package factsgithub_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheGitHubProviderReportsARevOnEveryInventoryChange(t *testing.T) {
	w := newWorld()
	a, b := w.requested(1, nil).Unit(), w.requested(2, nil).Unit()
	log := change.NewLog(change.DefaultWindow, nil)
	p := w.providerWith(log)
	ctx := context.Background()
	rev := func(u unit.Unit) uint64 {
		t.Helper()
		got, err := p.Revs(ctx, []unit.Unit{a, b})
		if err != nil {
			t.Fatal(err)
		}
		r, ok := got[u]
		if !ok {
			t.Fatalf("no rev of %s in %v", u, got)
		}
		return r
	}
	a0, b0 := rev(a), rev(b)
	log.Note(port.TopicPRs, a)
	if rev(a) == a0 || rev(b) != b0 {
		t.Fatalf("a row write moved a %d->%d, b %d->%d", a0, rev(a), b0, rev(b))
	}
	a1 := rev(a)
	log.Note(port.TopicSourceEvents, a)
	if rev(a) == a1 {
		t.Fatal("a timeline event kept a's rev")
	}
	a2 := rev(a)
	log.Note(port.TopicTurns, a)
	if rev(a) != a2 {
		t.Fatal("a turn, not inventory, moved a's rev")
	}
	b1 := rev(b)
	log.NoteAll(port.TopicFollow)
	if rev(a) == a2 || rev(b) == b1 {
		t.Fatal("a note of every unit kept a rev")
	}
	if got, _ := p.Revs(ctx, []unit.Unit{{Provider: "slack", Key: "T1/C1/1.1"}}); len(got) != 0 {
		t.Fatalf("revs of another provider's unit: %v", got)
	}
}
