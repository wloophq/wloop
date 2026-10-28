package source

import (
	"context"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type wakes struct{ b *Broker }

func (wakes) SetWake(context.Context, src.Wake) error { return src.ErrUnsupported }

func (wakes) ClearWake(context.Context, plugin.Unit, string) error { return src.ErrUnsupported }
