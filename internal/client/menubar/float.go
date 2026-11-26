package menubar

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/window"
)

// Visibility is how the floating widget shows: Mode, for a snooze the time
// it shows again, and while quiet the keys of the queue the user last saw;
// nil Seen is none read yet, an empty one an empty queue.
type Visibility struct {
	Mode  string    `json:"mode"`
	Until time.Time `json:"until,omitzero"`
	Seen  []string  `json:"seen"`
}

// The float's modes. Quiet hides it until something new arrives in the queue,
// snoozed until Until; off is the user's from the tray, and brings the
// Needs you banners back.
const (
	VisibilityShown   = "shown"
	VisibilityQuiet   = "quiet"
	VisibilitySnoozed = "snoozed"
	VisibilityOff     = "off"
)

// The ways the float's page asks to hide (window.FloatHide).
const (
	HideQuiet    = "quiet"
	HideHour     = "hour"
	HideTomorrow = "tomorrow"
)

// VisibilityKey is the defaults key the float's visibility is saved under,
// beside its placement.
var VisibilityKey = window.PlacementKey + ".visibility"

// snoozeHour is how long "Hide for 1 hour" hides the float.
const snoozeHour = time.Hour

// floatLoadGrace is how long a shown float whose page was asked to load
// carries the Needs you banners before its page reports in; past it, a page
// that never loaded lets them post.
const floatLoadGrace = 4 * time.Second

// snoozeTick is how often a snooze compares the wall clock with its end, since
// a timer stands still while the Mac sleeps.
const snoozeTick = time.Minute

type clockTime struct{ hour, minute int }

// morning is Config.FloatMorning, the local "HH:MM" a snooze until tomorrow
// ends at; false when it does not read.
func (c *Controller) morning() (clockTime, bool) {
	t, err := time.Parse("15:04", c.cfg.FloatMorning)
	if err != nil {
		c.cfg.Log("float: window.float_morning %q is not HH:MM", c.cfg.FloatMorning)
		return clockTime{}, false
	}
	return clockTime{t.Hour(), t.Minute()}, true
}

// nextMorning is the next time at after now, in now's location.
func nextMorning(now time.Time, at clockTime) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), at.hour, at.minute, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, at.hour, at.minute, 0, 0, now.Location())
	}
	return next
}

// floatOn says whether this app has a float the configuration turned on.
func (c *Controller) floatOn() bool { return c.cfg.Float && c.cfg.ShowFloat != nil }

func (c *Controller) visibility() Visibility {
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	return c.vis
}

// muteNeeds says whether the float carries what waits on the user, so no
// Needs you banner posts: while the user hid it for a while, or while it
// shows a page that has reported in or is within floatLoadGrace of loading.
func (c *Controller) muteNeeds() bool {
	if !c.floatOn() {
		return false
	}
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	switch {
	case !c.started || c.vis.Mode == VisibilityOff:
		return false
	case c.vis.Mode != VisibilityShown:
		return true
	}
	loading := !c.loadAt.IsZero() && c.cfg.Now().Sub(c.loadAt) < floatLoadGrace
	return loading || c.cfg.FloatReady == nil || c.cfg.FloatReady()
}

// FloatPageReady follows the float's page reporting in: the banners it now
// carries are withdrawn, off the caller's goroutine.
func (c *Controller) FloatPageReady() {
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	if c.running && !c.stopped {
		ctx := c.runCtx()
		c.bg.Go(func() { c.redraw(ctx) })
	}
}

// StartFloat puts the float as the last run left it, a snooze that has
// passed showing it; until it runs, nothing moves the float. It runs once:
// before the daemon answers when it is down at launch, so the float can say
// it is offline, else as Run starts.
func (c *Controller) StartFloat() {
	if !c.floatOn() {
		return
	}
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	if c.started || c.stopped {
		return
	}
	c.started = true
	v := c.loadVisibility()
	if v.Mode == VisibilitySnoozed && !c.cfg.Now().Before(v.Until) {
		v = Visibility{Mode: VisibilityShown}
	}
	c.vis = v
	if v.Mode == VisibilityQuiet && v.Seen != nil {
		c.seen = setOf(v.Seen)
	}
	if v.Mode == VisibilityShown {
		c.showFloat()
	}
}

// runFloat lets the float start goroutines, in bg, and arms a snooze's
// timer.
func (c *Controller) runFloat() {
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	if c.stopped {
		return
	}
	c.running, c.quit = true, make(chan struct{})
	if c.vis.Mode == VisibilitySnoozed {
		c.wakeAt(c.vis.Until)
	}
	c.armGrace()
}

// stopFloat keeps anything from moving the float from now on, then waits on
// the goroutines it started.
func (c *Controller) stopFloat() {
	c.floatMu.Lock()
	c.stopped, c.running = true, false
	if c.wake != nil {
		close(c.wake)
		c.wake = nil
	}
	if c.quit != nil {
		close(c.quit)
		c.quit = nil
	}
	c.floatMu.Unlock()
	c.bg.Wait()
}

func (c *Controller) loadVisibility() Visibility {
	shown := Visibility{Mode: VisibilityShown}
	if c.cfg.Defaults == nil {
		return shown
	}
	var v Visibility
	if json.Unmarshal([]byte(c.cfg.Defaults.String(VisibilityKey)), &v) != nil {
		return shown
	}
	switch v.Mode {
	case VisibilityShown, VisibilityOff:
		return Visibility{Mode: v.Mode}
	case VisibilityQuiet:
		return Visibility{Mode: v.Mode, Seen: v.Seen}
	case VisibilitySnoozed:
		return Visibility{Mode: v.Mode, Until: v.Until}
	}
	return shown
}

// FloatHide hides the float the way its page asked.
func (c *Controller) FloatHide(mode string) {
	now := c.cfg.Now()
	switch mode {
	case HideQuiet:
		c.setVisibility(Visibility{Mode: VisibilityQuiet})
	case HideHour:
		c.setVisibility(Visibility{Mode: VisibilitySnoozed, Until: now.Add(snoozeHour)})
	case HideTomorrow:
		if at, ok := c.morning(); ok {
			c.setVisibility(Visibility{Mode: VisibilitySnoozed, Until: nextMorning(now, at)})
		}
	default:
		c.cfg.Log("float: no way to hide %q", mode)
	}
}

// ToggleFloat is the hotkey's: it quiets a shown float and shows a hidden one.
func (c *Controller) ToggleFloat() { c.toggle(VisibilityQuiet) }

// trayFloat is the tray item's: it turns a shown float off and shows any
// other.
func (c *Controller) trayFloat() { c.toggle(VisibilityOff) }

func (c *Controller) toggle(hidden string) {
	if c.visibility().Mode == VisibilityShown {
		c.setVisibility(Visibility{Mode: hidden})
		return
	}
	c.setVisibility(Visibility{Mode: VisibilityShown})
}

// floatItem is the tray item that turns the float off and on, checked while
// it is on, hidden for a while included.
func (c *Controller) floatItem() []MenuItem {
	if !c.floatOn() {
		return nil
	}
	return []MenuItem{{Title: c.words().T("Floating widget", nil), Enabled: true, Checked: c.visibility().Mode != VisibilityOff, Action: c.trayFloat}}
}

// setVisibility moves the float to v and keeps it for the next launch; while
// Run runs, it redraws the menu and the banners off the caller's goroutine,
// which may be the host's or the main thread.
func (c *Controller) setVisibility(v Visibility) {
	if !c.floatOn() {
		return
	}
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	if !c.started || c.stopped {
		return
	}
	c.moveFloat(v)
	if c.running {
		ctx := c.runCtx()
		c.bg.Go(func() { c.redraw(ctx) })
	}
}

// moveFloat shows or hides the float as v asks and saves v; it runs under
// floatMu.
func (c *Controller) moveFloat(v Visibility) {
	was := c.vis.Mode
	if v.Mode == VisibilityQuiet && c.seen != nil {
		v.Seen = sortedKeys(c.seen)
	}
	c.vis = v
	if c.wake != nil {
		close(c.wake)
		c.wake = nil
	}
	switch {
	case v.Mode == VisibilityShown && was != VisibilityShown:
		c.showFloat()
	case v.Mode != VisibilityShown && was == VisibilityShown && c.cfg.HideFloat != nil:
		c.cfg.HideFloat()
	}
	if v.Mode == VisibilitySnoozed {
		c.wakeAt(v.Until)
	}
	c.saveVisibility()
}

func (c *Controller) saveVisibility() {
	if c.cfg.Defaults == nil {
		return
	}
	b, _ := json.Marshal(c.vis)
	c.cfg.Defaults.SetString(VisibilityKey, string(b))
}

// showFloat asks the window to show the float, whose page then has
// floatLoadGrace to report in; it runs under floatMu.
func (c *Controller) showFloat() {
	if err := c.cfg.ShowFloat(); err != nil {
		c.cfg.Log("float: %v", err)
		c.loadAt = time.Time{}
		return
	}
	c.loadAt = c.cfg.Now()
	c.armGrace()
}

// armGrace redraws the banners as the float's load grace ends, so they post
// when its page never reported in; it runs under floatMu, and starts nothing
// unless Run runs.
func (c *Controller) armGrace() {
	left := floatLoadGrace - c.cfg.Now().Sub(c.loadAt)
	if !c.running || c.stopped || c.loadAt.IsZero() || left <= 0 {
		return
	}
	ctx, quit, wait := c.runCtx(), c.quit, c.cfg.After(left)
	c.bg.Go(func() {
		select {
		case <-ctx.Done():
		case <-quit:
		case <-wait:
			c.redraw(ctx)
		}
	})
}

// wakeAt shows the float at until, or the first tick past it on the wall
// clock, unless it moved before; it runs under floatMu, and starts nothing
// unless Run runs.
func (c *Controller) wakeAt(until time.Time) {
	if !c.running || c.stopped {
		return
	}
	stop := make(chan struct{})
	c.wake = stop
	ctx := c.runCtx()
	wait := c.cfg.After(until.Sub(c.cfg.Now()))
	c.bg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-wait:
				c.snoozeEnded(until)
				return
			case <-c.cfg.After(snoozeTick):
				if !c.cfg.Now().Before(until) {
					c.snoozeEnded(until)
					return
				}
			}
		}
	})
}

func (c *Controller) snoozeEnded(until time.Time) {
	c.floatMu.Lock()
	if c.vis.Mode != VisibilitySnoozed || !c.vis.Until.Equal(until) {
		c.floatMu.Unlock()
		return
	}
	c.wake = nil
	c.moveFloat(Visibility{Mode: VisibilityShown})
	c.floatMu.Unlock()
	c.redraw(c.runCtx())
}

// floatRefreshed follows a refresh that read the daemon. A snooze past its
// time shows the float, since a sleeping Mac stops its timer. When the
// refresh read the queue, a quiet float shows if the queue holds an item
// the last read did not, and otherwise keeps the queue as the user last saw
// it. The first read after launch only learns the queue, unless the float
// starts quiet with a saved one. It runs under refreshMu.
func (c *Controller) floatRefreshed(queueRead bool) {
	if !c.floatOn() {
		return
	}
	var keys map[string]bool
	if queueRead {
		keys = make(map[string]bool, len(c.needs.Items))
		for _, it := range c.needs.Items {
			keys[queueKey(it)] = true
		}
	}
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	if !c.started {
		return
	}
	if c.vis.Mode == VisibilitySnoozed && !c.cfg.Now().Before(c.vis.Until) {
		c.moveFloat(Visibility{Mode: VisibilityShown})
	}
	if !queueRead {
		return
	}
	fresh := false
	for k := range keys {
		fresh = fresh || c.seen != nil && !c.seen[k]
	}
	c.seen = keys
	if c.vis.Mode != VisibilityQuiet {
		return
	}
	if fresh {
		c.moveFloat(Visibility{Mode: VisibilityShown})
		return
	}
	if seen := sortedKeys(keys); c.vis.Seen == nil || !slices.Equal(seen, c.vis.Seen) {
		c.vis.Seen = seen
		c.saveVisibility()
	}
}

// sortedKeys are m's keys in order, an empty slice for an empty m.
func sortedKeys(m map[string]bool) []string {
	keys := slices.AppendSeq(make([]string, 0, len(m)), maps.Keys(m))
	slices.Sort(keys)
	return keys
}

func setOf(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// queueKey names a queue item across reads, as the float's page does: its
// gate, else its task's decision of its kind and its situation's provider,
// kind and unit.
func queueKey(it api.QueueItem) string {
	if it.Gate != nil {
		return "gate:" + strconv.FormatInt(it.Gate.ID, 10)
	}
	var s api.QueueSituation
	if it.Situation != nil {
		s = *it.Situation
	}
	return "task:" + strings.Join([]string{it.Task.ID, it.Kind, s.Provider, s.Kind, s.Unit}, ":")
}

// redraw draws the menu and the banners again after the float moved, unless
// the last refresh failed and its menu says why.
func (c *Controller) redraw(ctx context.Context) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if !c.drawn || ctx.Err() != nil {
		return
	}
	c.cfg.Tray.SetMenu(c.menu(c.health))
	c.notify()
}
