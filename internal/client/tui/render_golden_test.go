package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/termfmt"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// goldenModel is a Needs me view with a question selected, a pending round,
// a stalled pull request of another repository, one with a brief and a
// failing check, and a busy one.
func goldenModel(t *testing.T, color bool) *Model {
	asked := fixtures(t)["asked_choice"]
	setPR(&asked, func(p *prView) {
		p.Brief = &api.BriefSummary{Size: "m", Risk: "high", Goal: "Export the ledger in batches of a thousand rows so the nightly job stops timing out"}
	})
	setPR(&asked, func(p *prView) { p.Workspace = api.Workspace{DisplayName: "REVIEW batch-export", Status: "reviewing"} })

	pending := row(3, "Retry webhooks with backoff", "round_pending", "needs-me")
	pending.Pending = &api.PendingRound{Deadline: now.Add(42 * time.Second)}
	pending.Summary.Reason = "new commits since the last review"

	stalled := row(1365, "Remove hardcoded credentials from the common constants fallbacks", "changes", "needs-me", "mine")
	stalled.Unit = pr.Ref{Owner: "acme", Repo: "billing-service", Number: 1365}.Unit()
	stalled.Label = "acme/billing-service#1365"
	stalled.Link = "https://github.com/acme/billing-service/pull/1365"
	stalled.Summary.Stalled, stalled.Summary.Since = true, now.Add(-9*24*time.Hour)
	stalled.Summary.WaitsOn = "author"
	setPR(&stalled, func(p *prView) { p.Author = "carla" })
	stalled.Summary.Reason = "changes asked, no new commit since"

	failing := row(42, "✨ feat: export ⚡️ faster 日本語のタイトル", "checks", "needs-me")
	failing.Summary.Since = now.Add(-50 * time.Hour)
	setPR(&failing, func(p *prView) { p.Brief = &api.BriefSummary{Size: "xl", Risk: "medium"} })
	setPR(&failing, func(p *prView) {
		p.Facts = &api.RowFacts{Checks: api.CheckFacts{State: "failure", Failing: []string{"build", "lint"}}}
	})

	busy := row(9, "Loop one", "loop_working", "needs-me", "loop")
	busy.Summary.Since = now.Add(-12 * time.Minute)

	m := modelWith(t, asked, pending, stalled, failing, busy)
	m.SetColor(color)
	m.busy[busy.Unit] = "review"
	m.health.Paused = true
	m.health.Warnings = []string{"app: notifications are off"}
	return m
}

// gtdGolden is the golden model on GTD view, the Inbox with its first
// entry's detail open, the Outbox with its draft's.
func gtdGolden(t *testing.T, color bool, view string) *Model {
	m := goldenModel(t, color)
	m.SetGTD(gtdCatalog())
	for v, es := range gtdEntries() {
		m.SetEntries(v, es, gtdCounters())
	}
	m.navigate(view)
	m.busy[entryUnit("e5")] = "done"
	if view == "inbox" || view == "outbox" {
		e, _ := m.selectedEntry()
		m.openEntry(e, 0)
		m.SetDetail(e.ID, e, nil)
	}
	return m
}

// draftsGolden is Focus with its entry's detail open, listing the drafts
// made for it, the first one picked.
func draftsGolden(t *testing.T, color bool) *Model {
	m := gtdGolden(t, color, "focus")
	e, _ := m.selectedEntry()
	m.openEntry(e, 0)
	m.SetDetail(e.ID, e, nil)
	m.SetDrafts(e.ID, []api.Draft{
		{ID: 4, Entry: e.ID, Kind: "reply", Text: "Thanks, the export now runs in batches.\nI will watch tonight's job.", State: "proposed", Origin: api.DraftOrigin{Kind: "turn"}},
		{ID: 3, Entry: e.ID, Kind: "react", Reaction: "eyes", State: "sent", Origin: api.DraftOrigin{Kind: "user"}},
	})
	m.picked = 4
	return m
}

// planGolden is the Plan view over planItems, the pull request with its
// title, and a Slack thread named by its row's label and title, the first
// item selected.
func planGolden(t *testing.T, color bool) *Model {
	m := goldenModel(t, color)
	m.SetPlanSpecs(servedSpecs(t).Plan)
	thread := planOf("slack-follow-up/slack/T1%2FC1%2F171.1/ee", "slack-follow-up", unit.Unit{Provider: "slack", Key: "T1/C1/171.1"}, "pending", "asked you a question")
	thread.Label, thread.Title, thread.Default = "#general", "Launch plan for the export", "wait"
	items := planItems()
	items[3].Title, items[3].Link = "Batch export", "https://github.com/acme/api/pull/7"
	m.SetPlan(append(items, thread), nil)
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	return m
}

func TestRenderGolden(t *testing.T) {
	models := map[string]func(*testing.T, bool) *Model{"render": goldenModel}
	for _, v := range []string{"focus", "inbox", "outbox", "next", "waiting"} {
		models["gtd-"+v] = func(t *testing.T, color bool) *Model { return gtdGolden(t, color, v) }
	}
	models["gtd-review"] = func(t *testing.T, color bool) *Model { return reviewModel(t, color) }
	models["gtd-drafts"] = draftsGolden
	models["plan"] = planGolden
	for prefix, model := range models {
		for _, color := range []bool{false, true} {
			for _, size := range [][2]int{{120, 40}, {80, 30}} {
				renderGolden(t, prefix, model, color, size)
			}
		}
	}
}

func renderGolden(t *testing.T, prefix string, model func(*testing.T, bool) *Model, color bool, size [2]int) {
	name := fmt.Sprintf("%s-%dx%d", prefix, size[0], size[1])
	if color {
		name += "-color"
	}
	t.Run(name, func(t *testing.T) {
		lines := model(t, color).Render(size[0], size[1], now)
		for i, l := range lines {
			if c := termfmt.Cells(termfmt.Plain(l)); c > size[0] {
				t.Errorf("line %d has %d cells: %q", i, c, termfmt.Plain(l))
			}
			if !color && termfmt.Plain(l) != l {
				t.Errorf("line %d is styled without color: %q", i, l)
			}
		}
		got := strings.Join(lines, "\n") + "\n"
		path := filepath.Join("testdata", name+".txt")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs; go test -run TestRenderGolden -update rewrites it.\ngot:\n%s", path, got)
		}
	})
}

func TestTheDetailSaysAPullRequestIsStalled(t *testing.T) {
	m := goldenModel(t, false)
	press(m, Key{Name: KeyDown}, Key{Name: KeyDown})
	screen := strings.Join(m.Render(120, 40, now), "\n")
	if !strings.Contains(screen, "carla · changes asked · 9d, stalled") {
		t.Fatalf("no stalled fact:\n%s", screen)
	}
}

func TestTheSelectedRowIsReversedInColor(t *testing.T) {
	lines := goldenModel(t, true).Render(80, 30, now)
	if !strings.HasPrefix(lines[2], "\x1b[7m> ") || termfmt.Cells(termfmt.Plain(lines[2])) != 80 {
		t.Fatalf("%q", lines[2])
	}
	if strings.Contains(lines[3], "\x1b[7m") {
		t.Fatalf("an unselected row is reversed: %q", lines[3])
	}
}

func TestADisabledKeySaysWhyOnTheStatusLine(t *testing.T) {
	m := goldenModel(t, false)
	screen := strings.Join(m.Render(120, 40, now), "\n")
	if !strings.Contains(screen, "not now    F address · N nudge") || strings.Contains(screen, "take it over first") {
		t.Fatalf("disabled actions not on one line without reasons:\n%s", screen)
	}
	if _, ok := press(m, Key{Rune: 'F'}); ok {
		t.Fatal("a disabled action was sent")
	}
	if last := m.Render(120, 40, now)[38]; last != "Not yours: take it over first" {
		t.Fatalf("status line %q", last)
	}
}

func TestACutDetailEndsInAnEllipsis(t *testing.T) {
	lines := goldenModel(t, false).Render(120, 20, now)
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "acme/api#7  Batch export") })
	if i < 0 {
		t.Fatalf("no detail:\n%s", strings.Join(lines, "\n"))
	}
	if lines[17] != "  …" {
		t.Fatalf("cut detail does not end in an ellipsis:\n%s", strings.Join(lines, "\n"))
	}
}

func TestAStalledAgeIsMarkedWithoutColor(t *testing.T) {
	lines := goldenModel(t, false).Render(80, 30, now)
	if !strings.HasPrefix(lines[4], "   !9d  acme/billing-service#1365") {
		t.Fatalf("%q", lines[4])
	}
}

func TestTheWaitCellCutsTheNamesBeforeTheKind(t *testing.T) {
	m := newModel(t)
	r := row(1, "x", "checks")
	r.Summary.WaitsOn = "gracehopper-acme, heidimueller"
	if got := m.waitCell(r, 28); !strings.HasSuffix(got, " · checks failing") || termfmt.Cells(got) > 28 {
		t.Fatalf("%q", got)
	}
}
