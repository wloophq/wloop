package source

import (
	"context"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// Publisher is a source plugin's Publisher and Lander as the outbox reads
// them: port.Effects and its optional capabilities.
type Publisher struct {
	ID     string
	Source *manifest.Source
	Units  Units
	Kind   *Kind
}

var (
	_ port.Effects   = Publisher{}
	_ port.Validator = Publisher{}
	_ port.Locator   = Publisher{}
	_ port.Placer    = Publisher{}
	_ port.Lander    = Publisher{}
	_ port.Deliverer = Publisher{}
)

func (p Publisher) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return draft.Evidence{}, err
	}
	ev, err := c.Publish(ctx, WireDraft(d))
	return draft.Evidence(ev), err
}

func (p Publisher) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return draft.Evidence{}, false, err
	}
	ev, ok, err := c.Verify(ctx, WireDraft(d), startedAt)
	return draft.Evidence(ev), ok, err
}

func (p Publisher) Validate(ctx context.Context, d draft.Draft) error {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return err
	}
	return c.Validate(ctx, WireDraft(d))
}

func (p Publisher) Where(ctx context.Context, d draft.Draft) (string, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return "", err
	}
	return c.Where(ctx, WireDraft(d))
}

func (p Publisher) Place(ctx context.Context, d draft.Draft) (string, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return "", err
	}
	return c.Place(ctx, WireDraft(d))
}

func (p Publisher) Landed(ctx context.Context, d draft.Draft) (unit.Unit, bool, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return unit.Unit{}, false, err
	}
	u, ok, err := c.Landed(ctx, WireDraft(d))
	if err != nil || !ok {
		return unit.Unit{}, ok, err
	}
	out, err := p.Units.Of(u)
	return out, err == nil, err
}

// Delivers is the delivery the manifest's draft kind k names, else its
// default delivery: a plugin posts every draft as one of its own
// deliveries. A manifest with neither posts k as its shape says.
func (p Publisher) Delivers(k draft.Kind) draft.Delivery {
	if i := slices.IndexFunc(p.Source.DraftKinds, func(m manifest.DraftKind) bool { return m.Name == string(k) }); i >= 0 && p.Source.DraftKinds[i].Delivery != "" {
		return draft.Delivery(p.Source.DraftKinds[i].Delivery)
	}
	if p.Source.DefaultDelivery != "" {
		return draft.Delivery(p.Source.DefaultDelivery)
	}
	_, d := draft.ShapeOf(k)
	return d
}
