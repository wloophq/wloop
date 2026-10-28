package cli

import (
	"context"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/source"
	slackstore "github.com/wloophq/wloop/plugins/slack/store"
)

// startSlackDaemon serves a daemon wired as serve wires it, with the real
// Slack plugin over te's fake Keychain and fake Slack, the Slack module
// relaying to it and a store of its own.
func startSlackDaemon(t *testing.T, te *slackTestEnv) *sqlite.DB {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := sqlitetest.Open(filepath.Join(dir, "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stop := te.servePlugins(ctx, db, te.cfg, t.Logf)
	t.Cleanup(func() {
		cancel()
		stop()
	})
	eventually(t, "the slack plugin running", func() bool {
		_, err := te.pluginKind.Client(string(slackPlugin))
		return err == nil
	})
	st := inventory.New(db)
	mod, err := te.slackRelay(te.cfg, source.Deps{DB: db, Inventory: st, Log: t.Logf, Clock: clockFunc(time.Now)})
	if err != nil {
		t.Fatal(err)
	}
	d := withSources(withStore(daemon.Deps{Cfg: Defaults(), Version: "test", Now: time.Now, Log: t.Logf}, st), plugins, []source.Module{mod})
	d.Ingest = nil
	d.PluginAction = te.pluginAction
	sock := filepath.Join(dir, "daemon.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(daemon.New(d).Handler())
	hs.Listener = l
	hs.Start()
	t.Cleanup(hs.Close)
	te.Socket = sock
	return db
}

// pluginStore is the own store of the Slack plugin running under stateDir.
func pluginStore(t *testing.T, stateDir string) *slackstore.DB {
	t.Helper()
	db, err := slackstore.Open(context.Background(), filepath.Join(stateDir, "plugins", string(slackPlugin)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestFollowSendsTrackForTheThread(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	startSlackDaemon(t, te)
	slack := pluginStore(t, te.StateDir).Slack()
	te.srv.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []any{map[string]any{"type": "message", "ts": v.Get("ts"), "thread_ts": "1700000000.000100", "user": "U001"}}}
	})
	const object = "C000/1700000000.000100"
	watched := func() bool {
		o, ok, err := slack.Object(context.Background(), object)
		if err != nil {
			t.Fatal(err)
		}
		return ok && o.Watched
	}
	for _, c := range []struct {
		verb, link string
		watched    bool
	}{
		{"follow", "https://acme.slack.com/archives/C000/p1700000000000100", true},
		{"unfollow", "https://acme.slack.com/archives/C000/p1700000000000100", false},
		{"follow", "https://acme.slack.com/archives/C000/p1700000050000200?thread_ts=1700000000.000100&cid=C000", true},
		{"follow", "https://acme.slack.com/archives/C000/p1700000050000200", true},
		{"unfollow", "https://acme.slack.com/archives/C000/p1700000050000200", false},
		{"follow", "C000/1700000050.000200", true},
		{"unfollow", "C000/1700000050.000200", false},
	} {
		te.out.Reset()
		te.err.Reset()
		if code := te.run("slack", c.verb, c.link); code != 0 {
			t.Fatalf("slack %s %s: exit %d: %s", c.verb, c.link, code, te.err)
		}
		if !strings.Contains(te.out.String(), "slack "+c.verb+" T000/"+object+": done") {
			t.Fatalf("slack %s: %q", c.verb, te.out)
		}
		if watched() != c.watched {
			t.Fatalf("after slack %s %s: watched %v", c.verb, c.link, !c.watched)
		}
	}
	if n := len(te.srv.Calls("conversations.replies")); n != 6 {
		t.Fatalf("conversations.replies calls %d, want one per ref or permalink without thread_ts", n)
	}
	assertNoSlackToken(t, te.out.String()+te.err.String())
}

func TestFollowRefusesWhatIsNotAPermalink(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	startSlackDaemon(t, te)
	if code := te.run("slack", "follow", "https://github.com/acme/api/pull/7"); code == 0 || !strings.Contains(te.err.String(), "not a Slack message ref (<conversation>/<ts>) or permalink") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("slack", "follow"); code == 0 {
		t.Fatal("follow without a permalink succeeded")
	}
}
