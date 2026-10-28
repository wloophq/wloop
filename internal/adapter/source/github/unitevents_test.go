package ghsource

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

func TestGitHubUnitEvents(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 527}
	other := github.Ref{Owner: "acme", Repo: "api", Number: 528}
	at := func(h int) time.Time { return now.Add(time.Duration(h) * time.Hour) }
	if _, err := st.AppendEvents(ctx, []dinv.Event{
		{PR: ref, Kind: dinv.KindReview, Key: "r0", Actor: "early", At: at(-1), Data: "APPROVED:h1"},
		{PR: ref, Kind: dinv.KindComment, Key: "c1", Actor: "ana", At: at(1)},
		{PR: ref, Kind: dinv.KindMerged, Actor: "ana", At: at(4), Data: "m1"},
		{PR: ref, Kind: dinv.KindReview, Key: "r1", Actor: "OctoCat", At: at(2), Data: "APPROVED:h1"},
		{PR: ref, Kind: dinv.KindReview, Key: "r1:DISMISSED", Actor: "OctoCat", At: at(3), Data: "DISMISSED:h1"},
		{PR: ref, Kind: dinv.KindClosed, At: at(5)},
		{PR: other, Kind: dinv.KindReview, Key: "r9", Actor: "octocat", At: at(2), Data: "APPROVED:h1"},
	}); err != nil {
		t.Fatal(err)
	}
	u := ref.Unit()
	got, err := unitEvents{st}.UnitEvents(ctx, u, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []link.Event{
		{Unit: u, Kind: link.EventReview, Actor: "github:OctoCat", At: at(2), Anchor: unit.Anchor{Unit: u, Ref: "review:r1", At: at(2)}},
		{Unit: u, Kind: link.EventMerged, Actor: "github:ana", At: at(4), Anchor: unit.Anchor{Unit: u, Ref: "merged", At: at(4)}},
		{Unit: u, Kind: link.EventClosed, At: at(5), Anchor: unit.Anchor{Unit: u, Ref: "closed", At: at(5)}},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v", got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Unit != w.Unit || g.Kind != w.Kind || g.Actor != w.Actor || !g.At.Equal(w.At) || g.Anchor.Ref != w.Anchor.Ref ||
			g.Anchor.Unit != w.Anchor.Unit || !g.Anchor.At.Equal(w.Anchor.At) {
			t.Fatalf("event %d = %+v, want %+v", i, g, w)
		}
	}
	outside := github.Ref{Owner: "acme", Repo: "api", Number: 999}.Unit()
	if got, err := (unitEvents{st}).UnitEvents(ctx, outside, time.Time{}); err != nil || got != nil {
		t.Fatalf("a pull request outside the inventory = %v, %v", got, err)
	}
	if got, err := (unitEvents{st}).UnitEvents(ctx, unit.Unit{Provider: "slack", Key: "C1/1"}, now); err == nil || got != nil {
		t.Fatalf("a slack unit = %v, %v", got, err)
	}
}

func TestTheModuleServesItsUnitEvents(t *testing.T) {
	m, err := Module(config.Defaults(), sourcereg.Deps{Inventory: openStore(t), Clock: stopped{}, Log: t.Logf})
	if err != nil || m.UnitEvents == nil {
		t.Fatalf("module: UnitEvents %v, %v", m.UnitEvents, err)
	}
}

type stopped struct{}

func (stopped) Now() time.Time { return now }
