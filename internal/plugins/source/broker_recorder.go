package source

import (
	"context"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type recorder struct{ b *Broker }

func (recorder) RecordEvents(context.Context, []src.Event) error { return src.ErrUnsupported }

func (recorder) SetMarks(context.Context, plugin.Unit, map[string]string) error {
	return src.ErrUnsupported
}

func (recorder) PutBrief(context.Context, src.Brief) error { return src.ErrUnsupported }
