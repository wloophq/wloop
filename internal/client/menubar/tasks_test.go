package menubar

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/texts"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func plainTask(n int) api.TaskView {
	return api.TaskView{ID: fmt.Sprintf("e-%d", n), State: "inbox", Title: "Review the retry", Units: []unit.Unit{uref(n)}}
}

func index(items []string, prefix string) int {
	return slices.IndexFunc(items, func(s string) bool { return strings.HasPrefix(s, prefix) })
}

func TestTitleFollowsNeedsYou(t *testing.T) {
	r := newRig(t)
	r.d.queue = needsYou(plainTask(1), plainTask(2))
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 0, "outbox": 0}}
	r.c.Refresh(context.Background())
	if got := r.title(); got != brand.Name+" 2" {
		t.Fatalf("title %q, want the Needs you total", got)
	}
	r.d.mu.Lock()
	r.d.queue = needsYou(plainTask(1), plainTask(2), plainTask(3))
	r.d.mu.Unlock()
	r.c.apply(context.Background(), api.Change{Type: "changed", Epoch: "e1", Rev: 1, Units: []unit.Unit{uref(1)}, Counters: &api.Counters{Views: map[string]int{"inbox": 3}}})
	if got := r.title(); got != brand.Name+" 3" {
		t.Fatalf("title %q after a change", got)
	}
}

func TestTitleReadsEachViewsCount(t *testing.T) {
	r := newRig(t)
	r.d.strings = &api.Strings{Locale: "en", Strings: map[string]api.Text{"{name} {n}": {"other": "{needs-me}/{inbox} of {n}"}}}
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 3, "needs-me": 2}}
	r.d.queue = needsYou(plainTask(1), plainTask(2), plainTask(3), plainTask(4))
	r.c.Refresh(context.Background())
	if got := r.title(); got != "2/3 of 4" {
		t.Fatalf("title %q, want each view's count", got)
	}
}

func TestTitleReadsTheCountsWithNoBadgeViewInTheRail(t *testing.T) {
	r := newRig(t)
	r.d.views = []api.TaskViewSpec{{ID: "inbox", Label: "Inbox", Rail: true}, {ID: "home-blocked", Label: "Blocked by you", Badge: true}}
	r.d.strings = &api.Strings{Locale: "en", Strings: map[string]api.Text{"{name} {n}": {"other": "{inbox} of {n}"}}}
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 3, "home-blocked": 1}}
	r.d.queue = needsYou(plainTask(1))
	r.c.Refresh(context.Background())
	if got := r.title(); got != "3 of 1" {
		t.Fatalf("title %q, want the counts though no badge view is in the rail", got)
	}
}

func TestTitleSaysPausedWhileAutomaticRoundsAre(t *testing.T) {
	r := newRig(t)
	r.d.health.Paused = true
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 3}}
	r.d.queue = needsYou(plainTask(1), plainTask(2), plainTask(3))
	r.c.Refresh(context.Background())
	if got := r.title(); got != brand.Name+" 3 paused" {
		t.Fatalf("title %q, want the paused title", got)
	}
	r.d.strings = &api.Strings{Locale: "en", Strings: map[string]api.Text{"{name} {n} paused": {"other": "⏸ {inbox}"}}}
	r.d.health.Strings = "custom-1"
	r.c.Refresh(context.Background())
	if got := r.title(); got != "⏸ 3" {
		t.Fatalf("title %q, want the profile's paused title with the view's count", got)
	}
}

func TestMenuListsFiveAndMore(t *testing.T) {
	r := newRig(t)
	var waiting []api.TaskView
	for i := 1; i <= 7; i++ {
		waiting = append(waiting, plainTask(i))
	}
	r.d.queue = needsYou(waiting...)
	r.d.rows["loop"] = []api.Row{reviewRow(9)}
	r.d.health = api.Health{Jobs: []api.JobStatus{{Name: "ingest:github", LastOK: now.Add(-2 * time.Minute)}}}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	if got[0] != "Needs you (7)" || !slices.Contains(got, "more…") || got[len(got)-1] != "Quit "+brand.Name {
		t.Fatalf("menu %q", got)
	}
	n := 0
	for _, it := range r.tray.menu {
		if strings.HasPrefix(it.Title, "acme/api#") {
			n++
		}
	}
	if n != 6 {
		t.Fatalf("%d listed, want 5 of Needs you + 1 in loop: %q", n, got)
	}
	for _, want := range []string{"In loop (1)", "Open cockpit", "Sweep now", "Pause automatic rounds", "Synced " + texts.English().Time(now.Add(-2*time.Minute).Local(), "time")} {
		if _, ok := find(r.tray.menu, want); !ok {
			t.Errorf("menu lacks %q: %q", want, got)
		}
	}
	more, _ := find(r.tray.menu, "more…")
	more.Action()
	waitFor(t, func() bool { o := r.openedURLs(); return len(o) == 1 && o[0] == "cockpit#view=needs-you" })
}

func TestPluginSectionsFollowTheCatalog(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{plainTask(1)}
	r.d.rows["needs-me"] = []api.Row{reviewRow(3)}
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	loop, needs := index(got, "In loop (1)"), index(got, "Needs you (0)")
	if loop < 0 || needs < 0 || loop < needs || index(got, "Needs me") >= 0 {
		t.Fatalf("menu %q, want In loop below the core sections and no Needs me", got)
	}
	r.d.mu.Lock()
	r.d.plugins = []api.Plugin{{ID: unit.GitHub}, {ID: "thread", Enabled: true, UI: &api.PluginUI{Menubar: []api.MenubarSection{{Label: "Threads", View: "loop"}, {Label: "Odd", View: "x&task=e-1"}}}}}
	r.d.mu.Unlock()
	r.c.apply(context.Background(), api.Change{Health: true})
	got = titles(r.tray.menu)
	if index(got, "In loop") >= 0 || index(got, "Threads (1)") < index(got, "Needs you (0)") {
		t.Fatalf("menu with github disabled %q", got)
	}
	r.d.mu.Lock()
	r.d.rows["x&task=e-1"] = []api.Row{reviewRow(1), reviewRow(2), reviewRow(3), reviewRow(4), reviewRow(5), reviewRow(6)}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	odd := index(titles(r.tray.menu), "Odd (6)")
	if odd < 0 || r.tray.menu[odd+6].Title != "more…" {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	r.tray.menu[odd+6].Action()
	waitFor(t, func() bool { return slices.Equal(r.openedURLs(), []string{"cockpit#view=x%26task%3De-1"}) })
}

func TestAStoreTheDaemonCannotOpenShowsWhyAndTheCommand(t *testing.T) {
	r := newRig(t)
	var copied []string
	r.c.cfg.Copy = func(s string) error { copied = append(copied, s); return nil }
	r.d.rowsErr = errors.New("the store is not open")
	r.d.health = api.Health{Store: &api.StoreStatus{State: api.StoreFailed, Message: "the store migration from version 1 failed", Fix: "fix-it migrate"}}
	r.c.Refresh(context.Background())
	if r.title() != brand.Name+" !" {
		t.Fatalf("title %q", r.title())
	}
	got := titles(r.tray.menu)
	want := []string{"Store needs attention: its migration failed", "the store migration from version 1 failed", "Copy command: fix-it migrate", "---", "Quit " + brand.Name}
	if !slices.Equal(got, want) {
		t.Fatalf("menu %q", got)
	}
	r.tray.menu[2].Action()
	if !slices.Equal(copied, []string{"fix-it migrate"}) {
		t.Fatalf("copied %q", copied)
	}
}

func TestDaemonDownShowsAQuestionMark(t *testing.T) {
	r := newRig(t)
	r.d.err = errors.New("dial unix: connection refused")
	r.c.Refresh(context.Background())
	if r.title() != brand.Name+" ?" {
		t.Fatalf("title %q", r.title())
	}
	got := titles(r.tray.menu)
	if !strings.Contains(got[0], "connection refused") || got[len(got)-1] != "Quit "+brand.Name {
		t.Fatalf("menu %q", got)
	}
	r.d.mu.Lock()
	r.d.err = nil
	r.d.queue = needsYou(plainTask(2))
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	if r.title() != brand.Name+" 1" {
		t.Fatalf("did not recover: %q", r.title())
	}
}

func TestTaskClickOpensItInTheCockpit(t *testing.T) {
	r := newRig(t)
	r.d.queue = needsYou(plainTask(2), api.TaskView{ID: "draft:5", State: "inbox", Title: "reply: on it"})
	r.c.Refresh(context.Background())
	it, ok := find(r.tray.menu, "acme/api#2 · Review the retry")
	if !ok {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	it.Action()
	d, ok := find(r.tray.menu, "reply: on it")
	if !ok {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	d.Action()
	waitFor(t, func() bool {
		o := r.openedURLs()
		return len(o) == 2 && slices.Contains(o, "cockpit#task=e-2") && slices.Contains(o, "cockpit#task=draft:5")
	})
}

func TestRowClickOpensItInTheCockpit(t *testing.T) {
	r := newRig(t)
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	r.c.Refresh(context.Background())
	it, _ := find(r.tray.menu, "acme/api#2")
	it.Action()
	waitFor(t, func() bool {
		o := r.openedURLs()
		return len(o) == 1 && o[0] == "cockpit#view=loop&unit=github/acme%2Fapi%232"
	})
}

// needsYou is the queue of every kind with an item for each of tasks.
func needsYou(tasks ...api.TaskView) *api.Queue {
	q := &api.Queue{Title: "Needs you", View: "needs-you", Total: len(tasks), Items: []api.QueueItem{}}
	for _, e := range tasks {
		q.Items = append(q.Items, api.QueueItem{Kind: "answer", Task: e})
	}
	return q
}

func TestMenuShowsNeedsYouCount(t *testing.T) {
	r := newRig(t)
	var waiting []api.TaskView
	for i := 1; i <= 7; i++ {
		waiting = append(waiting, plainTask(i))
	}
	r.d.queue = needsYou(waiting...)
	r.d.tasks["inbox"] = []api.TaskView{plainTask(20), plainTask(21)}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	at := index(got, "Needs you (7)")
	if at < 0 || index(got, "Inbox (") >= 0 || index(got, "Focus (") >= 0 || index(got, "Outbox (") >= 0 {
		t.Fatalf("menu %q, want one Needs you section instead of a section per view", got)
	}
	for i := 1; i <= perSection; i++ {
		if want := "acme/api#" + strconv.Itoa(i) + " · Review the retry"; got[at+i] != want {
			t.Fatalf("menu %q, want the queue's first items in its order", got)
		}
	}
	if got[at+perSection+1] != "more…" {
		t.Fatalf("menu %q, want more… after the first %d", got, perSection)
	}
	r.tray.menu[at+1].Action()
	r.tray.menu[at+perSection+1].Action()
	waitFor(t, func() bool {
		o := r.openedURLs()
		return len(o) == 2 && slices.Contains(o, "cockpit#task=e-1") && slices.Contains(o, "cockpit#view=needs-you")
	})
}

func TestTitleIsTheNeedsYouTotal(t *testing.T) {
	r := newRig(t)
	r.d.queue = needsYou(plainTask(1), plainTask(1), plainTask(2))
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 7}}
	r.c.Refresh(context.Background())
	if got, menu := r.title(), titles(r.tray.menu); got != brand.Name+" 3" || index(menu, "Needs you (3)") < 0 {
		t.Fatalf("title %q, menu %q: want both to read the queue's 3 items", got, menu)
	}
}

// A task with two decisions lists twice in Needs you, each line worded by
// the kind of its decision.
func TestNeedsYouWordsEachItemByItsKind(t *testing.T) {
	r := newRig(t)
	e := plainTask(1)
	r.d.queue = &api.Queue{Title: "Needs you", View: "needs-you", Total: 2, Items: []api.QueueItem{
		{Kind: "answer-question", Word: "question", Task: e},
		{Kind: "send-draft", Word: "draft", Task: e},
	}}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	label := taskLabel(e)
	if index(got, "question · "+label) < 0 || index(got, "draft · "+label) < 0 {
		t.Fatalf("menu %q, want each item worded by its kind", got)
	}
}

// A queue the daemon fails to serve keeps the menu, its title and the banners
// going on the last queue read, as the daemon's other reads do not hang on it.
func TestAQueueReadThatFailsKeepsTheMenuAndTheBanners(t *testing.T) {
	r := newRig(t)
	r.d.queue = needsYou(plainTask(1))
	r.c.Refresh(context.Background())
	r.d.mu.Lock()
	r.d.queueErr = errors.New("the queue failed")
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(2)}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	if got, menu := r.title(), titles(r.tray.menu); got != brand.Name+" 1" || index(menu, "Needs you (1)") < 0 {
		t.Fatalf("title %q, menu %q: want the last queue read's", got, menu)
	}
	if len(r.notes.posted) != 1 {
		t.Fatalf("posted %+v, want the inbox task's banner", r.notes.posted)
	}
}
