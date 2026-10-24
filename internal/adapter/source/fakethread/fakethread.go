// Package fakethread is a provider of chat threads for tests: a unit codec
// and a rows provider whose rows carry no payload. Nothing registers it.
package fakethread

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

const ID unit.ProviderID = "thread"

// Codec accepts any non-blank one-line key.
var Codec = unit.OpaqueCodec{ID: ID}

// The kinds of a thread.
const (
	KindWaiting  api.Kind = "waiting"
	KindAnswered api.Kind = "answered"
)

// FacetChannel is the thread provider's own facet.
const FacetChannel = "channel"

// Thread is one thread's row input.
type Thread struct {
	Key, Channel, Title string
	Kind                api.Kind
	WaitsOn, Reason     string
	Since               time.Time
	Stalled             bool
}

// Unit is the thread's unit.
func (t Thread) Unit() unit.Unit { return unit.Unit{Provider: ID, Key: t.Key} }

// Provider serves a row per thread it holds.
type Provider struct {
	mu      sync.Mutex
	threads map[string]Thread
}

var (
	_ readmodel.Provider  = (*Provider)(nil)
	_ readmodel.Presenter = (*Provider)(nil)
)

func New(threads ...Thread) *Provider {
	p := &Provider{threads: map[string]Thread{}}
	for _, t := range threads {
		p.Set(t)
	}
	return p
}

// Set adds or replaces a thread.
func (p *Provider) Set(t Thread) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.threads[t.Key] = t
}

func (p *Provider) Provider() unit.ProviderID { return ID }

func (p *Provider) Listed(context.Context) ([]unit.Unit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []unit.Unit
	for _, k := range slices.Sorted(maps.Keys(p.threads)) {
		out = append(out, p.threads[k].Unit())
	}
	return out, nil
}

func (p *Provider) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[unit.Unit]any{}
	for _, u := range units {
		if t, ok := p.threads[u.Key]; ok {
			out[u] = t
		}
	}
	return out, nil
}

// Row is the thread's row.
func Row(t Thread) api.Row {
	r := api.Row{Unit: t.Unit(), Link: "https://chat.example/" + t.Key, Label: "#" + t.Channel, Title: t.Title, Kind: t.Kind,
		Summary: api.Summary{WaitsOn: t.WaitsOn, Since: t.Since, Stalled: t.Stalled, Reason: t.Reason, Line: t.Reason},
		Views:   []string{}, Facets: map[string][]string{FacetChannel: {t.Channel}}, Actions: []api.ActionState{}}
	if t.WaitsOn == "me" {
		r.Views = append(r.Views, api.ViewNeedsMe)
	}
	return r
}

func (p *Provider) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	t, ok := in.(Thread)
	if !ok {
		return api.Row{}, fmt.Errorf("fakethread: input %T", in)
	}
	return Row(t), nil
}

func (p *Provider) Views() []api.ViewSpec { return nil }

func (p *Provider) Facets() []api.FacetSpec {
	return []api.FacetSpec{{ID: FacetChannel, Label: "Channel", Provider: string(ID), Group: true, Sort: "text", Menu: true}}
}

func (p *Provider) Situations() []api.SituationSpec {
	return []api.SituationSpec{
		{Provider: string(ID), Kind: KindWaiting, Label: "waiting", Rank: 0},
		{Provider: string(ID), Kind: KindAnswered, Label: "answered", Rank: 1},
	}
}
