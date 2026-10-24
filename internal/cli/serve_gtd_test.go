package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/source/github/ghsourcetest"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func withIdentity(p source.Plugin, who items.Person) source.Plugin {
	build := p.Build
	p.Build = func(cfg config.Config, d source.Deps) (source.Module, error) {
		m, err := build(cfg, d)
		m.Identity = func(context.Context) (items.Person, error) { return who, nil }
		return m, err
	}
	return p
}

func TestMeFromEveryEnabledModule(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := inventory.New(db)
	ctx := context.Background()
	gh := plugins[slices.IndexFunc(plugins, func(p source.Plugin) bool { return p.ID == unit.GitHub })]
	thread := withIdentity(threadModule(unit.Unit{Provider: "thread", Key: "C1 0001"}), items.Person{Key: "slack:U000AAA", Aliases: []string{"email:me@example.test"}})
	off := thread
	off.ID = "off"
	off = withIdentity(off, items.Person{Key: "off:someone"})
	cfg := config.Defaults()
	no := false
	cfg.Plugins = map[string]config.Plugin{"off": {Enabled: &no}}
	mods, err := buildSources([]source.Plugin{gh, thread, off}, cfg, source.Deps{DB: db, Inventory: st, Clock: clockFunc(te.Now)})
	if err != nil {
		t.Fatal(err)
	}
	d := te.serveGTD(daemon.Deps{}, db, cfg, mods, func(string, ...any) {})
	if _, err := d.GTDMe(ctx); err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("no viewer stored yet: %v", err)
	}
	if got := te.gtdMe.get().Keys; !slices.Equal(got, []string{"slack:U000AAA", "email:me@example.test"}) {
		t.Fatalf("before the viewer: %v", got)
	}
	if err := st.SetViewer(ctx, unit.GitHub, "octocat"); err != nil {
		t.Fatal(err)
	}
	grew, err := d.GTDMe(ctx)
	if err != nil || !grew {
		t.Fatalf("grew %v, %v", grew, err)
	}
	got := te.gtdMe.get().Keys
	if !slices.Contains(got, "github:octocat") || !slices.Contains(got, "slack:U000AAA") || slices.Contains(got, "off:someone") {
		t.Fatalf("me %v", got)
	}
}

// derivedRig stores each case's card and reconciles its derived items through the real ledger, then Syncs.
type derivedRig struct {
	db    *sqlite.DB
	svc   *coregtd.Service
	asked map[unit.Unit]bool
}

func newDerivedRig(t *testing.T, cases []ghsourcetest.Case) derivedRig {
	t.Helper()
	ctx := context.Background()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := clockFunc(func() time.Time { return ghsourcetest.Now })
	ledger := &coreitems.Ledger{Store: db.Items(), Clock: clock}
	me := gtd.Me{Keys: []string{"github:" + ghsourcetest.Viewer}}
	svc := coregtd.New(coregtd.Deps{Store: db.Entries(), Items: ledger, Me: me, Clock: clock, StaleWaitingDays: 2,
		Facts: coregtd.CardFacts{Cards: db.Cards(), Me: func() gtd.Me { return me }}})
	r := derivedRig{db: db, svc: svc, asked: map[unit.Unit]bool{}}
	var units []unit.Unit
	for i, c := range cases {
		u := c.Row.Unit
		units = append(units, u)
		cd, err := ghsource.Card(ctx, c.Row)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Cards().Save(ctx, cd, ghsourcetest.Now); err != nil {
			t.Fatal(err)
		}
		at := unit.Anchor{Unit: u, Ref: "row:" + strconv.Itoa(i+1)}
		if _, err := ledger.Reconcile(ctx, u, ghsource.Items(c.Row), at, items.Superseded); err != nil {
			t.Fatal(err)
		}
		if asked(c.Row) {
			r.asked[u] = true
			if _, err := svc.Open(ctx, coregtd.OpenRequest{Kind: gtd.KindMessage, Title: "the agent asks", Unit: &u}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := svc.Sync(ctx, units); err != nil {
		t.Fatal(err)
	}
	return r
}

func asked(r api.Row) bool {
	k := situation.Kind(r.Kind)
	return k == situation.KindLoopWaiting || k == situation.KindLoopStopped
}

func (r derivedRig) entries(t *testing.T, u unit.Unit) []gtd.Entry {
	t.Helper()
	es, err := r.db.Entries().EntriesOf(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func viewerItem(r api.Row) bool {
	return slices.ContainsFunc(ghsource.Items(r), func(it items.Item) bool {
		return slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return p.Key == "github:"+ghsourcetest.Viewer })
	})
}

func TestEveryNeedsMeRowHasAnInboxOrNextEntry(t *testing.T) {
	cases := ghsourcetest.Cases()
	r := newDerivedRig(t, cases)
	needs, muted, questions := 0, 0, 0
	for _, c := range cases {
		es := r.entries(t, c.Row.Unit)
		in := func(states ...gtd.State) bool {
			return slices.ContainsFunc(es, func(e gtd.Entry) bool { return slices.Contains(states, e.State) })
		}
		switch {
		case r.asked[c.Row.Unit]:
			questions++
			if len(es) != 1 || es[0].Kind != gtd.KindMessage || es[0].State != gtd.Inbox {
				t.Errorf("%s asked: %+v", c.Name, es)
			}
		case situation.Kind(c.Row.Kind) == situation.KindMuted && viewerItem(c.Row):
			muted++
			if !in(gtd.Scheduled) || in(gtd.Inbox, gtd.Next) {
				t.Errorf("%s muted: %+v", c.Name, es)
			}
		case slices.Contains(c.Row.Views, api.ViewNeedsMe):
			needs++
			if !in(gtd.Inbox, gtd.Next) {
				t.Errorf("%s needs me: %+v", c.Name, es)
			}
		}
	}
	if needs == 0 || muted == 0 || questions == 0 {
		t.Fatalf("compared %d needs-me, %d muted, %d asked rows", needs, muted, questions)
	}
	t.Logf("%d needs-me, %d muted, %d asked rows of %d", needs, muted, questions, len(cases))
}

func TestCardOnlyRowsHaveNoEntry(t *testing.T) {
	cases := ghsourcetest.Cases()
	r := newDerivedRig(t, cases)
	seen := map[string]int{}
	for _, c := range cases {
		if r.asked[c.Row.Unit] || len(ghsource.Items(c.Row)) > 0 {
			continue
		}
		base, _, _ := strings.Cut(c.Name, "/")
		seen[base]++
		if es := r.entries(t, c.Row.Unit); len(es) != 0 {
			t.Errorf("%s derives no item, has entries %+v", c.Name, es)
		}
	}
	for _, base := range []string{"loop_working", "draft", "approved_by_viewer"} {
		if seen[base] == 0 {
			t.Errorf("no %s row derives nothing: %v", base, seen)
		}
	}
}

// seedTodo stores a to-do on u, waiting on octocat, as the user's.
func seedTodo(t *testing.T, db *sqlite.DB, u unit.Unit) {
	t.Helper()
	l := &coreitems.Ledger{Store: db.Items(), Links: db.Links(), Enabled: func(unit.ProviderID) bool { return true }}
	_, err := l.Apply(context.Background(), items.User, []items.Op{{Op: items.OpCreate, Unit: u, Item: "i1",
		Anchor: unit.Anchor{Ref: "user:1"}, Fields: &items.Fields{Kind: ptrTo(items.Todo), Summary: ptrTo("review it"),
			WaitingOn: &[]items.Person{{Key: "github:octocat", Name: "octocat"}}}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSyncRunsUnderNoJobs(t *testing.T) {
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
	c := client.New(te.Socket)
	var page api.EntriesPage
	eventually(t, "the item's entry in the Inbox", func() bool {
		page, err = c.EntriesSince(ctx, client.EntryQuery{View: "inbox"}, api.Since{})
		return err == nil && len(page.Entries) == 1
	})
	if e := page.Entries[0]; e.Kind != "action" || !slices.Equal(e.Units, []unit.Unit{u}) || page.Counters.Badge != 1 {
		t.Fatalf("%+v", page)
	}
	res, err := c.EntryDo(ctx, page.Entries[0].ID, "next", api.EntryRequest{Source: "cli"})
	if err != nil || res.Entry.State != "next" {
		t.Fatalf("%+v %v", res, err)
	}
	if counters, err := c.Counters(ctx, ""); err != nil || counters.Views["next"] != 1 || counters.Badge != 0 {
		t.Fatalf("%+v %v", counters, err)
	}
	if cat, err := c.GTD(ctx); err != nil || len(cat.Views) == 0 {
		t.Fatalf("%+v %v", cat, err)
	}
}

func ptrTo[T any](v T) *T { return &v }

// socketDo sends body to path on the daemon's socket and returns the status and the body.
func socketDo(t *testing.T, sock, method, path, body string) (int, string) {
	t.Helper()
	hc := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	req, _ := http.NewRequest(method, "http://daemon"+path, strings.NewReader(body))
	req.Header.Set(api.Header, api.Version)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

func TestDisabledPluginEntriesRefuseOnARealServe(t *testing.T) {
	te := newServeEnv(t)
	no := false
	te.cfg.Plugins = map[string]config.Plugin{string(unit.GitHub): {Enabled: &no}}
	ctx, cancel := context.WithCancel(context.Background())
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	e := gtd.Entry{ID: "e-00000000000000aa", Kind: gtd.KindAction, State: gtd.Inbox, Title: "review it", Units: []unit.Unit{u},
		Items: []gtd.ItemRef{{Unit: u, Item: "gh:review"}}, Origin: gtd.OriginDerived, Created: at, Updated: at}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--no-jobs", "--mcp-port", strconv.Itoa(freePort(t))}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	waitHealth(t, te.Env, "the daemon", func(api.Health) bool { return true })
	code, body := socketDo(t, te.Socket, http.MethodGet, api.EntriesPath+"?view=inbox", "")
	if code != 200 || strings.Contains(body, string(e.ID)) || !strings.Contains(body, `"badge":0`) {
		t.Fatalf("the disabled plugin's entry is listed: %d %s", code, body)
	}
	if code, body := socketDo(t, te.Socket, http.MethodGet, api.EntryPath(string(e.ID)), ""); code != 200 || strings.Count(body, `"enabled":true`) != 1 {
		t.Fatalf("the read-only entry: %d %s", code, body)
	}
	for _, verb := range []string{"next", "approve", "delegate", "done"} {
		if code, body := socketDo(t, te.Socket, http.MethodPost, api.EntryPath(string(e.ID), verb), `{"source":"cli"}`); code != 409 || !strings.Contains(body, `"plugin":"github"`) {
			t.Errorf("%s: %d %s", verb, code, body)
		}
	}
	if code, body := socketDo(t, te.Socket, http.MethodPost, api.EntryPath(string(e.ID), "dismiss"), `{"source":"cli"}`); code != 200 || !strings.Contains(body, `"state":"trash"`) {
		t.Errorf("dismiss: %d %s", code, body)
	}
	if code, body := socketDo(t, te.Socket, http.MethodPost, api.EntriesPath, `{"title":"x","unit":{"provider":"github","key":"acme/api#7"}}`); code != 409 {
		t.Errorf("a capture on the disabled unit: %d %s", code, body)
	}
	if code, body := socketDo(t, te.Socket, http.MethodPost, api.EntriesPath, `{"title":"call the bank"}`); code != 200 {
		t.Errorf("a plain capture: %d %s", code, body)
	}
}
