package cli

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestRoundPromptNamesTheExecutable(t *testing.T) {
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.Executable = "/Apps/My App/" + brand.Name
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 1 || !strings.Contains(m.Prompts[0].Text, "Run `'/Apps/My App/"+brand.Name+"' instructions review --turn ") || !strings.Contains(m.Prompts[0].Text, " https://github.com/acme/api/pull/7` and follow") {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
}

func TestRoundStopsOnAMissingProfile(t *testing.T) {
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.cfg.Profile = filepath.Join(t.TempDir(), "nope")
	te.gh.basics = openBasics
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 || !strings.Contains(te.err.String(), "not found") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 0 || len(m.Commands) != 0 {
		t.Fatalf("a round started: %+v %v", m.Prompts, m.Commands)
	}
}

var mintedSessionID = regexp.MustCompile(` --session-id '[^']*'`)

// launches drops the minted session id and the session settings from agent
// commands, leaving how the agent starts.
func launches(cmds []string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i], _, _ = strings.Cut(mintedSessionID.ReplaceAllString(c, ""), " --settings ")
	}
	return out
}
