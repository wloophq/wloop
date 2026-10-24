package fake

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestObserverMirrorsTheFields(t *testing.T) {
	ctx := context.Background()
	m := &Manager{
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-1"}},
		Activity: map[string]wsdomain.Activity{
			"ws-1": {Busy: true, States: []turn.AgentState{turn.AgentWorking}},
			"ws-2": {UserActive: true},
			"ws-3": {Busy: true},
			"ws-4": {Busy: true, Waiting: true},
			"ws-5": {},
		},
		Sessions: map[string]string{"ws-1": "term-ws-1"},
	}
	ws, scope, err := m.Workspaces(ctx)
	if err != nil || len(ws) != 1 || !scope.Complete {
		t.Fatalf("Workspaces = %v %+v %v", ws, scope, err)
	}
	panes, err := m.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap := wsdomain.Snapshot{Panes: panes}
	act := snap.Activity()
	if !act["ws-1"].Busy || !act["ws-2"].UserActive || !slices.Equal(act["ws-1"].States, m.Activity["ws-1"].States) {
		t.Fatalf("activity from panes = %+v", act)
	}
	if !act["ws-3"].Busy || act["ws-3"].Waiting || !act["ws-4"].Busy || !act["ws-4"].Waiting || act["ws-2"].Busy {
		t.Fatalf("Busy and Waiting without States = %+v", act)
	}
	if len(panes) != 4 {
		t.Fatalf("panes = %+v, want none for an idle closed workspace", panes)
	}
	terms, scope, err := m.AllTerminals(ctx)
	if err != nil || !scope.Complete || len(terms) != 1 || terms[0].Handle != "term-ws-1" || !terms[0].Usable {
		t.Fatalf("AllTerminals = %+v %+v %v", terms, scope, err)
	}
}

func TestFakeCountsEachMethod(t *testing.T) {
	ctx, scope := metertest.Start(t, context.Background())
	m := &Manager{WorkspaceList: []wsdomain.Workspace{{ID: "ws-1"}}}
	if _, err := m.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.SetStatus(ctx, "ws-1", "done"); err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, scope, metertest.Budget{
		{Res: meter.Workspace}:                          2,
		{Res: meter.Workspace, Field: metertest.Spawns}: 0,
		{Res: meter.Workspace, Op: "List"}:              1,
		{Res: meter.Workspace, Op: "SetStatus"}:         1,
		{Res: meter.Workspace, Field: metertest.Errors}: 0,
	})
}

func TestFakeCountsAFailedCall(t *testing.T) {
	ctx, scope := metertest.Start(t, context.Background())
	m := &Manager{Unreachable: errors.New("down")}
	if _, _, err := m.Workspaces(ctx); err == nil {
		t.Fatal("want the unreachable error")
	}
	if got := metertest.Count(scope, metertest.Limit{Res: meter.Workspace, Op: "Workspaces", Field: metertest.Errors}); got != 1 {
		t.Fatalf("Workspaces errors = %d, want 1", got)
	}
}

func TestUnregisterDropsTheEntry(t *testing.T) {
	m := &Manager{WorkspaceList: []wsdomain.Workspace{{ID: "a"}, {ID: "b"}}}
	if err := m.Unregister(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if len(m.WorkspaceList) != 1 || m.WorkspaceList[0].ID != "b" {
		t.Fatalf("workspaces %+v", m.WorkspaceList)
	}
}
