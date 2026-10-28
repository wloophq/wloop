package ghsource_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// commentingGitHub is GitHub as the Enterprise layout serves it: the
// viewer, and the comments posted on acme/api#7.
type commentingGitHub struct {
	mu       sync.Mutex
	comments []string
}

func (g *commentingGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"no token"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/graphql":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{"login": "ana"},
			"rateLimit": map[string]any{"cost": 1, "remaining": 4900, "limit": 5000, "resetAt": "2026-10-20T13:00:00Z"}}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/repos/acme/api/issues/7/comments":
		var in struct{ Body string }
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &in)
		g.mu.Lock()
		g.comments = append(g.comments, in.Body)
		n := len(g.comments)
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1000 + n, "html_url": "https://github.com/acme/api/pull/7#issuecomment-1", "body": in.Body,
			"created_at": "2026-10-22T15:00:00Z", "user": map[string]any{"login": "ana"}})
	default:
		http.Error(w, `{"message":"not here"}`, http.StatusNotFound)
	}
}

func (g *commentingGitHub) posted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.comments...)
}

// TestAnApprovedDraftIsPostedThroughTheGitHubPlugin runs the GitHub
// plugin's binary as the outbox's publisher: the draft a person approves is
// posted as a comment on its pull request, the one they reject never is,
// and the delivery each carries is the one the manifest names for its kind.
func TestAnApprovedDraftIsPostedThroughTheGitHubPlugin(t *testing.T) {
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
	gh := &commentingGitHub{}
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
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	gs := outboxtest.NewGates(func() time.Time { return now })
	pub := psource.Publisher{ID: m.ID, Source: s, Units: units, Kind: kind}
	svc := gs.Bind(&outbox.Service{Store: db.Drafts(), Clock: pinnedClock(now),
		Effects: func(unit.ProviderID) (port.Effects, error) { return pub, nil }})
	u := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	propose := func(key, text string) draft.Draft {
		t.Helper()
		d, err := svc.Propose(ctx, outbox.Proposal{Unit: u, Purpose: "nudge", Text: text,
			Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: key}})
		if err != nil {
			t.Fatal(err)
		}
		if d.State != draft.Held || d.Delivery != draft.CommentDelivery {
			t.Fatalf("proposed %+v, want a held comment", d)
		}
		return d
	}
	approved := propose("a", "Friendly nudge: could @bruno take a look?")
	rejected := propose("b", "Rejected text that never posts")
	if err := gs.Discard(ctx, rejected.ID); err != nil {
		t.Fatal(err)
	}
	if sent, failed, err := svc.Send(ctx); sent+failed != 0 || err != nil || len(gh.posted()) != 0 {
		t.Fatalf("sent %d failed %d %v before an approval; posted %q", sent, failed, err, gh.posted())
	}
	if err := gs.Approve(ctx, approved.ID); err != nil {
		t.Fatal(err)
	}
	if sent, failed, err := svc.Send(ctx); sent != 1 || failed != 0 || err != nil {
		t.Fatalf("sent %d failed %d %v", sent, failed, err)
	}
	if got := gh.posted(); len(got) != 1 || got[0] != approved.Text {
		t.Fatalf("GitHub got %q, want the approved draft's text alone", got)
	}
	got, err := db.Drafts().Get(ctx, approved.ID)
	if err != nil || got.State != draft.Sent || got.Evidence.Permalink == "" {
		t.Fatalf("the approved draft %+v %v", got, err)
	}
	if got, err := db.Drafts().Get(ctx, rejected.ID); err != nil || got.State == draft.Sent {
		t.Fatalf("the rejected draft %+v %v", got, err)
	}
	if sent, _, err := svc.Send(ctx); sent != 0 || err != nil || len(gh.posted()) != 1 {
		t.Fatalf("a second send: %d %v, posted %q", sent, err, gh.posted())
	}
}

type pinnedClock time.Time

func (c pinnedClock) Now() time.Time { return time.Time(c) }
