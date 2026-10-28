package source

import (
	"context"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type reader struct{ b *Broker }

func (reader) Tasks(context.Context, []plugin.Unit) ([]src.Task, error) {
	return nil, src.ErrUnsupported
}

func (reader) Books(context.Context, []plugin.Unit) ([]src.Book, error) {
	return nil, src.ErrUnsupported
}

func (reader) RoundState(context.Context, plugin.Unit) (src.RoundState, error) {
	return src.RoundState{}, src.ErrUnsupported
}

func (reader) Workspaces(context.Context, string) (src.Listing, error) {
	return src.Listing{}, src.ErrUnsupported
}

func (reader) Session(context.Context, string) (src.Session, error) {
	return src.Session{}, src.ErrUnsupported
}
