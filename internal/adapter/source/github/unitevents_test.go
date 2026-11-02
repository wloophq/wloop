package ghsource

import (
	"context"
	"errors"
	"testing"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
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
	reported := prStates{ref: {Ref: ref, State: "CLOSED", ClosedAt: at(6)}}
	got, err := UnitEvents{Store: st, PRs: reported}.UnitEvents(ctx, u, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []link.Event{
		{Unit: u, Kind: link.EventReview, Actor: "github:OctoCat", At: at(2), Anchor: unit.Anchor{Unit: u, Ref: "review:r1", At: at(2)}},
		{Unit: u, Kind: link.EventMerged, Actor: "github:ana", At: at(4), Anchor: unit.Anchor{Unit: u, Ref: "merged", At: at(4)}},
		{Unit: u, Kind: link.EventClosed, At: at(6), Anchor: unit.Anchor{Unit: u, Ref: "closed", At: at(6)}},
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
	if got, err := (UnitEvents{Store: st, PRs: reported}).UnitEvents(ctx, outside, time.Time{}); err != nil || got != nil {
		t.Fatalf("a pull request outside the inventory = %v, %v", got, err)
	}
	if got, err := (UnitEvents{Store: st, PRs: reported}).UnitEvents(ctx, unit.Unit{Provider: "slack", Key: "C1/1"}, now); err == nil || got != nil {
		t.Fatalf("a slack unit = %v, %v", got, err)
	}
}

type prStates map[pr.Ref]dinv.PR

func (s prStates) PR(_ context.Context, ref pr.Ref) (dinv.PR, bool, error) {
	p, ok := s[ref]
	return p, ok, nil
}

func TestAPullRequestOpenAgainHasNoClose(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 527}
	if _, err := st.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindClosed, At: now.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	for name, states := range map[string]prStates{
		"reopened":   {ref: {Ref: ref, State: "OPEN", ClosedAt: now.Add(time.Hour)}},
		"unreported": {},
	} {
		got, err := UnitEvents{Store: st, PRs: states}.UnitEvents(ctx, ref.Unit(), time.Time{})
		if err != nil || len(got) != 0 {
			t.Fatalf("%s: events = %+v, %v", name, got, err)
		}
	}
}

func TestRefresherReadsAPullRequestOnlyWhenItsReportMayBeStale(t *testing.T) {
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 527}
	cases := []struct {
		name string
		pr   *dinv.PR
		read bool
	}{
		{"never reported", nil, true},
		{"open, seen an hour ago", &dinv.PR{Ref: ref, State: "OPEN", LastSeenAt: now.Add(-time.Hour)}, true},
		{"open, seen a minute ago", &dinv.PR{Ref: ref, State: "OPEN", LastSeenAt: now.Add(-time.Minute)}, false},
		{"closed, seen an hour ago", &dinv.PR{Ref: ref, State: "CLOSED", LastSeenAt: now.Add(-time.Hour)}, true},
		{"merged long ago", &dinv.PR{Ref: ref, State: "MERGED", LastSeenAt: now.Add(-30 * 24 * time.Hour)}, false},
	}
	for _, c := range cases {
		states := prStates{}
		if c.pr != nil {
			states[ref] = *c.pr
		}
		var reads []pr.Ref
		r := Refresher{PRs: states, Every: time.Hour, Now: func() time.Time { return now },
			Read: func(_ context.Context, got pr.Ref) error { reads = append(reads, got); return nil }}
		read, err := r.Refresh(ctx, ref.Unit())
		if err != nil || read != c.read || len(reads) != map[bool]int{true: 1}[c.read] {
			t.Fatalf("%s: read = %v, %v, reads %v", c.name, read, err, reads)
		}
	}
	r := Refresher{PRs: prStates{}, Every: time.Hour, Now: func() time.Time { return now },
		Read: func(context.Context, pr.Ref) error { return errors.New("rate limited") }}
	if _, err := r.Refresh(ctx, ref.Unit()); err == nil {
		t.Fatal("a failed read is no error")
	}
	if _, err := r.Refresh(ctx, unit.Unit{Provider: "slack", Key: "C1/1"}); err == nil {
		t.Fatal("a slack unit read as a pull request")
	}
}
