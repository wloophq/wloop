package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func probeManager() *fake.Manager {
	return &fake.Manager{WorkspaceList: []wsdomain.Workspace{
		{ID: "ws-other", Path: "/wt/other", DisplayName: "other", HostID: wsdomain.LocalHost},
		{ID: "ws-e2e", Path: "/wt/e2e", DisplayName: "e2e-1-home", HostID: wsdomain.LocalHost},
	}}
}

func TestProbeResumesInItsWorkspace(t *testing.T) {
	t.Parallel()
	m := probeManager()
	te := newTestEnv(t, m)
	ctx := context.Background()
	ref := pr.Ref{Owner: "loop-e2e", Repo: "probe", Number: 1760000001}
	first := turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Probe), Trigger: dturn.Automated, WorkspaceID: "ws-e2e", Home: "/homes/a", Model: "haiku"}
	if err := (cliLauncher{te.Env}).Launch(ctx, first); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	b := te.bookOf(t, pr.Ref(ref))
	if len(b.Turns) != 1 || b.Turns[0].Intent != dturn.Intent(dintent.Probe) || b.Turns[0].Trigger != dturn.Automated || b.Turns[0].SentTo != te.sessionOf(t, "fake", "term-ws-e2e") {
		t.Fatalf("turns %+v", b.Turns)
	}
	if err := (cliLauncher{te.Env}).Launch(ctx, turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Probe), ResumeOf: b.Turns[0].Seq, Answer: "finish"}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "session ws-e2e\nsend term-ws-e2e\nsession ws-e2e\nsend term-ws-e2e" {
		t.Fatalf("writes:\n%s", got)
	}
	for _, cmd := range m.Commands {
		if !strings.Contains(cmd, "CLAUDE_CONFIG_DIR='/homes/a'") || !strings.Contains(cmd, "--model 'haiku'") || strings.Contains(cmd, "disableAllHooks") {
			t.Errorf("session command %s", cmd)
		}
	}
	b = te.bookOf(t, pr.Ref(ref))
	if len(b.Turns) != 2 || b.Turns[1].ResumeOf != b.Turns[0].Seq || b.Turns[1].Trigger != dturn.Automated || b.Turns[1].SentTo != te.sessionOf(t, "fake", "term-ws-e2e") {
		t.Fatalf("turns %+v", b.Turns)
	}
	if text := turnText(t, te, m.Prompts[1].Text); !strings.Contains(text, "## Where the last session stopped") || !strings.Contains(text, "finish") {
		t.Fatalf("resume turn:\n%s", text)
	}
	if len(te.gh.calls) != 0 {
		t.Fatalf("a probe read GitHub: %v", te.gh.calls)
	}
}

func TestProbeRefusesAnUnknownWorkspace(t *testing.T) {
	t.Parallel()
	m := probeManager()
	te := newTestEnv(t, m)
	ref := pr.Ref{Owner: "loop-e2e", Repo: "probe", Number: 1760000002}
	err := cliLauncher{te.Env}.Launch(context.Background(), turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Probe), WorkspaceID: "ws-gone"})
	if err == nil || !strings.Contains(err.Error(), "ws-gone") || len(m.Writes) != 0 {
		t.Fatalf("err = %v, writes %v", err, m.Writes)
	}
}

func TestProbeWritesNoView(t *testing.T) {
	t.Parallel()
	m := probeManager()
	te := newTestEnv(t, m)
	ref := pr.Ref{Owner: "loop-e2e", Repo: "probe", Number: 1760000003}
	if err := (cliLauncher{te.Env}).Launch(context.Background(), turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Probe), Trigger: dturn.Automated, WorkspaceID: "ws-e2e"}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	for _, batch := range te.sink.Batches() {
		for _, it := range batch {
			if it.Unit == ref.Unit() && it.Status != view.None {
				t.Errorf("a view wrote %+v", it)
			}
		}
	}
	for _, w := range m.Writes {
		if strings.HasPrefix(w, "status ") || strings.HasPrefix(w, "unread ") || strings.HasPrefix(w, "link ") {
			t.Errorf("the probe wrote a card: %s", w)
		}
	}
}

func TestProbePolicyDeniesWrites(t *testing.T) {
	t.Parallel()
	raw := claude.Agent{}.Settings(probePolicy("'/bin/"+brand.Name+"'", probeStored).Denied(turns.Control{Commands: [][]string{{"orca"}}}))
	for _, want := range []string{"Bash(gh *)", "Bash(git push *)", "Bash(git commit *)", "Bash(orca *)", "Bash(curl *)"} {
		if !strings.Contains(raw, want) {
			t.Errorf("deny lacks %s:\n%s", want, raw)
		}
	}
	rules := allowRules(t, raw)
	for _, want := range []string{"Bash('/bin/" + brand.Name + "' instructions probe --turn *)", "Bash('/bin/" + brand.Name + "' turn end *)", "Bash('/bin/" + brand.Name + "' turn answer *)", claude.TurnEndTool, "Edit(./.loop/**)"} {
		if !slices.Contains(rules, want) {
			t.Errorf("allow lacks %s: %q", want, rules)
		}
	}
	if strings.Contains(raw, "disableAllHooks") {
		t.Errorf("the probe disables the home's hooks:\n%s", raw)
	}
	if !probePolicy("p", probeStored).TurnEnd || !roundPolicy("p", bundledStored()).TurnEnd || tryPolicy("p", bundledStored()).TurnEnd {
		t.Error("only the probe and round policies let the session end its turn")
	}
	cmd, err := claude.Agent{}.Command(turns.AgentSession{Home: "/h", Model: "haiku", Policy: probePolicy(brand.Name, probeStored)})
	if err != nil || strings.Contains(cmd, "--strict-mcp-config") || strings.Contains(cmd, "--setting-sources") {
		t.Fatalf("%s %v", cmd, err)
	}
}

func TestProbeCommandNeedsAWorkspaceAndTheDaemon(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("probe"); code == 0 || !strings.Contains(te.err.String(), "usage: probe --workspace") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	te.err.Reset()
	te.Socket = t.TempDir() + "/none.sock"
	if code := te.run("probe", "--workspace", "ws-e2e"); code == 0 || !strings.Contains(te.err.String(), "daemon") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

type preparedKind struct {
	turns.Agent
	prepared []turns.AgentSession
}

func (k *preparedKind) Preflight(ctx context.Context, s turns.AgentSession) error {
	k.prepared = append(k.prepared, s)
	return k.Agent.Preflight(ctx, s)
}

func TestAProbeSessionIsTrusted(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, probeManager())
	k := &preparedKind{Agent: te.Agents["claude"]}
	te.Agents = map[string]turns.Agent{"claude": k}
	ref := pr.Ref{Owner: "loop-e2e", Repo: "probe", Number: 1760000004}
	if err := (cliLauncher{te.Env}).Launch(context.Background(), turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Probe), Trigger: dturn.Automated, WorkspaceID: "ws-e2e", Home: t.TempDir()}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if len(k.prepared) != 1 || !k.prepared[0].Trusted {
		t.Fatalf("prepared %+v", k.prepared)
	}
}
