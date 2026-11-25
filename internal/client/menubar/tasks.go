package menubar

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/window"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// pullTasks reads the catalog, each view the profile's badge counts, whole, whose tasks notify, and the queue
// of every decision kind, which the menu lists first. A task's attention also moves with its row's notice and
// its proposed draft, which no task write marks, so a delta would miss it. The counters come with the first
// view's page, or on their own when no view is badged.
func (c *Controller) pullTasks(ctx context.Context) error {
	cat, err := c.cfg.Daemon.Views(ctx)
	if err != nil {
		return err
	}
	var views []api.TaskViewSpec
	var loading []api.Loading
	tasks := map[string][]api.TaskView{}
	for _, v := range cat.Views {
		if !v.Badge || !v.Rail {
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
		for _, l := range page.Loading {
			if !slices.Contains(loading, l) {
				loading = append(loading, l)
			}
		}
	}
	if len(views) == 0 {
		counters, err := c.cfg.Daemon.Counters(ctx, "", "")
		if err != nil {
			return err
		}
		c.counters = &counters
	}
	needs, err := c.cfg.Daemon.QueueAll(ctx, "")
	if err != nil {
		return err
	}
	c.taskViews, c.tasks, c.tasksLoading, c.needs = views, tasks, loading, needs
	c.mu.Lock()
	c.cat = cat
	c.mu.Unlock()
	return nil
}

// verbOf is the served verb that runs handler, handler itself before the catalog was read.
func (c *Controller) verbOf(handler string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.cat.VerbOf(handler); ok {
		return v
	}
	return handler
}

// The handlers of the task verbs a notification's buttons run: answer its question, write its reply,
// discard its draft.
const (
	handlerAnswer = "answer"
	handlerReply  = "reply"
	handlerDrop   = "drop"
)

// loadingItems say which plugins are still loading their units, and why.
func (c *Controller) loadingItems() []MenuItem {
	var items []MenuItem
	for _, l := range append(slices.Clone(c.tasksLoading), c.rowsLoading...) {
		title := c.words().T("Loading {plugin}", map[string]any{"plugin": l.Plugin})
		if l.Reason != "" {
			title = c.words().T("Loading {plugin}: {reason}", map[string]any{"plugin": l.Plugin, "reason": l.Reason})
		}
		if !slices.ContainsFunc(items, func(it MenuItem) bool { return it.Title == title }) {
			items = append(items, MenuItem{Title: title})
		}
	}
	if len(items) > 0 {
		items = append(items, MenuItem{Separator: true})
	}
	return items
}

func (c *Controller) viewTasks(v string) []api.TaskView { return c.tasks[v] }

// title says how many items Needs you holds, and each view's count for a profile that words it.
func (c *Controller) title() string {
	params := map[string]any{}
	if c.counters != nil {
		for view, n := range c.counters.Views {
			params[view] = n
		}
	}
	params["name"], params["n"] = brand.Name, c.needs.Total
	if c.health.Paused {
		return c.words().T("{name} {n} paused", params)
	}
	return c.words().T("{name} {n}", params)
}

// needsSection is what waits on the user, as many as the queue holds, and its first items, each worded by its
// kind and opening its task; more opens the queue's view, else the home.
func (c *Controller) needsSection() []MenuItem {
	items := []MenuItem{{Title: fmt.Sprintf("%s (%d)", c.needs.Title, c.needs.Total)}}
	more := window.HomeRoute
	if c.needs.View != "" {
		more = viewRoute(c.needs.View)
	}
	for i, it := range c.needs.Items {
		if i == perSection {
			items = append(items, MenuItem{Title: c.words().T("more…", nil), Enabled: true, Action: c.later(func(context.Context) error { return c.open(more) })})
			break
		}
		e := it.Task
		title := taskLabel(e)
		if it.Word != "" {
			title = it.Word + " · " + title
		}
		items = append(items, MenuItem{Title: title, Enabled: true, Action: c.later(func(context.Context) error { return c.open(taskRoute(e.ID)) })})
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
