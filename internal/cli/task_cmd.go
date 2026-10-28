package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

var taskUsage = "usage: " + brand.Command + " task attach task:<id>"

func runTask(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 || args[0] != "attach" {
		return errors.New(taskUsage)
	}
	return runTaskAttach(ctx, e, args[1:])
}

// runTaskAttach makes the agent session the command runs in a run of the
// task: the daemon starts the attach turn on it by hand, and its
// instructions go to standard output.
func runTaskAttach(ctx context.Context, e *Env, args []string) error {
	if len(args) != 1 {
		return errors.New(taskUsage)
	}
	u, ok, err := explicitTask(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s names no task\n%s", args[0], taskUsage)
	}
	handle, pane, agentSession, home := e.caller()
	if handle == "" {
		cfg, err := e.loadConfig()
		if err != nil {
			return err
		}
		text, err := profileNotices{e}.Notice(builtin.NoticeAttachNeedsSession, map[string]any{
			"Command": brand.Command, "Task": args[0], "Managers": e.displayOf(cfg.WorkspaceManagers)})
		if err != nil {
			return err
		}
		return errors.New(text)
	}
	var cwd string
	if e.Harness.Cwd != nil {
		cwd = e.Harness.Cwd()
	}
	if cwd == "" {
		return errors.New("task attach cannot read its working directory")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	res, err := c.Attach(ctx, handle, u.Key, api.AttachRequest{Cwd: cwd, Pane: pane, AgentSession: agentSession, Home: home})
	for _, n := range res.Notes {
		fmt.Fprintln(e.Err, n)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(e.Out, res.Text)
	return err
}
