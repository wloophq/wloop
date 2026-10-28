package factworkspace_test

import (
	"context"
	"testing"
	"time"

	factworkspace "github.com/wloophq/wloop/internal/adapter/facts/workspace"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workday"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// heldBy holds the units of a source no fact names, by workspace id.
type heldBy map[string]workspace.Holding

func (h heldBy) Held(context.Context, *workspace.Snapshot, config.Config) (workspace.Held, error) {
	return h, nil
}

func (h heldBy) Of(w workspace.Workspace) (workspace.Holding, bool) {
	x, ok := h[w.ID]
	return x, ok
}

func TestWorkspaceFactsReadTheUnitsEachWorkspaceHolds(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "rev", Path: "/w/rev", Branch: "notes"})
	w.add(workspace.Workspace{ID: "dev", Path: "/w/dev", Branch: "draft"})
	w.add(workspace.Workspace{ID: "free", Path: "/w/free", Branch: "scratch"})
	ended := now.Add(-50 * time.Hour)
	held := heldBy{
		"rev": {Unit: unit.Unit{Provider: "acme", Key: "doc-12"}, Repo: "acme/handbook", Review: true, State: "closed", Ended: ended, Duplicate: true},
		"dev": {Unit: unit.Unit{Provider: "acme", Key: "doc-13"}, State: "open"},
	}
	d := w.deps()
	d.Holdings = held
	p := factworkspace.New(d)
	facts := func(id string) map[string]any {
		f, err := p.Facts(context.Background(), w.unit(id))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	rev := facts("rev")
	if rev["workspace.pr"] != "doc-12" || rev["workspace.pr_state"] != "closed" || rev["workspace.role"] != factworkspace.RoleReview ||
		rev["workspace.duplicate"] != true || rev["workspace.pr_finished_for"] != workday.Between(ended, now) {
		t.Errorf("the reviewed unit's workspace: %v", rev)
	}
	if dev := facts("dev"); dev["workspace.pr"] != "doc-13" || dev["workspace.role"] != factworkspace.RoleAuthor ||
		dev["workspace.pr_finished_for"] != time.Duration(0) || dev["workspace.duplicate"] != false {
		t.Errorf("the developed unit's workspace: %v", dev)
	}
	if free := facts("free"); free["workspace.pr"] != "" || free["workspace.role"] != factworkspace.RoleNone || free["workspace.pr_state"] != "" {
		t.Errorf("a workspace holding nothing: %v", free)
	}
	owners, err := p.Owners(context.Background(), w.unit("rev"))
	if err != nil || len(owners) != 1 || owners[0] != held["rev"].Unit {
		t.Errorf("owners %v %v", owners, err)
	}
}
