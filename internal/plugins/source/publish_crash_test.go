package source_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

const (
	roleEnv  = "PSOURCE_TEST_ROLE"
	postsEnv = "PSOURCE_TEST_POSTS"
)

func TestMain(m *testing.M) {
	if os.Getenv(roleEnv) == "hanging-poster" {
		plugin.Serve(map[plugin.Kind]plugin.Impl{src.Kind: src.New(hangingPoster{Stub: sourcetest.NewStub(), posts: os.Getenv(postsEnv)})})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// hangingPoster posts a draft, recording it in its posts file, and then
// never answers, as a plugin killed after its post left.
type hangingPoster struct {
	*sourcetest.Stub
	posts string
}

func (p hangingPoster) Validate(context.Context, src.Draft) error        { return nil }
func (p hangingPoster) Where(context.Context, src.Draft) (string, error) { return "", nil }
func (p hangingPoster) Place(context.Context, src.Draft) (string, error) { return "", nil }
func (p hangingPoster) Verify(context.Context, src.Draft, time.Time) (src.Evidence, bool, error) {
	return src.Evidence{}, false, nil
}

func (p hangingPoster) Publish(_ context.Context, d src.Draft) (src.Evidence, error) {
	f, err := os.OpenFile(p.posts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return src.Evidence{}, err
	}
	_, _ = f.WriteString(d.Text + "\n")
	_ = f.Close()
	select {}
}

func postsIn(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func TestAPluginKilledDuringPublishLeavesTheDraftMayHavePostedAndUnsentAgain(t *testing.T) {
	posts := filepath.Join(t.TempDir(), "posts")
	t.Setenv(roleEnv, "hanging-poster")
	t.Setenv(postsEnv, posts)
	proc, err := host.ExecLauncher{}.Launch(t.Context(), plugin.Spec{ID: "acme", Path: os.Args[0], Kinds: []plugin.Kind{src.Kind}, Logger: hclog.NewNullLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proc.Kill)
	m, err := manifest.Parse([]byte("id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{name}\"]}\n" + commentingManifest))
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	k := kindOver(t, &change.Log{})
	c, err := proc.Kind(src.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(t.Context(), m, c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Stop(context.Background(), "acme") })
	cdc, err := codec.Of("acme", s.Codec)
	if err != nil {
		t.Fatal(err)
	}
	pub := psource.Publisher{ID: "acme", Source: s, Units: psource.Units{Codecs: unit.NewRegistry(cdc)}, Kind: k}

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	gs := outboxtest.NewGates(func() time.Time { return now })
	svc := gs.Bind(&outbox.Service{Store: db.Drafts(), Clock: pinned(now), Effects: func(unit.ProviderID) (port.Effects, error) { return pub, nil }})
	ctx := context.Background()
	d, err := svc.Propose(ctx, outbox.Proposal{Unit: unit.Unit{Provider: "acme", Key: "t1"}, Purpose: "reply", Text: "on its way",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "acme-reply", Key: "k"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := gs.Approve(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_, _, _ = svc.Send(ctx)
	}()
	eventually(t, "the plugin's post", func() bool { return postsIn(t, posts) == 1 })
	if err := syscall.Kill(proc.(interface{ Pid() int }).Pid(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-sent

	got, err := db.Drafts().Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != draft.Failed || !strings.HasPrefix(got.Error, "may have been posted") {
		t.Fatalf("after the kill: %s %q, want failed as may have been posted", got.State, got.Error)
	}
	if _, err := svc.Retry(ctx, d.ID, false); !errors.Is(err, outbox.ErrNeedsConfirm) {
		t.Fatalf("a retry without confirm: %v", err)
	}
	if _, _, err := svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if n := postsIn(t, posts); n != 1 {
		t.Fatalf("posted %d times", n)
	}
}
