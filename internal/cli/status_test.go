package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/pr"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestStatusUsesDaemonWhenUp(t *testing.T) {
	web := pr.Ref{Owner: "acme", Repo: "web", Number: 12}
	rows := []api.Row{
		{Unit: r7.Unit(), Label: r7.String(), Title: "Batch export", Views: []string{api.ViewNeedsMe}, Payload: rowPayload(prrow.Payload{Repo: "acme/api"}), Summary: api.Summary{WaitsOn: "me", Since: testNow.Add(-3 * time.Hour), Reason: "review requested"}},
		{Unit: web.Unit(), Label: web.String(), Title: "Fix login", Views: []string{api.ViewStalled, api.ViewMine}, Payload: rowPayload(prrow.Payload{Repo: "acme/web"}), Summary: api.Summary{WaitsOn: "ana", Since: testNow.Add(-50 * time.Hour), Reason: "changes requested"}},
	}
	m := &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "a", RepoID: "r1", DisplayName: "inline only", HostID: "local", LinkedPR: 1}}}
	te := newTestEnv(t, m)
	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET /api/rows": api.RowsPage{Full: true, Rows: rows}})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	want := "needs-me\tme\t3h\tacme/api#7\tBatch export\treview requested\n" +
		"stalled\tana\t2d\tacme/web#12\tFix login\tchanges requested\n" +
		"mine\tana\t2d\tacme/web#12\tFix login\tchanges requested\n"
	if got := te.out.String(); got != want {
		t.Fatalf("status:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatusPipedStaysTabSeparatedUnderCLICOLOR_FORCE(t *testing.T) {
	rows := []api.Row{{Unit: r7.Unit(), Label: r7.String(), Title: "Batch export", Views: []string{api.ViewNeedsMe}, Payload: rowPayload(prrow.Payload{Repo: "acme/api"}), Summary: api.Summary{WaitsOn: "me", Since: testNow.Add(-3 * time.Hour), Reason: "review requested"}}}
	te := newTestEnv(t, &fake.Manager{})
	te.Getenv = func(k string) string { return map[string]string{"CLICOLOR_FORCE": "1"}[k] }
	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET /api/rows": api.RowsPage{Full: true, Rows: rows}})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.out.String(); got != "needs-me\tme\t3h\tacme/api#7\tBatch export\treview requested\n" {
		t.Fatalf("status:\n%q", got)
	}
}

func TestStatusSaysWhenTheViewsAreEmpty(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET /api/rows": api.RowsPage{Full: true, Rows: []api.Row{}}})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.err.String(); !strings.Contains(got, "nothing in needs-me, stalled, mine or loop") || !strings.Contains(got, brand.Command+" open") || te.out.Len() != 0 {
		t.Fatalf("status:\n%s\nstderr:\n%s", te.out, got)
	}
	te.out.Reset()
	te.Getenv = func(k string) string { return map[string]string{"GH_FORCE_TTY": "80"}[k] }
	if code := te.run("status"); code != 0 || !strings.Contains(te.out.String(), "nothing in needs-me, stalled, mine or loop") {
		t.Fatalf("exit %d, on a terminal:\n%s", code, te.out)
	}
}

func TestStatusListsTheTrackedPullRequests(t *testing.T) {
	web := pr.Ref{Owner: "acme", Repo: "web", Number: 12}
	rows := []api.Row{
		{Unit: r7.Unit(), Label: r7.String(), Title: "Batch export", Views: []string{}, Payload: rowPayload(prrow.Payload{Repo: "acme/api", Override: api.Override{Tracked: true}}), Summary: api.Summary{WaitsOn: "bob", Since: testNow.Add(-3 * time.Hour)}},
		{Unit: web.Unit(), Label: web.String(), Title: "Fix login", Views: []string{}, Payload: rowPayload(prrow.Payload{Repo: "acme/web"}), Summary: api.Summary{WaitsOn: "ana", Since: testNow.Add(-50 * time.Hour)}},
	}
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET /api/rows": api.RowsPage{Full: true, Rows: rows}})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.out.String(); !strings.Contains(got, "tracked\tbob\t3h\tacme/api#7\tBatch export\t\n") || strings.Contains(got, "acme/web#12") {
		t.Fatalf("status:\n%s", got)
	}
}

func statusFixture() []statusSection {
	api7 := api.Row{Unit: r7.Unit(), Label: r7.String(), Link: "https://github.com/acme/api/pull/7", Title: "Batch export\tin chunks", Summary: api.Summary{WaitsOn: "me", Since: testNow.Add(-3 * time.Hour), Reason: "review requested, no review on the head"}, Payload: rowPayload(prrow.Payload{Repo: "acme/api"})}
	web := api.Row{Unit: pr.Ref{Owner: "acme", Repo: "web", Number: 1365}.Unit(), Label: "acme/web#1365", Link: "https://github.com/acme/web/pull/1365", Title: "✨ feat: 日本語のタイトルがとても長い and a title that goes on past the width", Summary: api.Summary{WaitsOn: "team:acme/reviewers, carla, bob", Since: testNow.Add(-9 * 24 * time.Hour), Reason: "checks failing: build-py3 (ci-pipeline);\r\nreviewed by frankhopper1,\n erinsantos-qa, frankoliveira"}, Payload: rowPayload(prrow.Payload{Repo: "acme/web"})}
	old := api.Row{Unit: pr.Ref{Owner: "initech-co", Repo: "tracker", Number: 7734}.Unit(), Label: "initech-co/tracker#7734", Link: "https://github.com/initech-co/tracker/pull/7734", Title: "fix(agentlog): bound container logs", Summary: api.Summary{WaitsOn: "author", Since: testNow.Add(-51 * 24 * time.Hour), Stalled: true, Reason: "no reviewer requested"}, Payload: rowPayload(prrow.Payload{Repo: "initech-co/tracker"})}
	return []statusSection{
		{api.ViewNeedsMe, []api.Row{web, api7}, true},
		{api.ViewStalled, []api.Row{old}, true},
		{sectionTracked, []api.Row{api7}, false},
	}
}

func TestStatusReadsAsAlignedSectionsOnATerminal(t *testing.T) {
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
