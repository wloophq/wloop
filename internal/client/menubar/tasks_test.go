package menubar

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func plainTask(n int) api.TaskView {
	return api.TaskView{ID: fmt.Sprintf("e-%d", n), Kind: "action", State: "inbox", Title: "Review the retry", Units: []unit.Unit{uref(n)}}
}

func index(items []string, prefix string) int {
	return slices.IndexFunc(items, func(s string) bool { return strings.HasPrefix(s, prefix) })
}

func TestTitleIsTheBadge(t *testing.T) {
	r := newRig(t)
	r.d.rows["needs-me"] = []api.Row{reviewRow(1), reviewRow(2)}
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 0, "outbox": 0}, Badge: 4}
	r.c.Refresh(context.Background())
	if got := r.title(); got != "PR 4" {
		t.Fatalf("title %q, want the badge", got)
	}
	r.d.mu.Lock()
	r.d.counters.Badge = 6
	r.d.mu.Unlock()
	r.c.apply(context.Background(), api.Change{Type: "changed", Epoch: "e1", Rev: 1, Units: []unit.Unit{uref(1)}, Counters: &api.Counters{Badge: 6}})
	if got := r.title(); got != "PR 6" {
		t.Fatalf("title %q after a change with counters", got)
	}
}

func TestBadgeAgreesWithTheInbox(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{plainTask(1), plainTask(2), plainTask(3)}
	r.d.tasks["outbox"] = []api.TaskView{draftTask(4)}
	r.d.counters = api.Counters{Views: map[string]int{"inbox": 3, "outbox": 1}, Badge: 4}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	if r.title() != "PR 4" || index(got, "Inbox (3)") < 0 || index(got, "Outbox (1)") < 0 {
		t.Fatalf("title %q, menu %q", r.title(), got)
	}
}

func TestMenuListsFiveAndMore(t *testing.T) {
	r := newRig(t)
	for i := 1; i <= 7; i++ {
		r.d.tasks["inbox"] = append(r.d.tasks["inbox"], plainTask(i))
	}
	r.d.rows["loop"] = []api.Row{reviewRow(9)}
	r.d.health = api.Health{Jobs: []api.JobStatus{{Name: "ingest:github", LastOK: now.Add(-2 * time.Minute)}}}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	if got[0] != "Focus (0)" || index(got, "Inbox (7)") < 0 || !slices.Contains(got, "more…") || got[len(got)-1] != "Quit "+brand.Name {
		t.Fatalf("menu %q", got)
	}
	n := 0
	for _, it := range r.tray.menu {
		if strings.HasPrefix(it.Title, "acme/api#") {
			n++
		}
	}
	if n != 6 {
		t.Fatalf("%d listed, want 5 of the Inbox + 1 in loop: %q", n, got)
	}
	for _, want := range []string{"Outbox (0)", "In loop (1)", "Open cockpit", "Sweep now", "Pause automatic rounds", "Synced " + now.Add(-2*time.Minute).Local().Format("15:04")} {
		if _, ok := find(r.tray.menu, want); !ok {
			t.Errorf("menu lacks %q: %q", want, got)
		}
	}
	more, _ := find(r.tray.menu, "more…")
	more.Action()
	waitFor(t, func() bool { o := r.openedURLs(); return len(o) == 1 && o[0] == "cockpit#view=inbox" })
}

func TestPluginSectionsFollowTheCatalog(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{plainTask(1)}
	r.d.rows["needs-me"] = []api.Row{reviewRow(3)}
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	loop, outbox := index(got, "In loop (1)"), index(got, "Outbox (0)")
	if loop < 0 || outbox < 0 || loop < outbox || index(got, "Needs me") >= 0 {
		t.Fatalf("menu %q, want In loop below the core sections and no Needs me", got)
	}
	r.d.mu.Lock()
	r.d.plugins = []api.Plugin{{ID: unit.GitHub}, {ID: "thread", Enabled: true, UI: &api.PluginUI{Menubar: []api.MenubarSection{{Label: "Threads", View: "loop"}, {Label: "Odd", View: "x&entry=e-1"}}}}}
	r.d.mu.Unlock()
	r.c.apply(context.Background(), api.Change{Health: true})
	got = titles(r.tray.menu)
	if index(got, "In loop") >= 0 || index(got, "Threads (1)") < index(got, "Outbox (0)") || index(got, "Inbox (1)") < 0 {
		t.Fatalf("menu with github disabled %q", got)
	}
	r.d.mu.Lock()
	r.d.rows["x&entry=e-1"] = []api.Row{reviewRow(1), reviewRow(2), reviewRow(3), reviewRow(4), reviewRow(5), reviewRow(6)}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	odd := index(titles(r.tray.menu), "Odd (6)")
	if odd < 0 || r.tray.menu[odd+6].Title != "more…" {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	r.tray.menu[odd+6].Action()
	waitFor(t, func() bool { return slices.Equal(r.openedURLs(), []string{"cockpit#view=x%26entry%3De-1"}) })
}

func TestAStoreTheDaemonCannotOpenShowsWhyAndTheCommand(t *testing.T) {
	r := newRig(t)
	var copied []string
	r.c.cfg.Copy = func(s string) error { copied = append(copied, s); return nil }
	r.d.rowsErr = errors.New("the store is not open")
	r.d.health = api.Health{Store: &api.StoreStatus{State: api.StoreFailed, Message: "the store migration from version 1 failed", Fix: "fix-it migrate"}}
	r.c.Refresh(context.Background())
	if r.title() != "PR !" {
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
	if r.title() != "PR ?" {
		t.Fatalf("title %q", r.title())
	}
	got := titles(r.tray.menu)
	if !strings.Contains(got[0], "connection refused") || got[len(got)-1] != "Quit "+brand.Name {
		t.Fatalf("menu %q", got)
	}
	r.d.mu.Lock()
	r.d.err = nil
	r.d.tasks["inbox"] = []api.TaskView{plainTask(2)}
	r.d.counters = api.Counters{Badge: 1}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	if r.title() != "PR 1" {
		t.Fatalf("did not recover: %q", r.title())
	}
}

func TestTaskClickOpensItInTheCockpit(t *testing.T) {
	r := newRig(t)
	r.d.tasks["focus"] = []api.TaskView{plainTask(2)}
	r.d.tasks["outbox"] = []api.TaskView{{ID: "draft:5", Kind: "draft", State: "inbox", Title: "reply: on it"}}
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
