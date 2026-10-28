package menubar

import (
	"context"
	"fmt"
	"net/url"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// coreSections are the task views the menu lists first, in order.
var coreSections = []struct {
	view  task.View
	label string
}{{task.ViewFocus, "Focus"}, {task.ViewInbox, "Inbox"}, {task.ViewOutbox, "Outbox"}}

// pullTasks reads each core view whole: a task's attention also moves
// with its row's notice and its proposed draft, which no task write marks,
// so a delta would miss it. The badge comes with the Inbox's page, so the
// title and the Inbox agree.
func (c *Controller) pullTasks(ctx context.Context) error {
	for _, sec := range coreSections {
		page, err := c.cfg.Daemon.TasksSince(ctx, client.TaskQuery{View: string(sec.view)}, api.Since{})
		if err != nil {
			return err
		}
		c.tasks[sec.view] = page.Tasks
		if sec.view == task.ViewInbox {
			counters := page.Counters
			c.counters = &counters
		}
	}
	return nil
}

func (c *Controller) viewTasks(v task.View) []api.TaskView { return c.tasks[v] }

func (c *Controller) title() string {
	badge := 0
	if c.counters != nil {
		badge = c.counters.Badge
	}
	return fmt.Sprintf("PR %d", badge)
}

func (c *Controller) taskSection(label string, view task.View) []MenuItem {
	tasks := c.viewTasks(view)
	items := []MenuItem{{Title: fmt.Sprintf("%s (%d)", label, len(tasks))}}
	for i, e := range tasks {
		if i == perSection {
			items = append(items, MenuItem{Title: "more…", Enabled: true, Action: c.later(func(context.Context) error { return c.open("view=" + string(view)) })})
			break
		}
		items = append(items, MenuItem{Title: taskLabel(e), Enabled: true, Action: c.later(func(context.Context) error { return c.open(taskRoute(e.ID)) })})
	}
	return items
}

func taskLabel(e api.TaskView) string {
	if len(e.Units) == 0 {
		return e.Title
	}
	return e.UnitLabel(0) + " · " + e.Title
}

func taskRoute(id string) string { return "task=" + id }

func viewRoute(v string) string { return "view=" + url.QueryEscape(v) }

func unitRoute(u unit.Unit) string {
	return "unit=" + string(u.Provider) + "/" + url.QueryEscape(u.Key)
}
