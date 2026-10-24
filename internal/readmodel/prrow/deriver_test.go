package prrow

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

func TestRowFactsPutTheGateDeadlineInOrder(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	r := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	d := Deriver{Now: now, Loaded: bundled(t), Pending: map[unit.Unit]round.Pending{r.Unit(): {Intent: "follow-up", Deadline: now.Add(3 * time.Minute)}}}
	p := dinv.PR{Ref: r, State: "OPEN", Reasons: []string{"involved"}}
	_, pl := d.Parts(p, nil, dinv.Override{PR: r, MutedUntil: now.Add(time.Minute)})
	got := pl.Facts
	want := []api.Upcoming{{At: now.Add(time.Minute), What: api.UpcomingUnmute}, {At: now.Add(3 * time.Minute), What: api.UpcomingGate}}
	if got == nil || !slices.EqualFunc(got.Next, want, sameUpcoming) {
		t.Fatalf("%+v", got)
	}
	d.Pending[r.Unit()] = round.Pending{Deadline: now}
	if _, pl := d.Parts(p, nil, dinv.Override{PR: r}); pl.Facts.Next != nil {
		t.Fatalf("a deadline at now is not next: %+v", pl.Facts.Next)
	}
}

func sameUpcoming(a, b api.Upcoming) bool { return a.What == b.What && a.At.Equal(b.At) }

func TestDeriverAtStampsTheServedSpecsVersion(t *testing.T) {
	set := readmodel.RowSettings{Loaded: bundled(t)}
	if d := DeriverAt(readmodel.Base{SpecsVersion: "served"}, set, false, time.Time{}); d.SpecsVersion != "served" {
		t.Fatalf("stamps %q", d.SpecsVersion)
	}
	if d := DeriverAt(readmodel.Base{}, set, false, time.Time{}); d.SpecsVersion != catalog.VersionOf(set.Loaded) {
		t.Fatalf("with no served version, stamps %q", d.SpecsVersion)
	}
}
