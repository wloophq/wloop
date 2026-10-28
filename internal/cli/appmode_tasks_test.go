package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type recNotifier struct {
	silentNotifier
	mu        sync.Mutex
	posted    []menubar.Notification
	withdrawn []string
}

func (n *recNotifier) Post(p menubar.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.posted = append(n.posted, p)
	return nil
}

func (n *recNotifier) Withdraw(ids []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.withdrawn = append(n.withdrawn, ids...)
}

func (n *recNotifier) seen() ([]menubar.Notification, []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.posted), slices.Clone(n.withdrawn)
}

func TestTheMenubarFollowsARealDaemonsInbox(t *testing.T) {
	te := newServeEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := inventory.New(db).SetViewer(ctx, unit.GitHub, "octocat"); err != nil {
		t.Fatal(err)
	}
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	seedTodo(t, db, u)
	db.Close()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, te.Env, []string{"--no-jobs", "--mcp-port", strconv.Itoa(freePort(t))})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	waitHealth(t, te.Env, "the daemon", func(api.Health) bool { return true })
	if err := os.WriteFile(filepath.Join(te.StateDir, httpInfoFile), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tray, notes := &recTray{}, &recNotifier{}
	var opened []string
	var mu sync.Mutex
	app := menubar.New(menubar.Config{Daemon: client.New(te.Socket), Tray: tray, Notifier: notes,
		OpenURL:    func(u string) error { mu.Lock(); opened = append(opened, u); mu.Unlock(); return nil },
		CockpitURL: func(route string) (string, error) { return cockpitURL(te.StateDir, route) },
		Quit:       func() {}, Now: te.Now, Log: t.Logf})
	appCtx, stopApp := context.WithCancel(ctx)
	appDone := make(chan struct{})
	go func() { _ = app.Run(appCtx); close(appDone) }()
	defer func() { stopApp(); <-appDone }()

	eventually(t, "the Inbox entry's notification and badge", func() bool {
		posted, _ := notes.seen()
		return len(posted) == 1 && tray.title() == "PR 1" && tray.has("Inbox (1)")
	})
	posted, _ := notes.seen()
	n := posted[0]
	cat, task, ok := api.ParseNotificationID(n.ID)
	if !ok || cat != api.CategoryInbox {
		t.Fatalf("posted %+v", n)
	}
	app.Respond(ctx, menubar.Response{ID: n.ID, Action: menubar.ActionOpen})
	mu.Lock()
	if want := "http://127.0.0.1:4321/?token=tok#task=" + task; !slices.Equal(opened, []string{want}) {
		t.Errorf("opened %v, want %s", opened, want)
	}
	mu.Unlock()

	if _, err := client.New(te.Socket).TaskDo(ctx, task, "next", api.TaskRequest{Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the withdrawal once the entry left the Inbox", func() bool {
		_, withdrawn := notes.seen()
		return slices.Equal(withdrawn, []string{n.ID}) && tray.title() == "PR 0" && tray.has("Inbox (0)")
	})
}
