package cli

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// barrier lets n callers through only once all n have arrived, and reports
// whoever gave up waiting.
type barrier struct {
	mu      sync.Mutex
	arrived int
	n       int
	all     chan struct{}
	late    []string
}

func newBarrier(n int) *barrier { return &barrier{n: n, all: make(chan struct{})} }

func (b *barrier) wait(who string) {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.all)
	}
	b.mu.Unlock()
	select {
	case <-b.all:
	case <-time.After(2 * time.Second):
		b.mu.Lock()
		b.late = append(b.late, who)
		b.mu.Unlock()
	}
}

type slowForge struct {
	testForge
	b *barrier
}

func (f slowForge) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	if strings.Join(req.Args, " ") == "auth" {
		f.b.wait("gh auth status")
	}
	return f.testForge.RunCLI(ctx, req, stdout, stderr)
}

type slowGit struct {
	*fakeGit
	b *barrier
}

func (g slowGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	if args[0] == "ls-remote" {
		g.b.wait("git ls-remote")
	}
	return g.fakeGit.Run(ctx, dir, env, args...)
}

type slowManager struct {
	*fake.Manager
	b *barrier
}

func (m slowManager) Reachable(ctx context.Context) error {
	m.b.wait("manager reachable")
	return m.Manager.Reachable(ctx)
}

func TestDoctorWaitsOnItsSlowChecksAtOnce(t *testing.T) {
	t.Parallel()
	b := newBarrier(4)
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/widget", RemoteKey: "github.com/acme/widget"}}}
	te := newTestEnv(t, slowManager{m, b})
	te.Env.forge = slowForge{testForge{te}, b}
	te.Git = gitx.Git{R: slowGit{te.git, b}}
	te.Sh = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-lc" {
			b.wait("login shell")
		}
		return nil, nil
	}
	te.Socket = startDaemon(t, nil).socket
	te.run("doctor", "--git")
	if len(b.late) > 0 {
		t.Fatalf("doctor waited on these one after another: %v", b.late)
	}
	if !strings.Contains(te.out.String(), "[ok     ] git fetch without prompts: /src/widget\n") {
		t.Fatalf("%s", te.out)
	}
}

func TestDoctorSkipsTheGitFetchWhenTheManagerIsDown(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{Unreachable: context.DeadlineExceeded, RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/widget", RemoteKey: "github.com/acme/widget"}}}
	te := newTestEnv(t, m)
	te.Socket = startDaemon(t, nil).socket
	te.run("doctor", "--git")
	if strings.Contains(te.out.String(), "git fetch without prompts") {
		t.Fatalf("%s", te.out)
	}
}
