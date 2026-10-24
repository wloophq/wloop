package workspace

import "testing"

func TestFindRepos(t *testing.T) {
	repos := []Repo{
		{ID: "a", Path: "/p/jsm", RemoteKey: "github.com/acme/monorepo"},
		{ID: "b", Path: "/p/monorepo", RemoteKey: "github.com/acme/monorepo"},
		{ID: "c", Path: "/p/other", RemoteKey: "github.com/acme/other"},
	}
	if got := FindRepos(repos, "github.com/acme/monorepo", ""); len(got) != 2 || got[0].ID != "b" {
		t.Fatalf("basename preference: %+v", got)
	}
	if got := FindRepos(repos, "github.com/ACME/monorepo", "a"); got[0].ID != "a" {
		t.Fatalf("workspace owner preference: %+v", got)
	}
	if got := FindRepos(repos, "github.com/acme/missing", ""); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestOwnerName(t *testing.T) {
	if o, n, ok := (Repo{RemoteKey: "github.com/acme/api"}).OwnerName(); !ok || o != "acme" || n != "api" {
		t.Fatal("github remote not parsed")
	}
	if _, _, ok := (Repo{RemoteKey: "gitlab.com/acme/api"}).OwnerName(); ok {
		t.Fatal("non-GitHub remote accepted")
	}
}

func TestIsDefaultBranch(t *testing.T) {
	known := Repo{DefaultBranch: "develop"}
	if !known.IsDefaultBranch("develop") || !known.IsDefaultBranch("main") || !known.IsDefaultBranch("master") || known.IsDefaultBranch("feat/x") {
		t.Fatal("origin/HEAD adds to main and master")
	}
	unknown := Repo{}
	if !unknown.IsDefaultBranch("main") || !unknown.IsDefaultBranch("master") || unknown.IsDefaultBranch("feat/x") || unknown.IsDefaultBranch("") {
		t.Fatal("unknown default branch falls back to main and master")
	}
}

func TestPRBranch(t *testing.T) {
	if got := (Workspace{Branch: "wip", PushBranch: "feat/x"}).PRBranch(); got != "feat/x" {
		t.Fatalf("worktree: %q", got)
	}
	if got := (Workspace{Branch: "wip", PushBranch: "feat/x", IsMain: true}).PRBranch(); got != "wip" {
		t.Fatalf("main checkout: %q", got)
	}
}
