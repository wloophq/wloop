package cli

import (
	"context"
	"errors"
	"testing"
)

type bootApp struct{ calls []string }

func (a *bootApp) Refresh(context.Context)   { a.calls = append(a.calls, "refresh") }
func (a *bootApp) Run(context.Context) error { a.calls = append(a.calls, "run"); return nil }

func TestBootMenubarLoadsOnceWithTheDaemonUp(t *testing.T) {
	app := &bootApp{}
	waited := false
	err := bootMenubar(context.Background(), func(context.Context) error { return nil },
		func(context.Context) error { waited = true; return nil }, app)
	if err != nil || waited || len(app.calls) != 1 || app.calls[0] != "run" {
		t.Fatalf("calls %v, waited %v, err %v: want Run alone", app.calls, waited, err)
	}
}

func TestBootMenubarPaintsTheErrorMenuWithTheDaemonDown(t *testing.T) {
	app := &bootApp{}
	err := bootMenubar(context.Background(), func(context.Context) error { return errors.New("down") },
		func(context.Context) error { return nil }, app)
	if err != nil || len(app.calls) != 2 || app.calls[0] != "refresh" || app.calls[1] != "run" {
		t.Fatalf("calls %v, err %v: want Refresh then Run", app.calls, err)
	}
}
