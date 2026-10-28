package source

import (
	"context"
	"errors"
	"fmt"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type ledger struct{ b *Broker }

// ApplyItems applies the plugin's ops to the host's ledger as extracted:
// an op on another provider's unit is refused, and a link to a unit with no
// provider is the address the host resolves with every plugin's codec.
func (l ledger) ApplyItems(ctx context.Context, ops []src.ItemOp) ([]src.ItemResult, error) {
	h := l.b.Hosted
	if h == nil || h.Items == nil {
		return nil, src.ErrUnsupported
	}
	out := make([]src.ItemResult, len(ops))
	var kept []items.Op
	var at []int
	for i, w := range ops {
		o, err := l.op(ctx, w)
		if err != nil {
			out[i].Error = err.Error()
			continue
		}
		out[i].Item = o.Item
		kept = append(kept, o)
		at = append(at, i)
	}
	if len(kept) == 0 {
		return out, nil
	}
	res, err := h.Items.Apply(ctx, items.Extracted, kept)
	if err != nil {
		return nil, err
	}
	for _, r := range res.Refused {
		if r.Index >= 0 && r.Index < len(at) {
			out[at[r.Index]].Error = r.Reason
		}
	}
	return out, nil
}

func (l ledger) op(ctx context.Context, w src.ItemOp) (items.Op, error) {
	o := opOf(w)
	u, err := l.b.Units.Own(l.b.ID, w.Unit)
	if err != nil {
		return o, err
	}
	o.Unit = u
	if !o.Anchor.Unit.IsZero() {
		if o.Anchor.Unit, err = l.b.Units.Own(l.b.ID, w.Anchor.Unit); err != nil {
			return o, err
		}
	}
	if o.Evidence != nil && !o.Evidence.Anchor.Unit.IsZero() {
		if o.Evidence.Anchor.Unit, err = l.b.Units.Own(l.b.ID, w.Evidence.Anchor.Unit); err != nil {
			return o, err
		}
	}
	if w.To == nil {
		return o, nil
	}
	to, err := l.to(ctx, *o.To)
	if err != nil {
		return o, err
	}
	o.To = &to
	return o, nil
}

// to is the unit a link op names: a unit of a known provider, or, with no
// provider, the unit its key's address lands in.
func (l ledger) to(ctx context.Context, u unit.Unit) (unit.Unit, error) {
	if u.Provider != "" {
		return l.b.Units.Of(Wire(u))
	}
	if l.b.Hosted.Resolve != nil {
		if got, ok, err := l.b.Hosted.Resolve(ctx, u.Key); err != nil || ok {
			return got, err
		}
	}
	if got, ok := l.b.Units.Codecs.ParseURL(u.Key); ok {
		return got, nil
	}
	return unit.Unit{}, fmt.Errorf("the link target %q names no unit", u.Key)
}

// QueryItems reads the items of the plugin's units; no units reads every
// unit of the plugin's.
func (l ledger) QueryItems(ctx context.Context, q src.ItemQuery) ([]src.Item, error) {
	h := l.b.Hosted
	if h == nil || h.Items == nil {
		return nil, src.ErrUnsupported
	}
	cq := coreitems.Query{}
	if q.OpenOnly {
		cq.State = items.Open
	}
	if len(q.Units) == 0 {
		cq.Providers = []unit.ProviderID{unit.ProviderID(l.b.ID)}
	}
	var errs []error
	for _, w := range q.Units {
		u, err := l.b.Units.Own(l.b.ID, w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cq.Units = append(cq.Units, u)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	got, err := h.Items.Query(ctx, cq)
	if err != nil {
		return nil, err
	}
	out := make([]src.Item, len(got))
	for i, it := range got {
		out[i] = wireItem(it)
	}
	return out, nil
}

func coreitemsQuery(u unit.Unit) coreitems.Query { return coreitems.Query{Units: []unit.Unit{u}} }
