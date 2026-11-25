package menubar

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/window"
)

// Visibility is how the floating widget shows: Mode, and for a snooze the
// time it shows again.
type Visibility struct {
	Mode  string    `json:"mode"`
	Until time.Time `json:"until,omitzero"`
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

const morningHour = 9

// nextMorning is the next 09:00 after now, in now's location.
func nextMorning(now time.Time) time.Time {
	at := time.Date(now.Year(), now.Month(), now.Day(), morningHour, 0, 0, 0, now.Location())
	if !at.After(now) {
		at = time.Date(now.Year(), now.Month(), now.Day()+1, morningHour, 0, 0, 0, now.Location())
	}
	return at
}

// floatOn says whether this app has a float the configuration turned on.
func (c *Controller) floatOn() bool { return c.cfg.Float && c.cfg.ShowFloat != nil }

func (c *Controller) visibility() Visibility {
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	return c.vis
}

// muteNeeds says whether the float carries what waits on the user, so no
// Needs you banner posts.
func (c *Controller) muteNeeds() bool {
	return c.floatOn() && c.visibility().Mode != VisibilityOff
}

// startFloat puts the float as the last run left it; a snooze that has
// passed shows it.
func (c *Controller) startFloat() {
	if !c.floatOn() {
		return
	}
	v := c.loadVisibility()
	if v.Mode == VisibilitySnoozed && !c.cfg.Now().Before(v.Until) {
		v = Visibility{Mode: VisibilityShown}
	}
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	c.vis = v
	switch v.Mode {
	case VisibilityShown:
		c.showFloat()
	case VisibilitySnoozed:
		c.wakeAt(v.Until)
	}
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
	case VisibilityShown, VisibilityQuiet, VisibilityOff:
		return Visibility{Mode: v.Mode}
	case VisibilitySnoozed:
		return v
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
		c.setVisibility(Visibility{Mode: VisibilitySnoozed, Until: now.Add(time.Hour)})
	case HideTomorrow:
		c.setVisibility(Visibility{Mode: VisibilitySnoozed, Until: nextMorning(now)})
	default:
		c.cfg.Log("float: no way to hide %q", mode)
	}
}

// ToggleFloat is the hotkey's: it quiets a shown float and shows a hidden one.
func (c *Controller) ToggleFloat() {
	if c.visibility().Mode == VisibilityShown {
		c.setVisibility(Visibility{Mode: VisibilityQuiet})
		return
	}
	c.setVisibility(Visibility{Mode: VisibilityShown})
}

// trayFloat is the tray item's: it turns a shown float off and shows any
// other.
func (c *Controller) trayFloat() {
	if c.visibility().Mode == VisibilityShown {
		c.setVisibility(Visibility{Mode: VisibilityOff})
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

// setVisibility moves the float to v, keeps it for the next launch, then
// redraws the menu and the banners off the caller's goroutine, which may be
// the host's or the main thread.
func (c *Controller) setVisibility(v Visibility) {
	if !c.floatOn() {
		return
	}
	c.floatMu.Lock()
	c.moveFloat(v)
	c.floatMu.Unlock()
	ctx := c.runCtx()
	c.bg.Go(func() { c.redraw(ctx) })
}

// moveFloat shows or hides the float as v asks and saves v; it runs under
// floatMu.
func (c *Controller) moveFloat(v Visibility) {
	was := c.vis.Mode
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
	if c.cfg.Defaults != nil {
		b, _ := json.Marshal(v)
		c.cfg.Defaults.SetString(VisibilityKey, string(b))
	}
}

func (c *Controller) showFloat() {
	if err := c.cfg.ShowFloat(); err != nil {
		c.cfg.Log("float: %v", err)
	}
}

// wakeAt shows the float at until unless it moved before; it runs under
// floatMu.
func (c *Controller) wakeAt(until time.Time) {
	stop := make(chan struct{})
	c.wake = stop
	ctx := c.runCtx()
	wait := c.cfg.After(until.Sub(c.cfg.Now()))
	c.bg.Go(func() {
		select {
		case <-ctx.Done():
		case <-stop:
		case <-wait:
			c.snoozeEnded(until)
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

// floatArrivals shows a quiet float when the queue holds an item it did not
// hold on the last read, and a snoozed one whose time passed while its timer
// stood still, as on a sleeping Mac; the first read only learns the queue.
// It runs under refreshMu, after a read of the queue.
func (c *Controller) floatArrivals() {
	if !c.floatOn() {
		return
	}
	keys := make(map[string]bool, len(c.needs.Items))
	fresh := false
	for _, it := range c.needs.Items {
		k := queueKey(it)
		keys[k] = true
		fresh = fresh || c.seen != nil && !c.seen[k]
	}
	c.seen = keys
	c.floatMu.Lock()
	defer c.floatMu.Unlock()
	switch {
	case c.vis.Mode == VisibilityQuiet && fresh:
		c.moveFloat(Visibility{Mode: VisibilityShown})
	case c.vis.Mode == VisibilitySnoozed && !c.cfg.Now().Before(c.vis.Until):
		c.moveFloat(Visibility{Mode: VisibilityShown})
	}
}

// queueKey tells a queue item from every other: its kind, its task, and its
// gate or its situation.
func queueKey(it api.QueueItem) string {
	k := it.Kind + "|" + it.Task.ID
	if it.Gate != nil {
		k += "|" + strconv.FormatInt(it.Gate.ID, 10)
	}
	if s := it.Situation; s != nil {
		k += "|" + s.Provider + "/" + s.Kind + "/" + s.Label
	}
	return k
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
