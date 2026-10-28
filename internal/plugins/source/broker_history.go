package source

import (
	"context"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type history struct{ b *Broker }

func (history) Events(context.Context, src.EventQuery) ([]src.Event, error) {
	return nil, src.ErrUnsupported
}
