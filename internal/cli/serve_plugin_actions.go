package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/wloophq/wloop/internal/api"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// pluginAction runs an action of running plugin id that names no unit.
func (e *Env) pluginAction(ctx context.Context, id string, req api.PluginActionRequest) (json.RawMessage, error) {
	if e.pluginKind == nil {
		return nil, fmt.Errorf("plugin %s: %w", id, src.ErrUnavailable)
	}
	c, err := e.pluginKind.Client(id)
	if err != nil {
		return nil, err
	}
	return c.Action(ctx, src.ActionRequest{Action: req.Action, Args: req.Args, Actor: req.Actor})
}

// pluginRun runs the command line args of running plugin id, in the
// plugin as it runs, writing as it goes.
func (e *Env) pluginRun(ctx context.Context, id string, args []string, stdout, stderr io.Writer) (int, error) {
	if e.pluginKind == nil {
		return 0, fmt.Errorf("plugin %s: %w", id, src.ErrUnavailable)
	}
	c, err := e.pluginKind.Client(id)
	if err != nil {
		return 0, err
	}
	return c.RunCLI(ctx, src.CLIRequest{Args: args}, stdout, stderr)
}
