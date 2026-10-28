package menubar

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// posted is what the buttons of a notification answer: its task, the
// task's units, the basis each button was served for and the options of
// its question.
type posted struct {
	task     string
	subtitle string
	units    []unit.Unit
	basis    map[string]string
	options  []record.Option
}

// draftButtons are the only buttons a draft's notification offers: a draft
// is approved where its whole text shows.
var draftButtons = []string{"open", api.TaskActionPrefix + string(task.VerbDismiss)}

// notify posts the attention of the tasks that are new, and withdraws the
// notifications of tasks that left the Inbox and of drafts that left the
// Outbox. A held task's id holds its day, so it is posted once a day.
func (c *Controller) notify() {
	today := c.cfg.Now().Local().Format(time.DateOnly)
	postedDaily := false
	live := map[string]bool{}
	notes := map[string]posted{}
	for _, e := range c.notifying() {
		n, p := notification(e)
		if _, dup := notes[n.ID]; dup {
			continue
		}
		notes[n.ID] = p
		held := Category(e.Attention.Category) == api.CategoryHeld
		c.mu.Lock()
		inSent, spent := c.sent[n.ID], c.daily[n.ID]
		c.mu.Unlock()
		if held && !inSent && (spent || !c.mayPost()) {
			continue
		}
		live[n.ID] = true
		if inSent {
			continue
		}
		c.mu.Lock()
		c.sent[n.ID] = true
		c.mu.Unlock()
		if err := c.cfg.Notifier.Post(n); err != nil {
			c.cfg.Log("notification %s: %v", n.ID, err)
			c.mu.Lock()
			delete(c.sent, n.ID)
			c.mu.Unlock()
		} else if held {
			c.mu.Lock()
			c.daily[n.ID] = true
			c.mu.Unlock()
			postedDaily = true
		}
	}
	if postedDaily {
		c.saveSent(today)
	}
	var gone []string
	c.mu.Lock()
	c.posted = notes
	for id := range c.sent {
		if !live[id] {
			gone = append(gone, id)
			delete(c.sent, id)
		}
	}
	c.mu.Unlock()
	if len(gone) > 0 {
		slices.Sort(gone)
		c.cfg.Notifier.Withdraw(gone)
	}
}

// notifying are the tasks of the menu's views that carry an attention.
func (c *Controller) notifying() []api.TaskView {
	var out []api.TaskView
	for _, sec := range coreSections {
		for _, e := range c.viewTasks(sec.view) {
			if e.Attention != nil {
				out = append(out, e)
			}
		}
	}
	return out
}

// notification is the task's attention as the daemon gave it, with what
// its buttons answer.
func notification(e api.TaskView) (Notification, posted) {
	a := *e.Attention
	n := Notification{ID: a.ID, Title: a.Title, Subtitle: a.Subtitle, Body: a.Body, Image: Image(a.Image)}
	p := posted{task: e.ID, subtitle: a.Subtitle, units: e.Units, basis: map[string]string{}}
	if m, _, ok := e.MessageOf(); ok {
		p.options = m.Options
	}
	for _, x := range a.Actions {
		if Category(a.Category) == api.CategoryOutbox && !slices.Contains(draftButtons, x.ID) {
			continue
		}
		if x.Basis != "" {
			p.basis[x.ID] = x.Basis
		}
		n.Actions = append(n.Actions, NotificationAction{ID: x.ID, Title: x.Title, TextInput: x.TextInput})
	}
	n.Category = displayCategory(Category(a.Category), n.Actions)
	return n, p
}

// displayCategory is the category macOS shows n's buttons under: a question
// keeps its own, any other gets one per set of buttons, since a category
// shows the buttons last registered for it on every notification it holds.
func displayCategory(cat Category, actions []NotificationAction) Category {
	if _, question := cat.Record(); question || len(actions) == 0 {
		return cat
	}
	h := fnv.New64a()
	for _, a := range actions {
		fmt.Fprintf(h, "%s\x00%s\x00%t\x00", a.ID, a.Title, a.TextInput)
	}
	return cat + "." + Category(strconv.FormatUint(h.Sum64(), 36))
}

// Respond turns a notification answer into the task's verb, its answer, or
// the catalog action on its first unit; opening the cockpit, and a draft's
// open, are the app's own.
func (c *Controller) Respond(ctx context.Context, r Response) {
	c.cfg.Log("notification response %s: action %q as %s, option %q, yes %v, text of %d bytes",
		r.ID, r.Identifier, r.Action, r.OptionID, r.Yes, len(r.Text))
	cat, task, ok := api.ParseNotificationID(r.ID)
	if !ok {
		c.cfg.Log("notification answer for an unknown id %q", r.ID)
		return
	}
	var err error
	switch {
	case r.Action == ActionOpen || r.Action == ActionServed && cat == api.CategoryOutbox && r.Identifier == "open":
		err = c.open(routeOf(cat, task))
	case r.Action == ActionAnswer:
		err = c.answer(ctx, r, task)
	case r.Action == ActionReply:
		err = c.reply(ctx, r, task)
	case r.Action == ActionServed:
		err = c.served(ctx, r, cat, task)
	}
	if err != nil {
		c.cfg.Log("%s %s: %v", r.Action, task, err)
	}
}

// routeOf is the cockpit route a notification opens: a note's task part is
// its route, the digest opens Next, anything else its task.
func routeOf(cat Category, tk string) string {
	switch cat {
	case api.CategoryNote:
		return tk
	case api.CategoryDigest:
		return "view=" + string(task.ViewNext)
	}
	return taskRoute(tk)
}

func (c *Controller) answer(ctx context.Context, r Response, tk string) error {
	args := map[string]string{}
	if r.Text != "" {
		args["guidance"] = r.Text
	}
	if r.OptionID != "" {
		args["option_id"] = r.OptionID
		if args["guidance"] == "" {
			if a := c.optionAnswer(r.ID, r.OptionID); a != "" {
				args["guidance"] = a
			}
		}
	}
	if r.Yes {
		args["yes"] = "true"
	}
	if r.No {
		args["no"] = "true"
	}
	res, err := c.cfg.Daemon.TaskDo(ctx, tk, string(task.VerbAnswer), api.TaskRequest{Args: args, Source: AnswerSource})
	if err != nil {
		c.notifyNote(r.ID, tk, "Answer not sent", err.Error())
		return err
	}
	if slices.Contains(res.Lines, api.AnswerQueued) {
		c.notifyNote(r.ID, tk, "Answer queued", api.AnswerQueued)
	}
	return nil
}

// reply writes the notification's text as a reply on its task's unit, and says where it went.
func (c *Controller) reply(ctx context.Context, r Response, task string) error {
	if strings.TrimSpace(r.Text) == "" {
		c.notifyNote(r.ID, task, "Reply not saved", "Nothing was typed.")
		return nil
	}
	args := map[string]string{"text": r.Text}
	title := "Reply saved to the Outbox"
	if r.Send {
		args["send"], title = "now", "Reply sent"
	}
	res, err := c.cfg.Daemon.TaskDo(ctx, task, "reply", api.TaskRequest{Args: args, Source: AnswerSource})
	if err != nil {
		c.notifyNote(r.ID, task, "Reply not saved", err.Error())
		return err
	}
	body := ""
	if len(res.Lines) > 0 {
		body = res.Lines[len(res.Lines)-1]
	}
	c.notifyNote(r.ID, task, title, body)
	return nil
}

func (c *Controller) served(ctx context.Context, r Response, cat Category, task string) error {
	var err error
	if verb, ok := strings.CutPrefix(r.Identifier, api.TaskActionPrefix); ok {
		if cat == api.CategoryOutbox && !slices.Contains(draftButtons, r.Identifier) {
			return errors.New("a draft's notification offers no " + r.Identifier)
		}
		var args map[string]string
		if v, mode, ok := strings.Cut(verb, ":"); ok {
			verb, args = v, map[string]string{"mode": mode}
		}
		_, err = c.cfg.Daemon.TaskDo(ctx, task, verb, api.TaskRequest{Args: args, Source: source})
	} else {
		var u unit.Unit
		if u, err = c.unitOf(ctx, r.ID, task); err == nil {
			_, err = c.cfg.Daemon.Action(ctx, u, r.Identifier, api.ActionRequest{Guidance: r.Text, Source: source, Basis: c.basisOf(r.ID, r.Identifier)})
		}
	}
	if err != nil {
		c.notifyNote(r.ID, task, "Action not sent", err.Error())
	}
	return err
}

// unitOf is the first unit of the notification's task, read again from the
// daemon when the app restarted since it was posted.
func (c *Controller) unitOf(ctx context.Context, id, task string) (unit.Unit, error) {
	c.mu.Lock()
	p, ok := c.posted[id]
	c.mu.Unlock()
	units := p.units
	if !ok {
		e, err := c.cfg.Daemon.Task(ctx, task)
		if err != nil {
			return unit.Unit{}, err
		}
		units = e.Units
	}
	if len(units) == 0 {
		return unit.Unit{}, errors.New("task " + task + " has no unit to act on")
	}
	return units[0], nil
}

func (c *Controller) basisOf(id, action string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.posted[id].basis[action]
}

// optionAnswer is the option's answer as the agent reads it, "" when the
// question is no longer known; the daemon then words it the same way.
func (c *Controller) optionAnswer(id, option string) string {
	c.mu.Lock()
	options := c.posted[id].options
	c.mu.Unlock()
	for _, o := range options {
		if o.ID == option {
			return o.Answer()
		}
	}
	return ""
}

// notifyNote tells the user what became of an answer to notification id,
// since the log is out of sight; the note opens the task.
func (c *Controller) notifyNote(id, task, title, body string) {
	c.mu.Lock()
	subtitle := c.posted[id].subtitle
	c.mu.Unlock()
	c.postNote(Notification{
		ID:       api.NotificationID(api.CategoryNote, taskRoute(task), c.cfg.Now().UTC().Format("20060102T150405.000000000")),
		Title:    title,
		Subtitle: subtitle,
		Body:     body,
		Category: api.CategoryNote,
	})
}

// postNotice posts what a workflow's notify.self raised; it opens its unit.
func (c *Controller) postNotice(n api.Notice) {
	route := "view=" + string(task.ViewInbox)
	if !n.Unit.IsZero() {
		route = unitRoute(n.Unit)
	}
	c.postNote(Notification{ID: api.NotificationID(api.CategoryNote, route, url.QueryEscape(n.ID)), Title: n.Workflow, Subtitle: cmp.Or(n.Label, n.Unit.Label()), Body: n.Text, Category: api.CategoryNote})
}

func (c *Controller) postNote(n Notification) {
	if err := c.cfg.Notifier.Post(n); err != nil {
		c.cfg.Log("notification %s: %v", n.ID, err)
	}
}

// forgetSent drops the held tasks marked as posted, which the start has
// just cleared from the notification center, so today's is posted again;
// today's digest stays posted.
func (c *Controller) forgetSent() {
	c.mu.Lock()
	clear(c.sent)
	n := len(c.daily)
	for id := range c.daily {
		if cat, _, _ := api.ParseNotificationID(id); cat == api.CategoryHeld {
			delete(c.daily, id)
		}
	}
	forgot := n != len(c.daily)
	c.mu.Unlock()
	if forgot {
		c.saveSent(c.cfg.Now().Local().Format(time.DateOnly))
	}
}

type sentFile struct {
	Day string   `json:"day"`
	IDs []string `json:"ids"`
}

func (c *Controller) loadSent() {
	if c.cfg.SentPath == "" {
		return
	}
	raw, err := os.ReadFile(c.cfg.SentPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.cfg.Log("notifications: %v", err)
		}
		return
	}
	var f sentFile
	if json.Unmarshal(raw, &f) != nil || f.Day != c.cfg.Now().Local().Format(time.DateOnly) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range f.IDs {
		if dailyOn(id, f.Day) {
			c.daily[id] = true
		}
	}
}

// saveSent writes today's daily ids and forgets the other days'; saves run
// one at a time, so the last write holds the last ids.
func (c *Controller) saveSent(today string) {
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	f := sentFile{Day: today, IDs: []string{}}
	c.mu.Lock()
	for id := range c.daily {
		if dailyOn(id, today) {
			f.IDs = append(f.IDs, id)
		} else {
			delete(c.daily, id)
		}
	}
	c.mu.Unlock()
	if c.cfg.SentPath == "" {
		return
	}
	slices.Sort(f.IDs)
	raw, _ := json.Marshal(f)
	tmp := c.cfg.SentPath + ".tmp"
	err := os.WriteFile(tmp, raw, 0o600)
	if err == nil {
		err = os.Rename(tmp, c.cfg.SentPath)
	}
	if err != nil {
		c.cfg.Log("notifications: %v", err)
	}
}

// dailyOn says whether id is a held task's or the digest's of day.
func dailyOn(id, day string) bool {
	cat, _, ok := api.ParseNotificationID(id)
	return ok && (cat == api.CategoryHeld || cat == api.CategoryDigest) && strings.HasSuffix(id, "|"+day)
}

// mayPost reports whether a notification would be shown, so a held task or
// the digest, announced once a day, is not spent while notifications are
// off.
func (c *Controller) mayPost() bool {
	perm, err := c.cfg.Notifier.Permission(c.runCtx())
	return err == nil && (perm == PermissionAuthorized || perm == PermissionProvisional)
}
