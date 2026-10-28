package source

import (
	"context"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type ledger struct{ b *Broker }

func (ledger) ApplyItems(context.Context, []src.ItemOp) ([]src.ItemResult, error) {
	return nil, src.ErrUnsupported
}

func (ledger) QueryItems(context.Context, src.ItemQuery) ([]src.Item, error) {
	return nil, src.ErrUnsupported
}
