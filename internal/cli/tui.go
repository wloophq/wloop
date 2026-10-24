package cli

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/tui"
	"github.com/wloophq/wloop/internal/termfmt"
)

func runTUI(ctx context.Context, e *Env, args []string) (err error) {
	if len(args) > 0 {
		return errors.New("usage: " + brand.Command + " tui")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer stop()
	t, err := tui.Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, t.Err(), t.Close()) }()
	open := func(u string) error { return e.openInBrowser(ctx, u) }
	return tui.Run(ctx, tui.Config{Daemon: c, Term: t, Now: e.Now, Color: e.tuiColor(), OpenURL: open})
}

// tuiColor is termfmt's color decision for the screen the TUI draws on.
func (e *Env) tuiColor() bool {
	return e.term(termfmt.Auto).Color
}
