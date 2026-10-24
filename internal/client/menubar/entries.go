package menubar

import (
	"context"
	"fmt"
	"net/url"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// coreSections are the GTD views the menu lists first, in order.
var coreSections = []struct {
	view  gtd.View
	label string
}{{gtd.ViewFocus, "Focus"}, {gtd.ViewInbox, "Inbox"}, {gtd.ViewOutbox, "Outbox"}}

// pullEntries reads each core view whole: an entry's attention also moves
// with its row's notice and its proposed draft, which no entry write marks,
// so a delta would miss it. The badge comes with the Inbox's page, so the
// title and the Inbox agree.
func (c *Controller) pullEntries(ctx context.Context) error {
	for _, sec := range coreSections {
		page, err := c.cfg.Daemon.EntriesSince(ctx, client.EntryQuery{View: string(sec.view)}, api.Since{})
		if err != nil {
			return err
		}
		c.entries[sec.view] = page.Entries
		if sec.view == gtd.ViewInbox {
			counters := page.Counters
			c.counters = &counters
		}
	}
	return nil
}

func (c *Controller) viewEntries(v gtd.View) []api.EntryView { return c.entries[v] }

func (c *Controller) title() string {
	badge := 0
	if c.counters != nil {
		badge = c.counters.Badge
	}
	return fmt.Sprintf("PR %d", badge)
}

func (c *Controller) entrySection(label string, view gtd.View) []MenuItem {
	entries := c.viewEntries(view)
	items := []MenuItem{{Title: fmt.Sprintf("%s (%d)", label, len(entries))}}
	for i, e := range entries {
		if i == perSection {
			items = append(items, MenuItem{Title: "more…", Enabled: true, Action: c.later(func(context.Context) error { return c.open("view=" + string(view)) })})
			break
		}
		items = append(items, MenuItem{Title: entryLabel(e), Enabled: true, Action: c.later(func(context.Context) error { return c.open(entryRoute(e.ID)) })})
	}
	return items
}

func entryLabel(e api.EntryView) string {
	if len(e.Units) == 0 {
		return e.Title
	}
	return e.UnitLabel(0) + " · " + e.Title
}

func entryRoute(id string) string { return "entry=" + id }

func viewRoute(v string) string { return "view=" + url.QueryEscape(v) }

func unitRoute(u unit.Unit) string {
	return "unit=" + string(u.Provider) + "/" + url.QueryEscape(u.Key)
}
