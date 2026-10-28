package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

func TestDoctorAndInitAskThePluginWhatTheyCheck(t *testing.T) {
	stub := func(auth forgeAnswer) *forgeStub {
		return &forgeStub{Stub: sourcetest.NewStub(), answers: map[string]forgeAnswer{
			"auth":           auth,
			"reviewed-repos": {out: "acme/api\nAcme/API\nacme/web\n"},
			"teams":          {out: "acme/platform\nacme/backend\n"},
		}}
	}

	t.Run("doctor reports the plugin's gh check", func(t *testing.T) {
		te := newTestEnv(t, nil)
		s := stub(forgeAnswer{out: "gh auth status: not logged in to github.example", exit: 1})
		te.Env.forge = sourcetest.Launch(t, "github", s, src.Host{})
		te.Socket = startDaemon(t, nil).socket
		te.run("doctor")
		if !strings.Contains(te.out.String(), "gh auth status: not logged in to github.example") {
			t.Fatalf("%s", te.out)
		}
		if commands, _ := s.asked(); !slices.ContainsFunc(commands, func(c []string) bool { return slices.Equal(c, []string{"auth"}) }) {
			t.Fatalf("commands %q", commands)
		}
		for _, call := range te.gh.calls {
			if strings.HasPrefix(call, "auth") {
				t.Errorf("gh reached outside the plugin: %s", call)
			}
		}
	})

	t.Run("init names the install when the plugin finds no gh", func(t *testing.T) {
		te := newInitTestEnv(t)
		te.Env.forge = sourcetest.Launch(t, "github", stub(forgeAnswer{out: "gh: not found", exit: 127}), src.Host{})
		if code := te.run("--projects-dir", "/x", "--profile-default"); code != 1 || !strings.Contains(te.out.String(), "fix with: brew install gh && gh auth login") {
			t.Fatalf("exit %d:\n%s", code, te.out)
		}
		if slices.ContainsFunc(te.gh.calls, func(c string) bool { return strings.HasPrefix(c, "auth") }) {
			t.Errorf("gh reached outside the plugin: %q", te.gh.calls)
		}
	})

	t.Run("init's scope suggestions come from the plugin", func(t *testing.T) {
		te := newInitTestEnv(t)
		s := stub(forgeAnswer{})
		te.Env.forge = sourcetest.Launch(t, "github", s, src.Host{})
		repos, err := te.reviewedRepos(context.Background())
		if err != nil || !slices.Equal(repos, []string{"acme/api", "acme/web"}) {
			t.Fatalf("repos %v (%v)", repos, err)
		}
		teams, err := te.userTeams(context.Background())
		if err != nil || !slices.Equal(teams, []string{"acme/backend", "acme/platform"}) {
			t.Fatalf("teams %v (%v)", teams, err)
		}
		commands, _ := s.asked()
		if want := [][]string{{"reviewed-repos", testNow.Add(-reviewedWindow).UTC().Format("2006-01-02")}, {"teams"}}; !slices.EqualFunc(commands, want, slices.Equal) {
			t.Fatalf("commands %q", commands)
		}
		if len(te.gh.calls) != 0 {
			t.Errorf("gh reached outside the plugin: %q", te.gh.calls)
		}
	})
}
