package cli

import (
	"bytes"
	"cmp"
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
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	roverlay "github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func withIdentity(p compiledPlugin, who items.Person) compiledPlugin {
	build := p.Build
	p.Build = func(cfg config.Config, d moduleDeps) (pluginModule, error) {
		m, err := build(cfg, d)
		m.Identity = func(context.Context) (items.Person, error) { return who, nil }
		return m, err
	}
	return p
}

func TestMeFromEveryEnabledModule(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := inventory.New(db)
	ctx := context.Background()
	gh := plugins[slices.IndexFunc(plugins, func(p compiledPlugin) bool { return p.ID == unit.GitHub })]
	thread := withIdentity(threadModule(unit.Unit{Provider: "thread", Key: "C1 0001"}), items.Person{Key: "slack:U000AAA", Aliases: []string{"email:me@example.test"}})
	off := thread
	off.ID = "off"
	off = withIdentity(off, items.Person{Key: "off:someone"})
	cfg := Defaults()
	no := false
	cfg.Plugins.Other = map[string]config.Plugin{"off": {Enabled: &no}}
	mods, err := buildSources([]compiledPlugin{gh, thread, off}, cfg, moduleDeps{DB: db, Inventory: st, Clock: clockFunc(te.Now)}, bundledUI)
	if err != nil {
		t.Fatal(err)
	}
	d := te.serveTasks(daemon.Deps{}, db, cfg, mods, func(string, ...any) {})
	if _, err := d.TasksMe(ctx); err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("no viewer stored yet: %v", err)
	}
	if got := te.tasksMe.get().Keys; !slices.Equal(got, []string{"slack:U000AAA", "email:me@example.test"}) {
		t.Fatalf("before the viewer: %v", got)
	}
	if err := st.SetViewer(ctx, unit.GitHub, "octocat"); err != nil {
		t.Fatal(err)
	}
	grew, err := d.TasksMe(ctx)
	if err != nil || !grew {
		t.Fatalf("grew %v, %v", grew, err)
	}
	got := te.tasksMe.get().Keys
	if !slices.Contains(got, "github:octocat") || !slices.Contains(got, "slack:U000AAA") || slices.Contains(got, "off:someone") {
		t.Fatalf("me %v", got)
	}
}

// derivedRig stores each case's card and reconciles its derived items through the real ledger, then Syncs.
type derivedRig struct {
	db    *sqlite.DB
	svc   *coretasks.Service
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
	me := task.Me{Keys: []string{"github:" + ghsourcetest.Viewer}}
	svc := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: db.Tasks(), Items: ledger, Me: me, Clock: clock,
		Facts: coretasks.CardFacts{Cards: db.Cards(), Me: func() task.Me { return me }}})
	r := derivedRig{db: db, svc: svc, asked: map[unit.Unit]bool{}}
	var units []unit.Unit
	for i, c := range cases {
		u := c.Row.Unit
		units = append(units, u)
		cd, err := ghsource.CardOf(c.Row, c.Report)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Cards().Save(ctx, cd, ghsourcetest.Now); err != nil {
			t.Fatal(err)
		}
		at := unit.Anchor{Unit: u, Ref: "row:" + strconv.Itoa(i+1)}
		if _, err := ledger.Reconcile(ctx, u, ghsource.ItemsOf(c.Row, c.Report), at, items.Superseded); err != nil {
			t.Fatal(err)
		}
		if asked(c.Row) {
			r.asked[u] = true
			if _, err := svc.Open(ctx, coretasks.OpenRequest{Title: "the agent asks", Unit: &u}); err != nil {
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

func (r derivedRig) tasks(t *testing.T, u unit.Unit) []task.Task {
	t.Helper()
	es, err := r.db.Tasks().TasksOf(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestEveryNeedsMeRowHasAnInboxOrNextTask(t *testing.T) {
	t.Parallel()
	cases := ghsourcetest.Cases()
	r := newDerivedRig(t, cases)
	needs, questions := 0, 0
	for _, c := range cases {
		es := r.tasks(t, c.Row.Unit)
		in := func(states ...task.State) bool {
			return slices.ContainsFunc(es, func(e task.Task) bool { return slices.Contains(states, e.State) })
		}
		switch {
		case r.asked[c.Row.Unit]:
			questions++
			if len(es) != 1 || es[0].State != task.State("inbox") {
				t.Errorf("%s asked: %+v", c.Name, es)
			}
		case slices.Contains(c.Row.Views, api.ViewNeedsMe):
			needs++
			if !in(task.State("inbox"), task.State("next")) {
				t.Errorf("%s needs me: %+v", c.Name, es)
			}
		}
	}
	if needs == 0 || questions == 0 {
		t.Fatalf("compared %d needs-me, %d asked rows", needs, questions)
	}
	t.Logf("%d needs-me, %d asked rows of %d", needs, questions, len(cases))
}

// TestEveryFollowedPRHasOneTask derives every situation's row through the real ledger and Sync: each open
// pull request the user follows, or that needs them, has exactly one task, with the pull request as its first
// unit and every item it derived as its asks.
func TestEveryFollowedPRHasOneTask(t *testing.T) {
	t.Parallel()
	cases := ghsourcetest.Cases()
	r := newDerivedRig(t, cases)
	followed, quiet := 0, 0
	for _, c := range cases {
		es := r.tasks(t, c.Row.Unit)
		if len(es) > 1 {
			t.Errorf("%s has %d tasks: %+v", c.Name, len(es), es)
			continue
		}
		open := situation.Kind(c.Row.Kind) != situation.KindDone
		if !open || !prrow.InScope(c.Row) && !prrow.NeedsMe(c.Row) {
			continue
		}
		followed++
		if len(es) != 1 || !es[0].Open() || es[0].Units[0] != c.Row.Unit {
			t.Errorf("%s (%s): tasks %+v", c.Name, c.Row.Kind, es)
			continue
		}
		its := ghsource.ItemsOf(c.Row, c.Report)
		for _, it := range its {
			if !slices.Contains(es[0].Items, task.ItemRef{Unit: c.Row.Unit, Item: it.ID}) {
				t.Errorf("%s: the task holds %+v, not %s", c.Name, es[0].Items, it.ID)
			}
		}
		if !slices.ContainsFunc(its, func(it items.Item) bool { return it.ID != "gh:follow" }) {
			quiet++
		}
	}
	if followed == 0 || quiet == 0 {
		t.Fatalf("%d followed rows, %d of them asking nothing", followed, quiet)
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
	var page api.TasksPage
	eventually(t, "the item's entry in the Inbox", func() bool {
		page, err = c.TasksSince(ctx, client.TaskQuery{View: "inbox"}, api.Since{})
		return err == nil && len(page.Tasks) == 1
	})
	if e := page.Tasks[0]; !slices.Equal(e.Units, []unit.Unit{u}) || page.Counters.Badge != 1 {
		t.Fatalf("%+v", page)
	}
	res, err := c.TaskDo(ctx, page.Tasks[0].ID, "next", api.TaskRequest{Source: "cli"})
	if err != nil || res.Task.State != "next" {
		t.Fatalf("%+v %v", res, err)
	}
	if counters, err := c.Counters(ctx, ""); err != nil || counters.Views["needs-me"] != 1 || counters.Views["inbox"] != 0 {
		t.Fatalf("%+v %v", counters, err)
	}
	if cat, err := c.Views(ctx); err != nil || len(cat.Views) == 0 {
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

func TestDisabledPluginTasksRefuseOnARealServe(t *testing.T) {
	te := newServeEnv(t)
	no := false
	te.cfg.GitHub.Enabled = &no
	ctx, cancel := context.WithCancel(context.Background())
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	e := task.Task{ID: "e-00000000000000aa", State: "inbox", Category: task.CategoryInbox, Title: "review it", Units: []unit.Unit{u},
		Items: []task.ItemRef{{Unit: u, Item: "gh:review"}}, Origin: task.OriginDerived, Created: at, Updated: at}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
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
	code, body := socketDo(t, te.Socket, http.MethodGet, api.TasksPath+"?view=inbox", "")
	if code != 200 || strings.Contains(body, string(e.ID)) || !strings.Contains(body, `"badge":0`) {
		t.Fatalf("the disabled plugin's entry is listed: %d %s", code, body)
	}
	if code, body := socketDo(t, te.Socket, http.MethodGet, api.TaskPath(string(e.ID)), ""); code != 200 || strings.Count(body, `"enabled":true`) != 1 {
		t.Fatalf("the read-only entry: %d %s", code, body)
	}
	for _, verb := range []string{"next", "approve", "delegate", "done"} {
		if code, body := socketDo(t, te.Socket, http.MethodPost, api.TaskPath(string(e.ID), verb), `{"source":"cli"}`); code != 409 || !strings.Contains(body, `"plugin":"github"`) {
			t.Errorf("%s: %d %s", verb, code, body)
		}
	}
	if code, body := socketDo(t, te.Socket, http.MethodPost, api.TaskPath(string(e.ID), "dismiss"), `{"source":"cli"}`); code != 200 || !strings.Contains(body, `"state":"trash"`) {
		t.Errorf("dismiss: %d %s", code, body)
	}
	if code, body := socketDo(t, te.Socket, http.MethodPost, api.TasksPath, `{"title":"x","unit":{"provider":"github","key":"acme/api#7"}}`); code != 409 {
		t.Errorf("a capture on the disabled unit: %d %s", code, body)
	}
	if code, body := socketDo(t, te.Socket, http.MethodPost, api.TasksPath, `{"title":"call the bank"}`); code != 200 {
		t.Errorf("a plain capture: %d %s", code, body)
	}
}

// prLife derives one pull request's situations in turn through the real ledger and Sync, as the cards job
// and the tasks worker do.
type prLife struct {
	t      *testing.T
	db     *sqlite.DB
	svc    *coretasks.Service
	ledger *coreitems.Ledger
	rev    int
}

func newPRLife(t *testing.T) *prLife {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := clockFunc(func() time.Time { return ghsourcetest.Now })
	me := task.Me{Keys: []string{"github:" + ghsourcetest.Viewer}}
	l := &coreitems.Ledger{Store: db.Items(), Clock: clock}
	svc := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: db.Tasks(), Items: l, Me: me, Clock: clock,
		Facts: coretasks.CardFacts{Cards: db.Cards(), Me: func() task.Me { return me }}})
	return &prLife{t: t, db: db, svc: svc, ledger: l}
}

// derive stores s's card and items and syncs its unit; it returns the items and the unit's tasks.
func (l *prLife) derive(s ghsourcetest.Situation) ([]items.Item, []task.Task) {
	l.t.Helper()
	ctx := context.Background()
	row, rep := ghsourcetest.DeriveReport(s)
	cd, err := ghsource.CardOf(row, rep)
	if err != nil {
		l.t.Fatal(err)
	}
	if _, err := l.db.Cards().Save(ctx, cd, ghsourcetest.Now); err != nil {
		l.t.Fatal(err)
	}
	l.rev++
	its := ghsource.ItemsOf(row, rep)
	if _, err := l.ledger.Reconcile(ctx, row.Unit, its, unit.Anchor{Unit: row.Unit, Ref: "row:" + strconv.Itoa(l.rev)}, cmp.Or(cd.Outcome, items.Superseded)); err != nil {
		l.t.Fatal(err)
	}
	if err := l.svc.Sync(ctx, []unit.Unit{row.Unit}); err != nil {
		l.t.Fatal(err)
	}
	es, err := l.db.Tasks().TasksOf(ctx, row.Unit)
	if err != nil {
		l.t.Fatal(err)
	}
	return its, es
}

func TestASituationChangeKeepsTheSameTask(t *testing.T) {
	t.Parallel()
	l := newPRLife(t)
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(900, ghsourcetest.Other)}
	s.PR.ReviewRequests = []string{ghsourcetest.Viewer}
	s.Events = []dinv.Event{ghsourcetest.Event(900, dinv.KindReviewRequest, ghsourcetest.Other, ghsourcetest.Now.Add(-3*time.Hour), ghsourcetest.Viewer)}
	_, es := l.derive(s)
	if len(es) != 1 || !es[0].Open() {
		t.Fatalf("the review request's tasks %+v", es)
	}
	id := es[0].ID
	steps := []struct {
		name string
		edit func(*ghsourcetest.Situation)
		want string
	}{
		{"the loop reviews it", func(s *ghsourcetest.Situation) {
			s.PR.Workspace = ghsourcetest.ReviewWorkspace
			s.Turn = &prrow.TurnFact{Facts: roverlay.Turn{Intent: "review", Role: "pr.review", State: "started", At: ghsourcetest.Now.Add(-time.Minute)}, Session: "s1"}
		}, "gh:follow"},
		{"the viewer asked for changes", func(s *ghsourcetest.Situation) {
			s.Turn, s.PR.ReviewRequests = nil, nil
			s.Events = append(s.Events, ghsourcetest.Event(900, dinv.KindReview, ghsourcetest.Viewer, ghsourcetest.Now.Add(-time.Hour), "CHANGES_REQUESTED:h1"))
		}, "gh:fix"},
		{"it merged", func(s *ghsourcetest.Situation) {
			s.PR.State, s.PR.MergedAt = "MERGED", ghsourcetest.Now
			s.Events = append(s.Events, ghsourcetest.Event(900, dinv.KindMerged, ghsourcetest.Other, ghsourcetest.Now, ""))
		}, ""},
	}
	for _, st := range steps {
		st.edit(&s)
		its, es := l.derive(s)
		if len(es) != 1 || es[0].ID != id || !es[0].Open() {
			t.Fatalf("%s: tasks %+v, want the one open task %s", st.name, es, id)
		}
		if st.want == "" {
			if len(its) != 0 || len(es[0].Items) != 0 {
				t.Fatalf("%s: items %+v, task holds %+v", st.name, its, es[0].Items)
			}
			continue
		}
		if len(its) != 1 || its[0].ID != st.want || !slices.Equal(es[0].Items, []task.ItemRef{{Unit: s.PR.Ref.Unit(), Item: st.want}}) {
			t.Fatalf("%s: items %+v, task holds %+v, want %s", st.name, its, es[0].Items, st.want)
		}
	}
}

func TestAPendingRoundIsAPlanGateNotAnItem(t *testing.T) {
	t.Parallel()
	for _, c := range ghsourcetest.Cases() {
		k := situation.Kind(c.Row.Kind)
		if k != situation.KindRoundPending && k != situation.KindHeldByCap && k != situation.KindBlocked && c.Row.Pending == nil {
			continue
		}
		for _, it := range ghsource.ItemsOf(c.Row, c.Report) {
			if it.ID == "gh:round" || it.ID == "gh:blocked" || strings.HasPrefix(it.ID, "gh:held") || it.ID == "gh:wait" {
				t.Errorf("%s: %s is an item", c.Name, it.ID)
			}
		}
	}
	l := newPRLife(t)
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(901, ghsourcetest.Other)}
	s.PR.ReviewRequests, s.PR.Workspace = []string{ghsourcetest.Viewer}, ghsourcetest.ReviewWorkspace
	_, before := l.derive(s)
	s.Pending = &round.Pending{Intent: "review", Reason: "new push", Since: ghsourcetest.Now.Add(-time.Minute), Deadline: ghsourcetest.Now.Add(4 * time.Minute)}
	its, after := l.derive(s)
	if len(before) != 1 || len(after) != 1 || after[0].ID != before[0].ID || !slices.Equal(after[0].Items, before[0].Items) {
		t.Fatalf("before the round %+v, pending %+v", before, after)
	}
	if len(its) != 1 || its[0].ID != "gh:review" {
		t.Fatalf("the pending round's items %+v", its)
	}
}

func TestUntrackingAPRDropsItsTask(t *testing.T) {
	t.Parallel()
	l := newPRLife(t)
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(902, ghsourcetest.Other)}
	s.PR.Reasons, s.Override.Tracked = nil, true
	_, es := l.derive(s)
	if len(es) != 1 || !es[0].Open() {
		t.Fatalf("the tracked pull request's tasks %+v", es)
	}
	id := es[0].ID
	s.Override.Tracked = false
	_, es = l.derive(s)
	if len(es) != 1 || es[0].ID != id || es[0].Category != task.CategoryDropped {
		t.Fatalf("after untracking: %+v, want %s dropped", es, id)
	}
}

func TestRefollowingAPRReopensItsTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newPRLife(t)
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(903, ghsourcetest.Other)}
	s.PR.Reasons, s.Override.Tracked = nil, true
	_, es := l.derive(s)
	id := es[0].ID
	s.Override.Tracked = false
	l.derive(s)
	s.Override.Tracked = true
	_, es = l.derive(s)
	if len(es) != 1 || es[0].ID != id || !es[0].Open() || !es[0].Changed {
		t.Fatalf("followed again: %+v, want %s open and changed", es, id)
	}
	e := es[0]
	e.State, e.Category = "trash", task.CategoryDropped
	if err := l.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "dismissed"}}); err != nil {
		t.Fatal(err)
	}
	s.Override.Tracked = false
	l.derive(s)
	s.Override.Tracked = true
	if _, es = l.derive(s); len(es) != 1 || es[0].Category != task.CategoryDropped {
		t.Fatalf("a task the person dropped while following: %+v, want it dropped", es)
	}
}
