package cli

import (
	"context"
	"crypto/sha256"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/automation"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
)

// tokenGH is a gh that names a token, the one call the GitHub plugin's API
// client makes of it.
const tokenGH = `#!/bin/sh
case "$1 $2" in
  "auth token") echo "test-token" ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac
`

// buildGitHubPlugin builds the GitHub plugin's binary.
func buildGitHubPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./"+gitHubPluginMain(t))
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// gitHubPluginMain is the plugin's main package, the one directory under
// plugins/github/cmd; its name carries the default brand, not the one built.
func gitHubPluginMain(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("plugins", "github", "cmd")
	entries, err := os.ReadDir(filepath.Join("..", "..", dir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("%s: %v %d entries", dir, err, len(entries))
	}
	return filepath.Join(dir, entries[0].Name())
}

// enterpriseGitHub serves te's fake GitHub over HTTP as GitHub Enterprise
// lays it out, which the plugin binary reaches through GH_HOST.
func enterpriseGitHub(te *testEnv) http.Handler {
	rt := apitest.RunnerTransport{R: quietGH{te.gh}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.URL.Path = strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api"), "/v3")
		r.RequestURI = ""
		resp, err := rt.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

// launchGitHubBinary runs the GitHub plugin's binary over te's store and fake
// GitHub, with the broker serve gives it: the host's events, marks, round
// state, books and workspaces.
func launchGitHubBinary(t *testing.T, te *testEnv) *src.Client {
	t.Helper()
	bin := buildGitHubPlugin(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	dir := t.TempDir()
	ghDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(tokenGH), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(enterpriseGitHub(te))
	t.Cleanup(srv.Close)
	m := bundledGitHubManifest(t)
	kind := &psource.Kind{Snapshots: psource.NewSnapshots(te.db.Snapshots()), StateDir: func(id string) string { return filepath.Join(dir, "state", id) },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			b := &psource.Broker{ID: id, Manifest: s, Units: psource.Units{Codecs: te.unitCodecs()}, Snapshots: psource.NewSnapshots(te.db.Snapshots()),
				Config: te.currentConfig, Events: sqlite.New(te.db), Marks: te.db, Drafts: te.db, Overrides: te.db, Workspaces: te.Env.workspaceListing,
				Paused: te.Env.roundsPaused, Rounds: te.Env.storedRoundState, Books: sqlite.New(te.db).Books,
				Hosted: &psource.Hosted{Profile: pluginProfile(id, te.Env.currentConfig)}}
			return b
		}}
	proc, err := host.ExecLauncher{}.Launch(context.Background(), plugin.Spec{ID: m.ID, Path: bin, Kinds: m.Kinds,
		Env:      []string{"PATH=" + ghDir, "GH_HOST=" + srv.URL, "GH_CONFIG_DIR=" + dir, plugin.BrandEnv + "=" + brand.Name},
		Checksum: sum[:], Logger: hclog.New(&hclog.LoggerOptions{Output: testWriter{t}, Level: hclog.Error})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proc.Kill)
	k, err := proc.Kind(src.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := kind.Start(context.Background(), m, k); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kind.Stop(context.Background(), m.ID) })
	c, err := kind.Client(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestARoundPlansLaunchesAndSendsThroughTheGitHubBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	te := sc.te
	c := launchGitHubBinary(t, te)
	ctx := context.Background()
	u := psource.Wire(sc.ref.Unit())
	if _, err := c.Action(ctx, src.ActionRequest{Unit: u, Action: ghsource.ActionRefresh}); err != nil {
		t.Fatal(err)
	}
	te.gh.hosted = true
	if _, err := c.Ingest(ctx, src.IngestRequest{Job: githubJobFast}); err != nil {
		t.Fatal(err)
	}
	key := sc.ref.String()
	stored, err := te.Rounds.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sc.env.roundStateOf(ctx, sc.ref.Unit(), stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.Plan(ctx, u, st)
	if err != nil || len(plan.Rounds) != 1 || plan.Rounds[0].Name != roundFollowup || plan.Rounds[0].Workspace.ID != "ws-rev" || plan.Rounds[0].OnSent[ghsource.MarkReviewed] == "" {
		t.Fatalf("the binary's plan %+v %v", plan, err)
	}
	r.steps.d.Planners = map[unit.ProviderID]roundPlanner{unit.GitHub: c}
	got, err := r.run(t, automation.RunOpts{})
	if err != nil || !strings.HasPrefix(got, "sent the follow-up: ") {
		t.Fatalf("run = %q, %v", got, err)
	}
	if len(sc.ws.Prompts) != 1 {
		t.Fatalf("prompts %+v", sc.ws.Prompts)
	}
	saved := te.loadRounds(t)
	if !maps.Equal(saved.PRs[key].Marks, plan.Rounds[0].OnSent) || saved.Days[round.DayKey(testNow)].PerPR[key] != 1 {
		t.Fatalf("marks %v, want the plan's %v; rounds today %d", saved.PRs[key].Marks, plan.Rounds[0].OnSent, saved.Days[round.DayKey(testNow)].PerPR[key])
	}
	again, err := c.Plan(ctx, u, func() src.RoundState {
		st, err := sc.env.roundStateOf(ctx, sc.ref.Unit(), saved, nil)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}())
	if err != nil || len(again.Rounds) != 0 {
		t.Fatalf("a sent follow-up is planned again: %+v %v", again, err)
	}
}

// testWriter logs what the plugin process writes to t.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
