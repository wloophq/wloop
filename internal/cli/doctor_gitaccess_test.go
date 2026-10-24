package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/daemon"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// checkoutOf is a checkout whose origin is remote, as RemoteURL reads it.
func checkoutOf(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[remote \"origin\"]\n\turl = "+remote+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type noGit struct{ t *testing.T }

func (g noGit) Run(_ context.Context, dir string, _ []string, args ...string) ([]byte, error) {
	g.t.Errorf("doctor ran git -C %s %s", dir, strings.Join(args, " "))
	return nil, nil
}

func TestDoctorRunsNoGitByDefault(t *testing.T) {
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: checkoutOf(t, "git@github.com:acme/widget.git"), RemoteKey: "github.com/acme/widget"}}}
	te := newTestEnv(t, m)
	te.Git = gitx.Git{R: noGit{t}}
	te.Socket = startDaemon(t, nil).socket
	te.run("doctor")
	out := te.out.String()
	if strings.Contains(out, "git fetch without prompts") {
		t.Fatalf("the default doctor checked git live:\n%s", out)
	}
	if !strings.Contains(out, "  [info   ] git without prompts: no git fetch of the daemon seen yet; run '"+te.command()+" doctor --git' to check now\n") {
		t.Fatalf("%s", out)
	}
}

func TestDoctorGitChecksLiveAndRecordsIt(t *testing.T) {
	dir := checkoutOf(t, "git@github.com:acme/widget.git")
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: dir, RemoteKey: "github.com/acme/widget"}}}
	te := newTestEnv(t, m)
	d := startDaemon(t, nil)
	te.Socket = d.socket
	te.run("doctor", "--git")
	out := te.out.String()
	if !strings.Contains(out, "  [ok     ] git fetch without prompts: "+dir+"\n") ||
		!strings.Contains(out, "  [ok     ] git without prompts (github.com): just now · ls-remote (doctor --git)\n") {
		t.Fatalf("%s", out)
	}
	h, err := client.New(d.socket).Health(context.Background())
	if err != nil || len(h.GitAccess) != 1 || h.GitAccess[0].Host != "github.com" || h.GitAccess[0].Last.Op != "ls-remote (doctor --git)" || h.GitAccess[0].LastOK == nil {
		t.Fatalf("%+v %v", h.GitAccess, err)
	}
}

func TestServeRecordsTheFetchOfARound(t *testing.T) {
	dir := checkoutOf(t, "https://github.com/acme/api")
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: dir, RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	log := &daemon.GitAccessLog{Store: te.Store}
	te.observeGit(context.Background(), log, t.Logf)
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	te.gitFlush()
	hosts, err := log.Hosts(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].Host != "github.com" || hosts[0].Last.Op != "fetch of acme/api#7" || !hosts[0].Last.At.Equal(testNow) {
		t.Fatalf("%+v %v", hosts, err)
	}
}

func TestCLIFetchRecordsNothing(t *testing.T) {
	dir := checkoutOf(t, "https://github.com/acme/api")
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: dir, RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	log := &daemon.GitAccessLog{Store: te.Store}
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if hosts, err := log.Hosts(context.Background()); err != nil || len(hosts) != 0 {
		t.Fatalf("%+v %v", hosts, err)
	}
}

func TestSlowStoreDoesNotBlockTheCaller(t *testing.T) {
	release := make(chan struct{})
	rec := newAccessRecorder(context.Background(), func(context.Context, api.GitAccessRecord) { <-release })
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			rec.submit(api.GitAccessRecord{Host: "github.com"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("submit blocked on a slow store")
	}
	close(release)
	rec.flush()
}

func TestGitAccessChecks(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	at := func(ago time.Duration) time.Time { return testNow.Add(-ago) }
	cmd := te.command()
	for name, c := range map[string]struct {
		g    api.GitAccess
		want check
	}{
		"recent success": {
			api.GitAccess{Host: "github.com", Last: api.GitAccessOutcome{At: at(12 * time.Minute), Op: "fetch of acme/widgets#4593"}},
			check{markOK, "git without prompts (github.com)", "12m ago · fetch of acme/widgets#4593", ""},
		},
		"prompt problem, even old": {
			api.GitAccess{Host: "github.com", Last: api.GitAccessOutcome{At: at(30 * time.Hour), Op: "fetch of acme/api#7", Error: "git fetch -q origin: git@github.com: Permission denied (publickey)."}},
			check{markMissing, "git without prompts (github.com)", "git fetch -q origin: git@github.com: Permission denied (publickey). (fetch of acme/api#7, 30h ago)", sshFix},
		},
		"network error": {
			api.GitAccess{Host: "github.com", Last: api.GitAccessOutcome{At: at(5 * time.Minute), Op: "fetch of acme/api", Error: "ssh: Could not resolve hostname github.com"},
				LastOK: &api.GitAccessOutcome{At: at(3 * time.Hour), Op: "fetch of acme/api#7"}},
			check{markInfo, "git without prompts (github.com)", "fetch of acme/api failed 5m ago: ssh: Could not resolve hostname github.com; last success 3h ago", ""},
		},
		"fresh success": {
			api.GitAccess{Host: "github.com", Last: api.GitAccessOutcome{At: at(10 * time.Second), Op: "fetch of acme/api"}},
			check{markOK, "git without prompts (github.com)", "just now · fetch of acme/api", ""},
		},
		"stale": {
			api.GitAccess{Host: "github.com", Last: api.GitAccessOutcome{At: at(26 * time.Hour), Op: "fetch of acme/api#7"}},
			check{markInfo, "git without prompts (github.com)", "not seen in 26h; run '" + cmd + " doctor --git' to check now", ""},
		},
	} {
		got := te.gitAccessChecks([]api.GitAccess{c.g}, nil)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %+v\nwant %+v", name, got, c.want)
		}
	}

	stale := []api.GitAccess{{Host: "github.com", Last: api.GitAccessOutcome{At: at(30 * time.Hour), Op: "fetch of acme/api#7"}}}
	live := &api.GitAccessRecord{Host: "github.com", GitAccessOutcome: api.GitAccessOutcome{At: testNow, Op: liveGitOp}}
	if got := te.gitAccessChecks(stale, live); len(got) != 1 || got[0].mark != markOK || got[0].info != "just now · ls-remote (doctor --git)" {
		t.Errorf("a live check stands over an old fetch: %+v", got)
	}
}
