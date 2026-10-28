package ghsource_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// fakeTokenGH is a gh that names a token, the one call the plugin's API
// client makes of it.
const fakeTokenGH = `#!/bin/sh
case "$1 $2" in
  "auth token") echo "test-token" ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac
`

// fakeGitHub is GitHub's GraphQL endpoint as the Enterprise layout serves
// it: the viewer, empty searches, and acme/api#7, open, read in full.
type fakeGitHub struct {
	mu    sync.Mutex
	reads int
}

var aliasRE = regexp.MustCompile(`(pr\d+): repository\(owner: "acme", name: "api"\) \{ pullRequest\(number: 7\)`)

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/graphql" || r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"not here"}`, http.StatusNotFound)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	data := map[string]any{"rateLimit": map[string]any{"cost": 1, "remaining": 4900, "limit": 5000, "resetAt": "2026-10-20T13:00:00Z"},
		"viewer": map[string]any{"login": "ana"}}
	for i := range strings.Count(body.Query, "search(") {
		data[fmt.Sprintf("s%d", i)] = map[string]any{"issueCount": 0, "pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}
	}
	for _, m := range aliasRE.FindAllStringSubmatch(body.Query, -1) {
		g.mu.Lock()
		g.reads++
		g.mu.Unlock()
		data[m[1]] = map[string]any{"pullRequest": map[string]any{"number": 7, "title": "Batch export", "state": "OPEN", "isDraft": false,
			"url": "https://github.com/acme/api/pull/7", "createdAt": "2026-10-19T10:00:00Z", "updatedAt": "2026-10-20T09:00:00Z",
			"headRefName": "feat/batch", "baseRefName": "main", "headRefOid": "h2", "isCrossRepository": false,
			"repository": map[string]any{"nameWithOwner": "acme/api", "visibility": "PRIVATE"}, "author": map[string]any{"login": "bruno"},
			"commits": map[string]any{"nodes": []any{}}, "reviews": map[string]any{"nodes": []any{}}, "comments": map[string]any{"nodes": []any{}},
			"reviewRequests": map[string]any{"nodes": []any{}}, "timelineItems": map[string]any{"nodes": []any{}}, "headCommit": map[string]any{"nodes": []any{}}}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func bundledGitHub(t *testing.T) *manifest.Manifest {
	t.Helper()
	ms, err := bundled.Manifests()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ms, func(m *manifest.Manifest) bool { return m.ID == string(unit.GitHub) })
	if i < 0 {
		t.Fatal("no bundled github manifest")
	}
	return ms[i]
}

func TestTheGitHubBinaryIngestsAndReportsAFollowedPullRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	bin := buildPluginBinary(t)
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
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(fakeTokenGH), 0o755); err != nil {
		t.Fatal(err)
	}
	gh := &fakeGitHub{}
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)

	db, err := sqlite.Open(filepath.Join(dir, "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	codecs, err := bundled.Codecs()
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(db)
	snaps := psource.NewSnapshots(db.Snapshots())
	m := bundledGitHub(t)
	kind := &psource.Kind{Snapshots: snaps, StateDir: func(id string) string { return filepath.Join(dir, "state", id) },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: psource.Units{Codecs: unit.NewRegistry(codecs[unit.GitHub])},
				Config: config.Defaults, Events: sqlite.New(db), Marks: db, Drafts: db, Overrides: db, Reported: ghsource.Reported(st)}
		}}
	proc, err := host.ExecLauncher{}.Launch(context.Background(), plugin.Spec{ID: m.ID, Path: bin, Kinds: m.Kinds,
		Env:      []string{"PATH=" + ghDir, "GH_HOST=" + srv.URL, "GH_CONFIG_DIR=" + dir, plugin.BrandEnv + "=" + brand.Name},
		Checksum: sum[:], Logger: hclog.NewNullLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proc.Kill)
	k, err := proc.Kind(src.Kind)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := kind.Start(ctx, m, k); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kind.Stop(context.Background(), m.ID) })
	c, err := kind.Client(m.ID)
	if err != nil {
		t.Fatal(err)
	}

	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	u := psource.Wire(ref.Unit())
	read, err := c.Track(ctx, src.TrackRequest{Unit: u})
	if err != nil || read.Unit != u || read.State != "OPEN" {
		t.Fatalf("read %+v %v", read, err)
	}
	if _, ok, err := snaps.Last(ctx, ref.Unit()); err != nil || ok {
		t.Fatalf("a read not followed was reported (%v)", err)
	}
	if _, err := c.Track(ctx, src.TrackRequest{Unit: u, Follow: true}); err != nil {
		t.Fatal(err)
	}
	snap, ok, err := snaps.Last(ctx, ref.Unit())
	if err != nil || !ok || snap.Rev == 0 {
		t.Fatalf("the followed pull request's snapshot %+v %v %v", snap, ok, err)
	}
	row, ok, err := st.PR(ctx, ref)
	if err != nil || !ok || row.Title != "Batch export" || row.HeadOid != "h2" || !slices.Contains(row.Reasons, "tracked") {
		t.Fatalf("the host keeps %+v %v %v", row, ok, err)
	}
	if evs, err := sqlite.New(db).UnitEvents(ctx, []unit.Unit{ref.Unit()}, []string{"tracked", "followed"}, time.Time{}); err != nil || len(evs) != 2 {
		t.Fatalf("the host's events %+v %v, want the track and its flip", evs, err)
	}

	res, err := c.Ingest(ctx, src.IngestRequest{Job: "fast"})
	if err != nil || res.Summary == "" {
		t.Fatalf("fast: %+v %v", res, err)
	}
	gh.mu.Lock()
	reads := gh.reads
	gh.mu.Unlock()
	if reads == 0 {
		t.Fatal("the plugin never read the pull request from GitHub")
	}
}
