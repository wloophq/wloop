package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// fakeResolver resolves the addresses under prefix to their rest.
type fakeResolver struct{ prefix string }

func (f fakeResolver) Resolve(_ context.Context, address string) (string, bool, error) {
	key, ok := strings.CutPrefix(address, f.prefix)
	return key, ok, nil
}

func TestATargetResolvesThroughTheCodecs(t *testing.T) {
	t.Parallel()
	resolvers := map[unit.ProviderID]unit.Resolver{"slack": fakeResolver{"https://acme.slack.com/archives/"}}
	providers := []unit.ProviderID{"github", "slack"}
	for arg, want := range map[string]unit.Unit{
		"https://acme.slack.com/archives/C1/p1": {Provider: "slack", Key: "C1/p1"},
		"https://github.com/acme/api/pull/7":    {Provider: "github", Key: "acme/api#7"},
		"slack:T1/C1/1.2":                       {Provider: "slack", Key: "T1/C1/1.2"},
		"acme/api#7":                            {Provider: "github", Key: "acme/api#7"},
	} {
		got, err := resolveTarget(context.Background(), arg, providers, resolvers, pluginCodecs())
		if err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", arg, got, err, want)
		}
	}
	if _, err := resolveTarget(context.Background(), "https://example.com/x", providers, resolvers, pluginCodecs()); err == nil {
		t.Error("an address no provider knows resolved")
	}
}

func TestInstructionsByHandResolvesAnyProvider(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "w1", RepoID: "r1", Path: "/src/api-7", HostID: wsdomain.LocalHost, LinkedPR: 7}},
	}
	te := newTestEnv(t, m)
	ctx := context.Background()
	repos, all := te.workspaceState(ctx)
	for _, c := range []struct {
		target, cwd string
		want        unit.Unit
	}{
		{"slack:T1/C1/1.2", "/elsewhere", unit.Unit{Provider: "slack", Key: "T1/C1/1.2"}},
		{"https://github.com/acme/web/pull/3", "/elsewhere", unit.Unit{Provider: "github", Key: "acme/web#3"}},
		{"", "/src/api-7/pkg", unit.Unit{Provider: "github", Key: "acme/api#7"}},
	} {
		ws, repo, inWS := workspaceAt(c.cwd, repos, all)
		got, err := te.handUnit(ctx, c.target, ws, repo, inWS)
		if err != nil || got != c.want {
			t.Errorf("%q in %s: %v %v, want %v", c.target, c.cwd, got, err, c.want)
		}
	}
}

func TestATurnByHandOnAUnitThatIsNoPullRequestTakesTheUnitPath(t *testing.T) {
	te, _ := threadTurnEnv(t)
	d, err := te.intentDef("discuss")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	if err := te.turnByHand(context.Background(), handTurn{def: d, url: thread1.String(), cwd: "/elsewhere"}, &text); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if !strings.Contains(text.String(), "# Discuss thread 1700000000.1, asked for by you.") {
		t.Fatalf("instructions:\n%s", text.String())
	}
}
