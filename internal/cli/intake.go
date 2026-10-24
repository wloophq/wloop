package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
)

// errDetached is a follow the user interrupted; the intake goes on.
var errDetached = errors.New("stopped following the intake")

const exitDetached = 130

// followWait is how long one poll of an intake waits for a new line.
var followWait = 20 * time.Second

// ask has the daemon accept a review or an address and follows the
// intake that accepted it, unless noWait.
func (e *Env) ask(ctx context.Context, ref github.Ref, action string, req api.ActionRequest, noWait bool) error {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	req.AgentHome = e.callerAgentHome(config.Config{Agent: e.callerKind()})
	req.Source = sourceCLI
	res, err := c.Ask(ctx, ref.Unit(), action, req)
	printLines(e.Out, res.Output)
	if err != nil {
		return err
	}
	label := commandOf(action) + " " + ref.String()
	in := res.Intake
	if in == nil {
		fmt.Fprintf(e.Out, "%s: done by the daemon\n", label)
		return nil
	}
	switch {
	case !in.Joined:
		fmt.Fprintf(e.Out, "%s: accepted as intake %d\n", label, in.Seq)
	case in.Waiting():
		fmt.Fprintf(e.Out, "%s: joined intake %d\n", label, in.Seq)
	default:
		fmt.Fprintf(e.Out, "%s: intake %d is already starting the turn; this request changed nothing\n", label, in.Seq)
	}
	if noWait {
		return nil
	}
	return e.follow(ctx, c, in.Seq, label)
}

// follow prints the intake's lines as they come until it ends; an
// interrupt stops following and leaves the intake to the daemon.
func (e *Env) follow(ctx context.Context, c *client.Client, seq int64, label string) error {
	after := 0
	for {
		in, err := c.Intake(ctx, seq, after, followWait)
		if ctx.Err() != nil {
			fmt.Fprintf(e.Out, "intake %d goes on in the daemon; follow it with "+brand.Command+" intake %d, stop it with "+brand.Command+" intake cancel %d\n", seq, seq, seq)
			return errDetached
		}
		if err != nil {
			return err
		}
		printLines(e.Out, in.Log)
		after = in.LogLen
		switch in.State {
		case api.IntakeStarted:
			fmt.Fprintf(e.Out, "%s: done by the daemon\n", label)
			return nil
		case api.IntakeFailed:
			return fmt.Errorf("intake %d failed: %s", seq, in.Error)
		case api.IntakeCanceled:
			return fmt.Errorf("intake %d was canceled", seq)
		}
	}
}

func runIntake(ctx context.Context, e *Env, args []string) error {
	var usage = "usage: " + brand.Command + " intake <n> | intake without <n> | intake cancel <n>"
	verb, arg := "", ""
	switch len(args) {
	case 1:
		arg = args[0]
	case 2:
		verb, arg = args[0], args[1]
	default:
		return errors.New(usage)
	}
	seq, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || seq <= 0 || verb != "" && verb != "without" && verb != "cancel" {
		return errors.New(usage)
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	switch verb {
	case "without":
		if err := c.IntakeWithout(ctx, seq); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "intake %d starts without the brief\n", seq)
		return nil
	case "cancel":
		if err := c.IntakeCancel(ctx, seq); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "intake %d canceled\n", seq)
		return nil
	}
	in, err := c.Intake(ctx, seq, 0, 0)
	if err != nil {
		return err
	}
	label := fmt.Sprintf("intake %d", seq)
	if ref, err := pr.ParseURL(in.PR); err == nil {
		label = in.Intent + " " + ref.String()
	}
	return e.follow(ctx, c, seq, label)
}
