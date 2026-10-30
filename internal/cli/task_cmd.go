package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

var taskUsage = "usage: " + brand.Command + " task new --title <title> [--brief-file <path>] [--prop <name>=<value>]... [--repo <owner/name>]\n" +
	"       " + brand.Command + " task attach task:<id>"

func runTask(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 {
		return errors.New(taskUsage)
	}
	switch args[0] {
	case "new":
		return runTaskNew(ctx, e, args[1:])
	case "attach":
		return runTaskAttach(ctx, e, args[1:])
	}
	return errors.New(taskUsage)
}

// runTaskNew creates a task with a title, the brief a file holds, the properties the profile declares, and
// the repository its work runs in, and prints it as task:<id>.
func runTaskNew(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("task new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var title, briefFile, repo string
	var props listFlag
	fs.StringVar(&title, "title", "", "")
	fs.StringVar(&briefFile, "brief-file", "", "")
	fs.Var(&props, "prop", "")
	fs.StringVar(&repo, "repo", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError{usage: taskUsage, why: err.Error()}
	}
	if fs.NArg() > 0 || strings.TrimSpace(title) == "" {
		return usageError{usage: taskUsage}
	}
	req := api.TaskCreate{Title: title, Placement: repo}
	if briefFile != "" {
		raw, err := os.ReadFile(briefFile)
		if err != nil {
			return fmt.Errorf("--brief-file: %w", err)
		}
		req.Brief = string(raw)
	}
	for _, p := range props {
		name, value, ok := strings.Cut(p, "=")
		if !ok || name == "" {
			return usageError{usage: taskUsage, why: fmt.Sprintf("--prop %q is not <name>=<value>", p)}
		}
		if _, dup := req.Props[name]; dup {
			return fmt.Errorf("--prop %s is given twice", name)
		}
		if req.Props == nil {
			req.Props = map[string]string{}
		}
		req.Props[name] = value
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	tk, err := c.CreateTask(ctx, req)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Out, "created %s\n", task.UnitOf(task.ID(tk.ID)))
	return err
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
