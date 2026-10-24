package e2e

import (
	"strings"
	"testing"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

func testScrub(home string) scrubber {
	return scrubber{viewer: "ana-gh", home: home, allowed: []string{home + "/work/sandbox-wt", home + "/.claude"}}
}

func TestScrubRewritesTheHomeNameInAnyForm(t *testing.T) {
	home := "/Users/ana.souza"
	in := `{"home":"/Users/ana.souza/work/sandbox-wt/a","agent":"profile-ana-souza-claude","cut":"x-a-souza-claude-personal","dot":"ana.souza","under":"Ana_Souza"}`
	got, err := testScrub(home).text(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, left := range []string{"ana", "souza"} {
		if strings.Contains(strings.ToLower(got), left) {
			t.Fatalf("%q left in %s", left, got)
		}
	}
	if !strings.Contains(got, `"/home/u/work/sandbox-wt/a"`) {
		t.Fatalf("the home path: %s", got)
	}
}

func TestScrubFailsOnANameItCannotRewrite(t *testing.T) {
	if _, err := testScrub("/Users/ana.souza").text(`{"who":"souzaana"}`); err == nil {
		t.Fatal("a name glued to another word passed")
	}
}

func TestScrubSpellsTheSandboxOneWay(t *testing.T) {
	owner, repo, _ := strings.Cut(Sandbox, "/")
	in := `{"pr":"` + Sandbox + `#3","owner":"` + owner + `","repo":"` + repo + `","url":"https://github.com/` + Sandbox + `/pull/3"}`
	got, err := testScrub("/Users/ana.souza").text(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"pr":"acme/sandbox#3","owner":"acme","repo":"sandbox","url":"https://github.com/acme/sandbox/pull/3"}`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestScrubFailsOnAnythingOutsideTheSandbox(t *testing.T) {
	home := "/Users/ana.souza"
	for name, in := range map[string]string{
		"a path outside":         `{"worktree":"` + home + `/orca/workspaces/billing/fix-x"}`,
		"a temporary path":       `{"worktree":"/private/tmp/scratch"}`,
		"another repository":     `{"pr":"acme/billing#12"}`,
		"another repository url": `{"url":"https://github.com/acme/billing/pull/12"}`,
	} {
		if _, err := testScrub(home).text(in); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	if _, err := testScrub(home).text(`{"home":"` + home + `/.claude/projects/x","wt":"` + home + `/work/sandbox-wt"}`); err != nil {
		t.Errorf("an allowed path failed: %v", err)
	}
}

func TestOnlyASandboxWorkspacesSessionsAreKept(t *testing.T) {
	snap := wsdomain.Snapshot{Workspaces: []wsdomain.Workspace{{ID: "ws1", Path: "/w/sandbox-a"}}}
	got := sandboxSessions(snap, []string{"/w/sandbox-a", "/src/sandbox"})
	want := "0 OR workspace_id = 'ws1' OR worktree = '/w/sandbox-a' OR worktree LIKE '/w/sandbox-a/%' OR worktree = '/src/sandbox' OR worktree LIKE '/src/sandbox/%'"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestTheSandboxsWorkspaceRootIsTheSandboxs(t *testing.T) {
	_, repo, _ := strings.Cut(Sandbox, "/")
	snap := wsdomain.Snapshot{Workspaces: []wsdomain.Workspace{
		{ID: "a", Path: "/Users/ana/orca/workspaces/" + repo + "/REVIEW-x"},
		{ID: "b", Path: "/Users/ana/orca/workspaces/" + repo + "/e2e-y"},
		{ID: "main", Path: "/Users/ana/src/" + repo, IsMain: true},
		{ID: "c", Path: "/Users/ana/orca/workspaces/elsewhere"},
	}}
	got := workspaceRoots(snap)
	want := []string{"/Users/ana/orca/workspaces/" + repo}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("roots %v", got)
	}
	s := scrubber{viewer: "viewer-gh", home: "/Users/ana", allowed: got}
	if _, err := s.text(`{"wt":"/Users/ana/orca/workspaces/` + repo + `/REVIEW-gone-1"}`); err != nil {
		t.Fatalf("a gone review worktree of the sandbox: %v", err)
	}
	if _, err := s.text(`{"wt":"/Users/ana/orca/workspaces/billing/fix"}`); err == nil {
		t.Fatal("another repository's worktree passed")
	}
}

func TestTheSandboxsWorkspaceRootComesFromThePathsTheRecordingNames(t *testing.T) {
	_, repo, _ := strings.Cut(Sandbox, "/")
	short := repo[strings.LastIndex(repo, "-")+1:]
	raw := `{"main":"/Users/ana/projects/oss/` + repo + `","wt":"/Users/ana/orca/workspaces/` + short + `/REVIEW-bot-1792456913","other":"/Users/ana/orca/workspaces/billing/fix"}`
	got := sandboxRoots(raw)
	if len(got) != 1 || got[0] != "/Users/ana/orca/workspaces/"+short {
		t.Fatalf("roots %v", got)
	}
	s := scrubber{viewer: "viewer-gh", home: "/Users/ana", allowed: append(got, "/Users/ana/projects/oss/"+repo)}
	if _, err := s.text(`{"main":"/Users/ana/projects/oss/` + repo + `","wt":"/Users/ana/orca/workspaces/` + short + `/REVIEW-bot-1792456913"}`); err != nil {
		t.Fatalf("the sandbox's paths: %v", err)
	}
	if _, err := s.text(raw); err == nil {
		t.Fatal("another repository's worktree passed")
	}
}
