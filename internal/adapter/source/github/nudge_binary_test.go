package ghsource_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// waitingGitHub is GitHub as the Enterprise layout serves it: the viewer
// ana, and ana's acme/api#7, whose review she asked of bob on October 1st;
// a comment posted on it answers fail when set.
type waitingGitHub struct {
	mu    sync.Mutex
	posts []string
	fail  int
}

func (g *waitingGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"no token"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/graphql":
		var body struct {
			Query string `json:"query"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		data := map[string]any{"rateLimit": map[string]any{"cost": 1, "remaining": 4900, "limit": 5000, "resetAt": "2026-10-22T16:00:00Z"},
			"viewer": map[string]any{"login": "ana"}}
		for i := range strings.Count(body.Query, "search(") {
			data[fmt.Sprintf("s%d", i)] = map[string]any{"issueCount": 0, "pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}
		}
		bob := map[string]any{"__typename": "User", "login": "bob"}
		for _, m := range aliasRE.FindAllStringSubmatch(body.Query, -1) {
			data[m[1]] = map[string]any{"pullRequest": map[string]any{"number": 7, "title": "Batch export", "state": "OPEN", "isDraft": false,
				"url": "https://github.com/acme/api/pull/7", "createdAt": "2026-10-01T10:00:00Z", "updatedAt": "2026-10-01T10:00:00Z",
				"headRefName": "feat/batch", "baseRefName": "main", "headRefOid": "h2", "isCrossRepository": false,
				"repository": map[string]any{"nameWithOwner": "acme/api", "visibility": "PRIVATE"}, "author": map[string]any{"login": "ana"},
				"commits": map[string]any{"nodes": []any{}}, "reviews": map[string]any{"nodes": []any{}}, "comments": map[string]any{"nodes": []any{}},
				"reviewRequests": map[string]any{"nodes": []any{map[string]any{"requestedReviewer": bob}}},
				"timelineItems": map[string]any{"nodes": []any{map[string]any{"__typename": "ReviewRequestedEvent", "createdAt": "2026-10-01T10:00:00Z",
					"actor": map[string]any{"__typename": "User", "login": "ana"}, "requestedReviewer": bob}}},
				"headCommit": map[string]any{"nodes": []any{}}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/repos/acme/api/issues/7/comments":
		var in struct{ Body string }
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &in)
		g.mu.Lock()
		g.posts = append(g.posts, in.Body)
		fail := g.fail
		g.mu.Unlock()
		if fail != 0 {
			http.Error(w, `{"message":"bad gateway"}`, fail)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1001, "html_url": "https://github.com/acme/api/pull/7#issuecomment-1001", "body": in.Body,
			"created_at": "2026-10-22T15:00:00Z", "user": map[string]any{"login": "ana"}})
	default:
		http.Error(w, `{"message":"not here"}`, http.StatusNotFound)
	}
}

func (g *waitingGitHub) posted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.posts...)
}

// TestGitHubNudgeDraftsThroughTheOutbox runs the GitHub plugin's binary as
// the proposals job's source and the outbox's publisher: its github-nudge
// rule proposes a nudge on a stalled pull request, which waits for an
// approval as a comment and is not proposed twice; once approved, a 502
// from GitHub fails it as one that may have been posted, so a retry asks
// for a confirmation, the nudge it may have made holds the rule, and
// GitHub saw one post.
func TestGitHubNudgeDraftsThroughTheOutbox(t *testing.T) {
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
	gh := &waitingGitHub{}
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
	units := psource.Units{Codecs: unit.NewRegistry(codecs[unit.GitHub])}
	snaps := psource.NewSnapshots(db.Snapshots())
	m := bundledGitHub(t)
	kind := &psource.Kind{Snapshots: snaps, StateDir: func(id string) string { return filepath.Join(dir, "state", id) },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: units, Config: config.Defaults, Events: sqlite.New(db), Overrides: db,
				Hosted: &psource.Hosted{Profile: ghreporttest.Profile}}
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
	if _, err := c.Identity(ctx); err != nil {
		t.Fatal(err)
	}
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	if _, err := c.Track(ctx, src.TrackRequest{Unit: psource.Wire(ref.Unit())}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Track(ctx, src.TrackRequest{Unit: psource.Wire(ref.Unit()), Follow: true}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	proposals := psource.Proposals{ID: m.ID, Units: units, Kind: kind}
	pass, err := proposals.Proposals(ctx, now)
	props := pass.Proposals
	if err != nil || len(props) != 1 || props[0].Unit != ref.Unit() || !strings.Contains(props[0].Text, "@{github:bob}") {
		t.Fatalf("proposals %+v %v, want the nudge of bob on #7", props, err)
	}
	gs := outboxtest.NewGates(func() time.Time { return now })
	pub := psource.Publisher{ID: m.ID, Source: s, Units: units, Kind: kind}
	svc := gs.Bind(&outbox.Service{Store: db.Drafts(), Clock: pinnedClock(now),
		Effects: func(unit.ProviderID) (port.Effects, error) { return pub, nil }})
	d, err := svc.Propose(ctx, props[0])
	if err != nil {
		t.Fatal(err)
	}
	if d.State != draft.Held || d.Delivery != draft.CommentDelivery {
		t.Fatalf("proposed %+v, want a held comment", d)
	}
	if _, err := svc.Propose(ctx, props[0]); !errors.Is(err, outbox.ErrDuplicate) {
		t.Fatalf("a second proposal of the same pull request: %v", err)
	}
	if sent, failed, err := svc.Send(ctx); sent+failed != 0 || err != nil || len(gh.posted()) != 0 {
		t.Fatalf("sent %d failed %d %v before an approval", sent, failed, err)
	}
	if err := gs.Approve(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	gh.mu.Lock()
	gh.fail = http.StatusBadGateway
	gh.mu.Unlock()
	if sent, failed, err := svc.Send(ctx); sent != 0 || failed != 1 || err != nil {
		t.Fatalf("sent %d failed %d %v", sent, failed, err)
	}
	got, err := db.Drafts().Get(ctx, d.ID)
	if err != nil || got.State != draft.Failed || !strings.HasPrefix(got.Error, "may have been posted") {
		t.Fatalf("the failed draft %+v %v, want one that may have been posted", got, err)
	}
	if _, err := svc.Retry(ctx, d.ID, false); !errors.Is(err, outbox.ErrNeedsConfirm) {
		t.Fatalf("a retry without a confirmation: %v", err)
	}
	if evs, err := sqlite.New(db).UnitEvents(ctx, []unit.Unit{ref.Unit()}, []string{"nudged"}, time.Time{}); err != nil || len(evs) != 1 || evs[0].Data != "@bob" {
		t.Fatalf("the nudged events %+v %v, want the one the post may have made", evs, err)
	}
	if again, err := proposals.Proposals(ctx, now.Add(time.Hour)); err != nil || len(again.Proposals) != 0 {
		t.Fatalf("proposed %+v %v within the nudge's window", again, err)
	}
	if posts := gh.posted(); len(posts) != 1 {
		t.Fatalf("GitHub saw %d posts, want one", len(posts))
	}
}
