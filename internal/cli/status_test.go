package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// statusDaemon serves the views catalog, whose badge counts inbox and needs-me, the tasks of each view, and rows.
func statusDaemon(t *testing.T, tasks map[string][]api.TaskView, rows []api.Row) string {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(pattern string, v func(*http.Request) any) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(v(r)) })
	}
	reply("GET /api/health", func(*http.Request) any { return api.Health{} })
	reply("GET "+api.ViewsPath, func(*http.Request) any {
		return api.ViewsCatalog{Views: []api.TaskViewSpec{{ID: "inbox", Label: "Inbox", Badge: true}, {ID: "needs-me", Label: "Needs me", Badge: true},
			{ID: "waiting", Label: "Waiting"}}}
	})
	reply("GET "+api.TasksPath, func(r *http.Request) any {
		return api.TasksPage{Full: true, Tasks: append([]api.TaskView{}, tasks[r.URL.Query().Get("view")]...)}
	})
	reply("GET "+api.Rows, func(*http.Request) any { return api.RowsPage{Full: true, Rows: rows} })
	return serveSocket(t, mux)
}

func TestStatusListsTheBadgeViewsTasks(t *testing.T) {
	t.Parallel()
	tasks := map[string][]api.TaskView{
		"needs-me": {{ID: "t-1", Title: "Review Batch export", UnitLabels: []string{"acme/api#7"}, OwedBy: "me", Updated: testNow.Add(-3 * time.Hour)}},
		"waiting":  {{ID: "t-2", Title: "Fix login", UnitLabels: []string{"acme/web#12"}, OwedBy: "github:ana", Updated: testNow.Add(-50 * time.Hour)}},
		"inbox":    {{ID: "t-3", Title: "Deploy window?", OwedBy: "me", Updated: testNow.Add(-50 * time.Hour)}},
	}
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = statusDaemon(t, tasks, []api.Row{})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	want := "inbox\tme\t2d\t\tDeploy window?\t\n" +
		"needs-me\tme\t3h\tacme/api#7\tReview Batch export\t\n"
	if got := te.out.String(); got != want {
		t.Fatalf("status:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatusPipedStaysTabSeparatedUnderCLICOLOR_FORCE(t *testing.T) {
	t.Parallel()
	tasks := map[string][]api.TaskView{"needs-me": {{ID: "t-1", Title: "Review Batch export", UnitLabels: []string{"acme/api#7"}, OwedBy: "me", Updated: testNow.Add(-3 * time.Hour)}}}
	te := newTestEnv(t, &fake.Manager{})
	te.Getenv = func(k string) string { return map[string]string{"CLICOLOR_FORCE": "1"}[k] }
	te.Socket = statusDaemon(t, tasks, []api.Row{})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.out.String(); got != "needs-me\tme\t3h\tacme/api#7\tReview Batch export\t\n" {
		t.Fatalf("status:\n%q", got)
	}
}

func TestStatusSaysWhenTheViewsAreEmpty(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = statusDaemon(t, nil, []api.Row{})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.err.String(); !strings.Contains(got, "nothing in Inbox, Needs me") || !strings.Contains(got, brand.Command+" open") || te.out.Len() != 0 {
		t.Fatalf("status:\n%s\nstderr:\n%s", te.out, got)
	}
	te.out.Reset()
	te.Getenv = func(k string) string { return map[string]string{"GH_FORCE_TTY": "80"}[k] }
	if code := te.run("status"); code != 0 || !strings.Contains(te.out.String(), "nothing in Inbox, Needs me") {
		t.Fatalf("exit %d, on a terminal:\n%s", code, te.out)
	}
}

func TestStatusListsTheTrackedPullRequests(t *testing.T) {
	t.Parallel()
	web := pr.Ref{Owner: "acme", Repo: "web", Number: 12}
	rows := []api.Row{
		{Unit: r7.Unit(), Label: r7.String(), Title: "Batch export", Views: []string{}, Payload: rowPayload(prrow.Payload{Repo: "acme/api", Override: api.Override{Tracked: true}}), Summary: api.Summary{WaitsOn: "bob", Since: testNow.Add(-3 * time.Hour)}},
		{Unit: web.Unit(), Label: web.String(), Title: "Fix login", Views: []string{}, Payload: rowPayload(prrow.Payload{Repo: "acme/web"}), Summary: api.Summary{WaitsOn: "ana", Since: testNow.Add(-50 * time.Hour)}},
	}
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = statusDaemon(t, nil, rows)
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.out.String(); !strings.Contains(got, "tracked\tbob\t3h\tacme/api#7\tBatch export\t\n") || strings.Contains(got, "acme/web#12") {
		t.Fatalf("status:\n%s", got)
	}
}

func statusFixture() []statusSection {
	api7 := statusLine{label: r7.String(), link: "https://github.com/acme/api/pull/7", title: "Batch export\tin chunks", who: "me", since: testNow.Add(-3 * time.Hour), reason: "review requested, no review on the head"}
	web := statusLine{label: "acme/web#1365", link: "https://github.com/acme/web/pull/1365", title: "✨ feat: 日本語のタイトルがとても長い and a title that goes on past the width", who: "team:acme/reviewers, carla, bob", since: testNow.Add(-9 * 24 * time.Hour), reason: "checks failing: build-py3 (ci-pipeline);\r\nreviewed by frankhopper1,\n erinsantos-qa, frankoliveira"}
	old := statusLine{label: "initech-co/tracker#7734", link: "https://github.com/initech-co/tracker/pull/7734", title: "fix(agentlog): bound container logs", who: "author", since: testNow.Add(-51 * 24 * time.Hour), stalled: true, reason: "no reviewer requested"}
	return []statusSection{
		{"needs-me", "Needs me", []statusLine{web, api7}, true},
		{"stalled", "Stalled", []statusLine{old}, true},
		{sectionTracked, sectionTracked, []statusLine{api7}, false},
	}
}

func TestStatusReadsAsAlignedSectionsOnATerminal(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	writeStatus(te.Env, termfmt.Term{TTY: true, Color: true, Width: 90}, statusFixture(), testNow)
	got := te.out.String()
	for _, l := range strings.Split(got, "\n") {
		if c := termfmt.Cells(termfmt.Plain(l)); c > 90 {
			t.Errorf("%d cells: %q", c, termfmt.Plain(l))
		}
	}
	golden(t, "status_tty.golden", bracketed(got))
}

func TestStatusPipedIsTabSeparatedAndWhole(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	writeStatusTSV(te.Env, statusFixture(), testNow)
	want := "needs-me\tteam:acme/reviewers, carla, bob\t9d\tacme/web#1365\t✨ feat: 日本語のタイトルがとても長い and a title that goes on past the width\tchecks failing: build-py3 (ci-pipeline); reviewed by frankhopper1, erinsantos-qa, frankoliveira\n" +
		"needs-me\tme\t3h\tacme/api#7\tBatch export in chunks\treview requested, no review on the head\n" +
		"stalled\tauthor\t51d\tinitech-co/tracker#7734\tfix(agentlog): bound container logs\tno reviewer requested\n" +
		"tracked\tme\t3h\tacme/api#7\tBatch export in chunks\treview requested, no review on the head\n"
	if got := te.out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// rowPayload encodes a GitHub row's payload.
func rowPayload(p prrow.Payload) json.RawMessage {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return raw
}
