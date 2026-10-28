package source

import (
	"context"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type gates struct{ b *Broker }

func (gates) OpenGate(context.Context, src.GateRequest) (src.GateRef, error) {
	return src.GateRef{}, src.ErrUnsupported
}

func (gates) RecordSent(context.Context, src.Draft, string, string) (src.GateRef, error) {
	return src.GateRef{}, src.ErrUnsupported
}

func (gates) RehomeDrafts(context.Context, []src.UnitMove) (int, error) { return 0, src.ErrUnsupported }

func (gates) SetOverride(context.Context, plugin.Unit, map[string]string, map[string]string) (bool, error) {
	return false, src.ErrUnsupported
}
