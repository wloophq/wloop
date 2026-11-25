package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type bootApp struct{ calls []string }

func (a *bootApp) Refresh(context.Context)   { a.calls = append(a.calls, "refresh") }
func (a *bootApp) Run(context.Context) error { a.calls = append(a.calls, "run"); return nil }
func (a *bootApp) StartFloat()               { a.calls = append(a.calls, "float") }

func TestBootMenubarLoadsOnceWithTheDaemonUp(t *testing.T) {
	t.Parallel()
	app := &bootApp{}
	waited := false
	err := bootMenubar(context.Background(), func(context.Context) error { return nil },
		func(context.Context) error { waited = true; return nil }, app)
	if err != nil || waited || len(app.calls) != 1 || app.calls[0] != "run" {
		t.Fatalf("calls %v, waited %v, err %v: want Run alone", app.calls, waited, err)
	}
}

func TestBootMenubarPaintsTheErrorMenuWithTheDaemonDown(t *testing.T) {
	t.Parallel()
	app := &bootApp{}
	err := bootMenubar(context.Background(), func(context.Context) error { return errors.New("down") },
		func(context.Context) error { return nil }, app)
	if err != nil || strings.Join(app.calls, ",") != "float,refresh,run" {
		t.Fatalf("calls %v, err %v: want the float, Refresh, then Run", app.calls, err)
	}
}
