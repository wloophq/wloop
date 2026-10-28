// Package fakesource is a source plugin's reports of chat threads, for
// tests: each thread is a snapshot, served as rows through the host's
// readmodel.Reported, as a plugin's units are. Nothing registers it.
package fakesource

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

const ID unit.ProviderID = "thread"

// Codec accepts any non-blank one-line key.
var Codec = unit.OpaqueCodec{ID: ID}

// The kinds of a thread.
const (
	KindWaiting  api.Kind = "waiting"
	KindAnswered api.Kind = "answered"
)

// FacetChannel is the thread plugin's own facet.
const FacetChannel = "channel"

// Thread is one thread as its plugin knows it.
type Thread struct {
	Key, Channel, Title string
	Kind                api.Kind
	WaitsOn, Reason     string
	Since               time.Time
	Stalled             bool
}

// Unit is the thread's unit.
func (t Thread) Unit() unit.Unit { return unit.Unit{Provider: ID, Key: t.Key} }

// Snapshot is the thread as its plugin reports it.
func (t Thread) Snapshot() src.Snapshot {
	s := src.Snapshot{Unit: plugin.Unit{Provider: string(ID), Key: t.Key}, Rev: 1,
		Situation: &src.Situation{Kind: string(t.Kind), WaitingOn: t.WaitsOn, Since: t.Since, Reason: t.Reason, Stalled: src.Bool(t.Stalled)},
		Card:      &src.Card{Label: "#" + t.Channel, Title: t.Title, Link: "https://chat.example/" + t.Key},
		Facets:    map[string]string{FacetChannel: t.Channel}, Views: []string{}}
	if t.WaitsOn == "me" {
		s.Views = append(s.Views, api.ViewNeedsMe)
	}
	return s
}

// Source holds the threads' last reports.
type Source struct {
	mu    sync.Mutex
	snaps map[unit.Unit]src.Snapshot
}

var _ readmodel.Reports = (*Source)(nil)

// New is a Source that reported threads.
func New(threads ...Thread) *Source {
	s := &Source{snaps: map[unit.Unit]src.Snapshot{}}
	for _, t := range threads {
		s.Set(t)
	}
	return s
}

// Set reports t again, replacing its last report.
func (s *Source) Set(t Thread) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := t.Snapshot()
	if old, ok := s.snaps[t.Unit()]; ok {
		snap.Rev = old.Rev + 1
	}
	s.snaps[t.Unit()] = snap
}

func (s *Source) Units(_ context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []unit.Unit
	for _, u := range slices.SortedFunc(maps.Keys(s.snaps), api.CompareUnits) {
		if u.Provider == p {
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *Source) Reported(_ context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[unit.Unit]src.Snapshot{}
	for _, u := range units {
		if snap, ok := s.snaps[u]; ok {
			out[u] = snap
		}
	}
	return out, nil
}

// Rows serves the threads as the host serves a plugin's units.
func (s *Source) Rows() *readmodel.Reported {
	return readmodel.NewReported(readmodel.ReportedSource{ID: ID, Reports: s, Catalog: presenter{}})
}

// Row is t's row, as the host derives it from t's snapshot.
func Row(t Thread) api.Row {
	p := New(t).Rows()
	ins, err := p.Load(context.Background(), []unit.Unit{t.Unit()})
	if err != nil {
		panic(err)
	}
	r, err := p.Derive(readmodel.Base{}, ins[t.Unit()], t.Since)
	if err != nil {
		panic(err)
	}
	r.Unit = t.Unit()
	return r
}

type presenter struct{}

func (presenter) Views() []api.ViewSpec { return nil }

func (presenter) Facets() []api.FacetSpec {
	return []api.FacetSpec{{ID: FacetChannel, Label: "Channel", Provider: string(ID), Group: true, Sort: "text", Menu: true}}
}

func (presenter) Situations() []api.SituationSpec {
	return []api.SituationSpec{
		{Provider: string(ID), Kind: KindWaiting, Label: "waiting", Rank: 0},
		{Provider: string(ID), Kind: KindAnswered, Label: "answered", Rank: 1},
	}
}
