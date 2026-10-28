package inventory

import (
	"context"

	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// ByUnit is the store as the daemon's inventory roles read it, keyed by
// unit; a unit that is no pull request reads as the zero reference.
type ByUnit struct{ *Store }

func (s ByUnit) PRs(ctx context.Context, units []unit.Unit) (map[unit.Unit]dinv.PR, error) {
	got, err := s.Store.PRs(ctx, unitRefs(units))
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PR, len(got))
	for ref, p := range got {
		out[ref.Unit()] = p
	}
	return out, nil
}

func (s ByUnit) Overrides(ctx context.Context) (map[unit.Unit]dinv.Override, error) {
	got, err := s.Store.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.Override, len(got))
	for ref, o := range got {
		out[ref.Unit()] = o
	}
	return out, nil
}

func unitRef(u unit.Unit) github.Ref {
	ref, _ := pr.RefOf(u)
	return ref
}

func unitRefs(units []unit.Unit) []github.Ref {
	refs := make([]github.Ref, len(units))
	for i, u := range units {
		refs[i] = unitRef(u)
	}
	return refs
}

// RecordEvent records ev on its unit's pull request, none for another unit.
func (s ByUnit) RecordEvent(ctx context.Context, ev port.Event) error {
	return s.Store.RecordEvent(ctx, dinv.Event{PR: unitRef(ev.Unit), Kind: ev.Kind, Key: ev.Key, Actor: ev.Actor, At: ev.At, Data: ev.Data})
}

func (s ByUnit) UnitlessEvents(ctx context.Context) ([]port.Event, error) {
	evs, err := s.Store.UnitlessEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.Event, len(evs))
	for i, e := range evs {
		out[i] = portEvent(e)
	}
	return out, nil
}

// RecentActivity is the activity feed, a pull request's address its URL.
func (s ByUnit) RecentActivity(ctx context.Context, limit int) ([]port.Activity, error) {
	got, err := s.Store.RecentActivity(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]port.Activity, len(got))
	for i, a := range got {
		ev := portEvent(a.Event)
		ev.Unit = a.Unit
		url := a.URL
		if url == "" && a.PR.Number != 0 {
			url = a.PR.URL()
		}
		out[i] = port.Activity{Event: ev, Title: a.Title, URL: url, Author: a.Author, State: a.State, Mine: a.Mine}
	}
	return out, nil
}

func (s ByUnit) Turn(ctx context.Context, id string) (port.SentText, bool, error) {
	t, ok, err := s.Store.Turn(ctx, id)
	return port.SentText{ID: t.ID, Kind: t.Kind, Repo: t.Repo, Text: t.Text, Sources: t.Sources, BundledSHA: t.BundledSHA}, ok, err
}

func portEvent(e dinv.Event) port.Event {
	return port.Event{Unit: e.PR.Unit(), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: e.Data}
}
