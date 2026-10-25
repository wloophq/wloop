package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const adoptBasics = `{"data":{"pr":{"pullRequest":{"number":20,"title":"Theirs","state":"OPEN","headRefName":"feat/theirs","baseRefName":"main","headRefOid":"c9","repository":{"nameWithOwner":"acme/api"}}}}}`

func TestAdoptTurnsTheReviewWorkspaceIntoAnAuthorOne(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/theirs", Status: "needs-me", HostID: "local", LinkedPR: 20},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/wt/rev"] = "feat/theirs"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "rename ws-rev feat/theirs\nlink ws-rev 20" {
		t.Fatalf("writes:\n%s", got)
	}
	if len(m.Prompts) != 0 {
		t.Fatalf("adopt prompted an agent: %v", m.Prompts)
	}
}

func TestAdoptCreatesAWorkspaceWithoutAPrompt(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "create feat/theirs\nsession ws-1\nlink ws-1 20" {
		t.Fatalf("writes:\n%s", got)
	}
	if len(m.Prompts) != 0 {
		t.Fatalf("prompts = %+v, want none", m.Prompts)
	}
}

func TestTakeOverMarksTheInventory(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	st := te.Store
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 20}
	ctx := context.Background()
	if o, err := st.Override(ctx, ref); err != nil || !o.Adopted {
		t.Fatalf("not adopted: %+v %v", o, err)
	}
	evs, _ := st.Events(ctx, ref)
	if len(evs) != 1 || evs[0].Kind != dinv.KindAdopted {
		t.Fatalf("events: %+v", evs)
	}
}

func TestTakeOverRefusesMyOwnPullRequest(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/theirs", HostID: "local", IsMain: true},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = strings.Replace(adoptBasics, `"repository"`, `"author":{"__typename":"User","login":"me"},"repository"`, 1)
	te.git.branch["/src/api"] = "feat/theirs"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code == 0 || !strings.Contains(te.err.String(), "It is yours already") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Writes) != 0 || len(te.git.writes) != 0 {
		t.Fatalf("writes %v, git %v", m.Writes, te.git.writes)
	}
}

func TestAdoptRefusesABranchCheckedOutElsewhere(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-other", RepoID: "r1", Path: "/wt/other", DisplayName: "REVIEW other", Branch: "feat/theirs", HostID: "local"},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code == 0 {
		t.Fatalf("adopted into a second worktree: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "feat/theirs is checked out in /wt/other") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestCreateRemovesAWorkspaceOrcaPutOnAnotherBranch(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		args          []string
		basics, wants string
	}{
		"take-over": {[]string{"take-over", "https://github.com/acme/api/pull/20"}, adoptBasics, "create feat/theirs\nremove --force ws-1"},
		"review":    {[]string{"review", "https://github.com/acme/api/pull/7"}, openBasics, "create REVIEW feat/x\nremove --force ws-1"},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, BranchSuffix: "-2"}
			te := newTestEnv(t, m)
			te.gh.basics = c.basics
			if code := te.run(c.args...); code == 0 {
				t.Fatalf("kept a workspace on a renamed branch: %s", te.out)
			}
			if got := strings.Join(m.Writes, "\n"); got != c.wants {
				t.Fatalf("writes:\n%s", got)
			}
			head := "feat/theirs"
			if name == "review" {
				head = "feat/x"
			}
			if msg := te.err.String(); !strings.Contains(msg, "on branch "+head+"-2 instead of "+head) {
				t.Fatalf("err: %s", msg)
			}
			if len(m.WorkspaceList) != 0 {
				t.Fatalf("left %+v", m.WorkspaceList)
			}
			if len(m.Prompts) != 0 {
				t.Fatalf("an agent got its prompt in the renamed workspace: %+v", m.Prompts)
			}
		})
	}
}

func TestAdoptRefusesAReleasePRFromTheMainCheckoutsDefaultBranch(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api", DefaultBranch: "feat/theirs"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/theirs", HostID: "local", IsMain: true},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code == 0 {
		t.Fatalf("adopted a release PR: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "feat/theirs is checked out in /src/api") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestAdoptRefusesALocalBranchWithCommitsNotOnThePR(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.tips["feat/theirs"] = "0ld"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code == 0 {
		t.Fatalf("adopted onto a renamed branch: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "local branch feat/theirs has commits not on the PR: push them or `git branch -D feat/theirs`") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestAdoptFastForwardsALocalBranchBehindThePR(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.tips["feat/theirs"] = "0ld"
	te.git.ancestor["/src/api"] = true
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	gitWrites := strings.Join(te.git.writes, "\n")
	fetch, ff := strings.Index(gitWrites, "/src/api: fetch -q origin +refs/pull/20/head"), strings.Index(gitWrites, "/src/api: update-ref refs/heads/feat/theirs c9 0ld")
	if fetch < 0 || ff < fetch {
		t.Fatalf("git writes:\n%s", gitWrites)
	}
	if !strings.HasPrefix(strings.Join(m.Writes, "\n"), "create feat/theirs\n") || m.WorkspaceList[0].Branch != "feat/theirs" {
		t.Fatalf("writes %v workspaces %+v", m.Writes, m.WorkspaceList)
	}
}

func TestAdoptRefusesABranchCheckedOutInAWorktreeOrcaDoesNotList(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.tips["feat/theirs"] = "c9"
	te.git.worktrees["feat/theirs"] = "/elsewhere/wt"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code == 0 {
		t.Fatalf("adopted onto a renamed branch: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "feat/theirs is checked out in /elsewhere/wt") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestAdoptRefusesAMainCheckoutOnSomeoneElsesBranch(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/theirs", HostID: "local", IsMain: true},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code == 0 {
		t.Fatalf("took the main checkout for someone else's PR: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "feat/theirs is checked out in /src/api") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestAdoptReusesALocalBranchAtThePRHead(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.tips["feat/theirs"] = "c9"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.HasPrefix(strings.Join(m.Writes, "\n"), "create feat/theirs\n") {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestFindAuthorWorkspacePrefersAWorktreeOverTheMainCheckout(t *testing.T) {
	t.Parallel()
	yes := func() (bool, error) { return true, nil }
	no := func() (bool, error) { return false, nil }
	repos := []wsdomain.Repo{{ID: "r1", RemoteKey: "github.com/acme/api"}}
	pr := &github.PullRequest{Facts: pr.Facts{Ref: github.Ref{Owner: "acme", Repo: "api", Number: 20}, HeadRefName: "feat/theirs"}}
	for name, worktree := range map[string]wsdomain.Workspace{
		"on the branch": {ID: "wt", RepoID: "r1", HostID: "local", Branch: "feat/theirs"},
		"linked":        {ID: "wt", RepoID: "r1", HostID: "local", Branch: "other", LinkedPR: 20},
	} {
		main := wsdomain.Workspace{ID: "main", RepoID: "r1", HostID: "local", IsMain: true, Branch: "feat/theirs"}
		for _, all := range [][]wsdomain.Workspace{{main, worktree}, {worktree, main}} {
			if w, ok, _ := findAuthorWorkspace(config.Defaults(), repos, all, pr.Ref, pr, yes); !ok || w.ID != "wt" {
				t.Fatalf("%s: got %+v %v", name, w, ok)
			}
		}
	}
	upstreamOnly := []wsdomain.Workspace{{ID: "main", RepoID: "r1", HostID: "local", IsMain: true, Branch: "wip", PushBranch: "feat/theirs"}}
	if w, ok, _ := findAuthorWorkspace(config.Defaults(), repos, upstreamOnly, pr.Ref, pr, yes); ok {
		t.Fatalf("took a main checkout by its push branch: %+v", w)
	}
	onBranch := []wsdomain.Workspace{{ID: "main", RepoID: "r1", HostID: "local", IsMain: true, Branch: "feat/theirs"}}
	if w, ok, _ := findAuthorWorkspace(config.Defaults(), repos, onBranch, pr.Ref, pr, no); ok {
		t.Fatalf("took a main checkout for someone else's PR: %+v", w)
	}
	if _, ok, _ := findAuthorWorkspace(config.Defaults(), repos, onBranch, pr.Ref, pr, yes); !ok {
		t.Fatal("main checkout on my PR's branch not taken")
	}
}

func TestTakeOverIsTheVerb(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "create feat/theirs\nsession ws-1\nlink ws-1 20" {
		t.Fatalf("writes:\n%s", got)
	}
}

func TestTakeOverClearsTheFingerprint(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 20}
	st := te.loadRounds(t)
	st.SetFingerprint(ref.String(), "2026-10-18T10:00:00Z@c9")
	te.saveRounds(t, st)
	if code := te.run("take-over", ref.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.loadRounds(t).Fingerprint(ref.String()); got != "" {
		t.Fatalf("fingerprint %q survived the take-over", got)
	}
}
