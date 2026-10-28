package ghreporttest

import (
	"context"
	"encoding/json"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghplugin "github.com/wloophq/wloop/plugins/github"
)

// Prepare is the GitHub plugin's nudge prepare as the running plugin serves
// the presenter: over host, which serves the unit's events and turns, for
// the viewer viewer names now, who never waits on whom ignore names.
func Prepare(host func() src.Host, viewer func(ctx context.Context) (string, error), ignore func() []string, now func() time.Time) func(context.Context, src.ActionRequest) (json.RawMessage, error) {
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
		v, err := viewer(ctx)
		if err != nil {
			return nil, err
		}
		return ghplugin.PrepareNudge(ctx, host(), ghplugin.NudgeEnv{Viewer: v, Ignore: ignore(), Now: now()}, req.Unit, req.Args)
	}
}
