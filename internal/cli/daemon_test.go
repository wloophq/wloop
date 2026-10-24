package cli

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/config"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

var _ menubar.Daemon = (*client.Client)(nil)

var daemonNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

var r7 = github.Ref{Owner: "acme", Repo: "api", Number: 7}

type testDaemon struct {
	socket string
	store  *inventory.Store

	mu       sync.Mutex
	requests []string
}

func (d *testDaemon) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.requests...)
}

// holdByCap holds today's automatic round of acme/api#7 by the cap.
func (d *testDaemon) holdByCap(t *testing.T) {
	t.Helper()
	if _, err := d.store.AppendEvents(context.Background(), []dinv.Event{{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: daemonNow.Add(time.Second), Data: "daily cap"}}); err != nil {
		t.Fatal(err)
	}
}

// startDaemon serves a daemon over a socket in a short temporary directory,
// with one open pull request in its store.
func startDaemon(t *testing.T, actions daemon.Actions) *testDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	stDB, err := sqlitetest.Open(filepath.Join(dir, "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(stDB)
	t.Cleanup(func() { stDB.Close() })
	ctx := context.Background()
	if err := st.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h2",
		ReviewRequests: []string{"me"}, Reasons: []string{"review-requested"}, URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	d := &testDaemon{socket: filepath.Join(dir, "daemon.sock"), store: st}
	srv := daemon.New(withRows(daemon.Deps{
		Cfg: config.Defaults(), Version: "test", Actions: actions,
		Now:    func() time.Time { return daemonNow },
		Viewer: func(context.Context) (string, []string, error) { return "me", nil, nil },
		Sweep:  daemon.SweepFunc(func(context.Context) (string, string, error) { return "", "", nil }),
		Log:    t.Logf,
		GTD:    coregtd.New(coregtd.Deps{Store: stDB.Entries(), Clock: clockFunc(func() time.Time { return daemonNow })}),
	}, st))
	h := srv.Handler()
	l, err := net.Listen("unix", d.socket)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.requests = append(d.requests, r.Method+" "+r.URL.Path)
		d.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	hs.Listener = l
	hs.Start()
	t.Cleanup(hs.Close)
	return d
}

func TestClientTalksToTheDaemon(t *testing.T) {
	d := startDaemon(t, nil)
	ctx := context.Background()
	c := client.New(d.socket)
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Unit != r7.Unit() {
		t.Fatalf("rows = %+v", rows)
	}
	h, err := c.Health(ctx)
	if err != nil || h.Version != "test" {
		t.Fatalf("health = %+v, %v", h, err)
	}
	if _, err := c.Action(ctx, r7.Unit(), "pause", api.ActionRequest{}); err != nil {
		t.Fatal(err)
	}
	o, _ := d.store.Override(ctx, r7)
	if !o.Hold {
		t.Fatalf("not held: %+v", o)
	}
	if err := c.Trigger(ctx, "sweep"); err != nil {
		t.Fatal(err)
	}
	if err := c.Trigger(ctx, "nope"); err == nil {
		t.Fatal("triggered a job that does not exist")
	}
	if _, err := c.Action(ctx, r7.Unit(), "review", api.ActionRequest{Source: "cli"}); err == nil || err.Error() != "daemon: actions unavailable" {
		t.Fatalf("err = %v", err)
	}
	if err := client.New("/tmp/nope.sock").Ping(ctx); err == nil {
		t.Fatal("pinged a daemon that does not exist")
	}
}
