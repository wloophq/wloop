package workspace

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestSnapshotLookups(t *testing.T) {
	s := &Snapshot{
		Workspaces: []Workspace{{ID: "a"}, {ID: "b", Unread: true}},
		Panes: []Pane{
			{WorkspaceID: "a", Pane: "t:1", State: turn.AgentDone},
			{WorkspaceID: "a", Pane: "t:2", State: turn.AgentWaiting},
			{WorkspaceID: "b", UserActive: true},
		},
		Terminals: []Terminal{
			{Handle: "h1", WorkspaceID: "a", Usable: true},
			{Handle: "h2", WorkspaceID: "a"},
			{Handle: "h3", WorkspaceID: "a", Usable: true},
		},
	}
	if w, ok := s.Workspace("b"); !ok || !w.Unread {
		t.Errorf("Workspace(b) = %+v, %v", w, ok)
	}
	if _, ok := s.Workspace("z"); ok {
		t.Error("found a workspace that is not listed")
	}
	act := s.Activity()
	if a := act["a"]; !a.Busy || !a.Waiting || len(a.States) != 2 || a.UserActive {
		t.Errorf("activity of a = %+v", a)
	}
	if b := act["b"]; !b.UserActive || b.Busy || len(b.States) != 0 {
		t.Errorf("activity of b = %+v", b)
	}
	if got := s.TerminalsOf("a"); len(got) != 2 || got[0].Handle != "h1" || got[1].Handle != "h3" {
		t.Errorf("TerminalsOf(a) = %+v", got)
	}
	if term, ok := s.Terminal("h2"); !ok || term.Usable {
		t.Errorf("Terminal(h2) = %+v, %v", term, ok)
	}
}

func TestDomainWorkspaceImportsNoAdapter(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/wloophq/wloop/"
	for _, dep := range strings.Fields(string(out)) {
		rel, ok := strings.CutPrefix(dep, module)
		if ok && rel != "internal/brand" && !strings.HasPrefix(rel, "internal/domain/") {
			t.Errorf("internal/domain/workspace depends on %s", rel)
		}
	}
}
