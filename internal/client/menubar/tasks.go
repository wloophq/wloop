package menubar

import (
	"context"
	"fmt"
	"net/url"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// pullTasks reads the catalog, then each view the profile's badge counts, whole: the menu lists those views
// first, in the profile's order. A task's attention also moves with its row's notice and its proposed draft,
// which no task write marks, so a delta would miss it. The badge comes with the first view's page.
func (c *Controller) pullTasks(ctx context.Context) error {
	cat, err := c.cfg.Daemon.Views(ctx)
	if err != nil {
		return err
	}
	var views []api.TaskViewSpec
	tasks := map[string][]api.TaskView{}
	for _, v := range cat.Views {
		if !v.Badge {
			continue
		}
		page, err := c.cfg.Daemon.TasksSince(ctx, client.TaskQuery{View: v.ID}, api.Since{})
		if err != nil {
			return err
		}
		if len(views) == 0 {
			counters := page.Counters
			c.counters = &counters
		}
		views, tasks[v.ID] = append(views, v), page.Tasks
	}
	c.taskViews, c.tasks = views, tasks
	return nil
}

func (c *Controller) viewTasks(v string) []api.TaskView { return c.tasks[v] }

// firstView is the route of the first view the menu lists; the cockpit's own first view when it lists none.
func (c *Controller) firstView() string {
	if len(c.taskViews) == 0 {
		return ""
	}
	return viewRoute(c.taskViews[0].ID)
}

func (c *Controller) title() string {
	badge := 0
	if c.counters != nil {
		badge = c.counters.Badge
	}
	return fmt.Sprintf("PR %d", badge)
}

func (c *Controller) taskSection(label string, view string) []MenuItem {
	tasks := c.viewTasks(view)
	items := []MenuItem{{Title: fmt.Sprintf("%s (%d)", label, len(tasks))}}
	for i, e := range tasks {
		if i == perSection {
			items = append(items, MenuItem{Title: "more…", Enabled: true, Action: c.later(func(context.Context) error { return c.open(viewRoute(view)) })})
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
