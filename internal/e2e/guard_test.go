package e2e

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestGuardRefusesForeignNames(t *testing.T) {
	home := brand.Org + "/" + brand.StateDir()
	for _, name := range []string{"review 17/10 14:02", "main", "", strings.ToUpper(Prefix) + "x", "x-" + Prefix + "1"} {
		if MustOwn(name) == nil {
			t.Errorf("MustOwn(%q) passed", name)
		}
	}
	if err := MustOwn(Prefix + "20261018-1200-home"); err != nil {
		t.Error(err)
	}
	for _, repo := range []string{"acme/infra", home, "", Sandbox + "-2", "other/" + brand.Name + "-sandbox"} {
		if SandboxOnly(repo) == nil {
			t.Errorf("SandboxOnly(%q) passed", repo)
		}
	}
	if err := SandboxOnly(Sandbox); err != nil {
		t.Error(err)
	}
	for _, ws := range []Workspace{
		{Name: "feat/x", Repo: "acme/infra", PR: 1},
		{Name: "feat/x", Repo: Sandbox},
		{Name: "main", Repo: home, PR: 3},
	} {
		if MustOwnWorkspace(ws) == nil {
			t.Errorf("MustOwnWorkspace(%+v) passed", ws)
		}
	}
	for _, ws := range []Workspace{
		{Name: "feat/x", Repo: Sandbox, PR: 3},
		{Name: Prefix + "1-s4", Repo: Sandbox},
		{Name: Prefix + "1-home", Repo: home},
	} {
		if err := MustOwnWorkspace(ws); err != nil {
			t.Errorf("MustOwnWorkspace(%+v): %v", ws, err)
		}
	}
}

func TestGuardNamesDeriveFromTheBrand(t *testing.T) {
	if want := brand.Org + "/" + brand.Name + "-sandbox"; Sandbox != want {
		t.Errorf("Sandbox %q, want %q", Sandbox, want)
	}
	if want := brand.Name + "-e2e-"; Prefix != want {
		t.Errorf("Prefix %q, want %q", Prefix, want)
	}
}

func TestBranchGuardAcceptsOnePrefixSegment(t *testing.T) {
	for _, b := range []string{Prefix + "x", "someone/" + Prefix + "x"} {
		if err := MustOwnBranch(b); err != nil {
			t.Errorf("MustOwnBranch(%q): %v", b, err)
		}
	}
	for _, b := range []string{"someone/feature", "a/b/" + Prefix + "x", "main", "", "/" + Prefix + "x", Prefix + "x/main"} {
		if MustOwnBranch(b) == nil {
			t.Errorf("MustOwnBranch(%q) passed", b)
		}
	}
}
