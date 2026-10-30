package factsgithub

import (
	"context"
	"sync"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// inventoryTopics are the notes of a change to a pull request's stored data.
const inventoryTopics = port.TopicPRs | port.TopicFollow | port.TopicSourceEvents

var _ automation.Revisioner = (*Provider)(nil)

// revs counts the inventory changes the change log noted of each pull
// request, read up to gen; base counts the notes of every unit at once.
type revs struct {
	mu     sync.Mutex
	gen    port.Generation
	base   uint64
	byUnit map[unit.Unit]uint64
}

// Revs are the revs of units' pull requests, each grown by every inventory
// change noted of it since the provider was built; none without a change
// log.
func (p *Provider) Revs(_ context.Context, units []unit.Unit) (map[unit.Unit]uint64, error) {
	if p.d.Changes == nil {
		return nil, nil
	}
	r := &p.revs
	r.mu.Lock()
	defer r.mu.Unlock()
	c := p.d.Changes.Since(r.gen, inventoryTopics)
	r.gen = c.Gen
	if c.All {
		r.base++
	}
	for _, u := range c.Units {
		if u.Provider == unit.GitHub {
			r.byUnit[u]++
		}
	}
	out := map[unit.Unit]uint64{}
	for _, u := range units {
		if u.Provider == unit.GitHub {
			out[u] = r.base + r.byUnit[u]
		}
	}
	return out, nil
}
