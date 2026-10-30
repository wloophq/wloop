package ghreporttest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"time"

	"github.com/wloophq/wloop/plugins"
	"github.com/wloophq/wloop/plugins/github/nudge"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghplugin "github.com/wloophq/wloop/plugins/github"
)

// Prepare is the GitHub plugin's nudge prepare as the running plugin serves
// the presenter: over host, which serves the unit's events and turns, for
// the viewer viewer names now, who never waits on whom ignore names.
func Prepare(host func() src.Host, viewer func(ctx context.Context) (string, error), ignore func() []string, now func() time.Time) func(context.Context, src.ActionRequest) (json.RawMessage, error) {
	return func(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
		v, err := viewer(ctx)
		if err != nil {
			return nil, err
		}
		return ghplugin.PrepareNudge(ctx, host(), ghplugin.NudgeEnv{Viewer: v, Ignore: ignore(), Now: now(), Texts: NudgeTexts()}, req.Unit, req.Args)
	}
}

// Profile reads path in the GitHub plugin's bundled profile layer.
func Profile(_ context.Context, path string) ([]byte, error) {
	return fs.ReadFile(plugins.Profiles, "github/profile/"+path)
}

// NudgeTexts are the nudge's texts in the GitHub plugin's bundled profile
// layer.
func NudgeTexts() nudge.Texts {
	raw, err := Profile(context.Background(), nudge.TextsFile)
	if err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	x, err := nudge.ParseTexts(string(raw))
	if err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	return x
}
