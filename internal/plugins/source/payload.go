package source

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/source/value"
	"github.com/wloophq/wloop/internal/turn"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Payloads is a source plugin's Planner.Payload as a turn reads it.
type Payloads struct {
	ID   string
	Kind *Kind
	Now  func() time.Time
}

var _ turn.PayloadReader = Payloads{}

func (p Payloads) Payload(ctx context.Context, r turn.Request) (turn.Payload, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return nil, err
	}
	inputs, err := json.Marshal(r.Inputs)
	if err != nil {
		return nil, err
	}
	req := src.PayloadRequest{Unit: Wire(r.Unit), Intent: string(r.Intent), Trigger: r.Trigger.String(), Inputs: inputs,
		Guidance: r.Guidance, Local: r.Local, Owned: r.Owned, Plain: r.Plain, Round: r.Planned.String(), Agent: r.Agent,
		BriefMissing: r.BriefMissing, Items: r.Items, Task: r.Task}
	if v := r.LastVerdict; v != nil {
		req.LastVerdict = &src.Verdict{Head: v.Head, Endorsed: v.Endorsed, At: v.At}
	}
	res, err := c.Payload(ctx, req)
	if err != nil {
		return nil, err
	}
	facts := make(map[string]any, len(res.Facts))
	now := p.Now()
	for _, f := range res.Facts {
		facts[f.Name] = value.Resolve(f.Value, now)
	}
	return payload{unit: r.Unit, res: res, facts: facts}, nil
}

// payload is a plugin's PayloadResult as a turn's instructions read it.
type payload struct {
	unit  unit.Unit
	res   src.PayloadResult
	facts map[string]any
}

var _ turn.Contents = payload{}

func (p payload) Unit() unit.Unit { return p.unit }

func (p payload) Arg() string { return p.res.Arg }

func (p payload) Wording() turn.Wording {
	w := p.res.Wording
	return turn.Wording{Noun: w.Noun, This: w.This, Ref: w.Ref, Short: w.Short}
}

func (p payload) Fact(name string) (any, bool) {
	v, ok := p.facts[name]
	return v, ok
}

func (p payload) Contents() string { return p.res.Contents }
