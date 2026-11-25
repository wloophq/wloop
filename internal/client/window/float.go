package window

import (
	"encoding/json"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

// FloatRoute is the cockpit route the floating widget loads.
const FloatRoute = "/float"

// Rect is a frame in points, its origin at the top left of the main screen
// and Y growing down.
type Rect struct{ X, Y, W, H float64 }

func (r Rect) right() float64  { return r.X + r.W }
func (r Rect) bottom() float64 { return r.Y + r.H }

// Screen is a screen's frame and its visible frame, without the menu bar and
// the Dock.
type Screen struct{ Frame, Visible Rect }

// FloatPlace is what the float's page needs of its placement: the edge it
// is docked on, whether its anchor is in the lower half of its screen, so
// the panel opens upward, whether it is in the right half, so the panel
// opens leftward, and the room from its anchor to the far side of its
// screen's visible frame, which bounds what the page draws.
type FloatPlace struct {
	Edge       string
	Up         bool
	Leftward   bool
	MaxW, MaxH float64
}

// FloatBridge is the page's global the host calls setMode on.
var FloatBridge = brand.Command + "Float"

// overlap is the area r and o share.
func (r Rect) overlap(o Rect) float64 {
	w := min(r.right(), o.right()) - max(r.X, o.X)
	h := min(r.bottom(), o.bottom()) - max(r.Y, o.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// The edges a float docks on, and the corners it grows away from.
const (
	EdgeLeft          = "left"
	EdgeRight         = "right"
	CornerTopLeft     = "tl"
	CornerTopRight    = "tr"
	CornerBottomLeft  = "bl"
	CornerBottomRight = "br"
)

// FloatPlacement is where the person left the float: docked on an Edge of
// its screen or free, and the point of its anchor Corner, which stays put as
// the page resizes the frame.
type FloatPlacement struct {
	Edge   string  `json:"edge,omitempty"`
	Corner string  `json:"corner"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	W      float64 `json:"w"`
	H      float64 `json:"h"`
}

func (p FloatPlacement) valid() bool {
	switch p.Corner {
	case CornerTopLeft, CornerTopRight, CornerBottomLeft, CornerBottomRight:
	default:
		return false
	}
	return p.Edge == "" || p.Edge == EdgeLeft || p.Edge == EdgeRight
}

func (p FloatPlacement) frame() Rect {
	r := Rect{X: p.X, Y: p.Y, W: p.W, H: p.H}
	if strings.HasSuffix(p.Corner, "r") {
		r.X -= p.W
	}
	if strings.HasPrefix(p.Corner, "b") {
		r.Y -= p.H
	}
	return r
}

// snapDistance is how near an edge a dropped float docks on it.
const snapDistance = 16

// screenOf is the visible frame of the screen whose frame holds most of r,
// the main screen's (the first) when no screen holds any of it.
func screenOf(r Rect, screens []Screen) Rect {
	best, most := screens[0], 0.0
	for _, s := range screens {
		if a := r.overlap(s.Frame); a > most {
			best, most = s, a
		}
	}
	return best.Visible
}

// ClampToScreens is the frame p puts the float in: on the screen holding it,
// or on the main screen, the first of screens, when that screen is gone; no
// larger than that screen's visible frame, flush with its docked edge, and
// inside the visible frame.
func ClampToScreens(p FloatPlacement, screens []Screen) Rect {
	if len(screens) == 0 {
		return p.frame()
	}
	s := screenOf(p.frame(), screens)
	p.W, p.H = min(p.W, s.W), min(p.H, s.H)
	r := p.frame()
	switch p.Edge {
	case EdgeLeft:
		r.X = s.X
	case EdgeRight:
		r.X = s.right() - r.W
	}
	r.X = max(min(r.X, s.right()-r.W), s.X)
	r.Y = max(min(r.Y, s.bottom()-r.H), s.Y)
	return r
}

// RoomOf is how wide and tall the float at p may grow: from its anchor
// corner to the far side of its screen's visible frame, so a frame within it
// never needs the clamp to move the anchor, and the pill stays put as the
// panel opens.
func RoomOf(p FloatPlacement, screens []Screen) (w, h float64) {
	if len(screens) == 0 {
		return p.W, p.H
	}
	s := screenOf(p.frame(), screens)
	x, y := max(min(p.X, s.right()), s.X), max(min(p.Y, s.bottom()), s.Y)
	w, h = s.right()-x, s.bottom()-y
	if strings.HasSuffix(p.Corner, "r") {
		w = x - s.X
	}
	if strings.HasPrefix(p.Corner, "b") {
		h = y - s.Y
	}
	return w, h
}

// PlacementOf is the placement of a frame the person dropped: docked on the
// edge it was dropped near, anchored at the corner of its screen's quarter,
// so a panel opening from it grows toward the screen's middle.
func PlacementOf(frame Rect, screens []Screen) FloatPlacement {
	p := FloatPlacement{W: frame.W, H: frame.H}
	if len(screens) == 0 {
		p.Corner, p.X, p.Y = CornerTopLeft, frame.X, frame.Y
		return p
	}
	s := screenOf(frame, screens)
	switch {
	case frame.X-s.X <= snapDistance:
		p.Edge = EdgeLeft
	case s.right()-frame.right() <= snapDistance:
		p.Edge = EdgeRight
	}
	vertical, horizontal := "t", "l"
	if frame.Y+frame.H/2 >= s.Y+s.H/2 {
		vertical = "b"
	}
	if p.Edge == EdgeRight || p.Edge == "" && frame.X+frame.W/2 >= s.X+s.W/2 {
		horizontal = "r"
	}
	p.Corner = vertical + horizontal
	p.X, p.Y = frame.X, frame.Y
	if horizontal == "r" {
		p.X = frame.right()
	}
	if vertical == "b" {
		p.Y = frame.bottom()
	}
	switch p.Edge {
	case EdgeLeft:
		p.X = s.X
	case EdgeRight:
		p.X = s.right()
	}
	return p
}

// The frame's size before the page sends its own.
const floatStartW, floatStartH = 112, 32

// defaultPlacement docks the float on the main screen's right edge, a
// quarter down its visible frame.
func defaultPlacement(main Screen) FloatPlacement {
	v := main.Visible
	return FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: v.right(), Y: v.Y + v.H/4, W: floatStartW, H: floatStartH}
}

// Defaults is the app's user defaults, whose domain is the app bundle's
// identifier, brand.LaunchAgent.
type Defaults interface {
	String(key string) string
	SetString(key, value string)
}

// PlacementKey is the defaults key the float's placement is saved under.
var PlacementKey = brand.LaunchAgent() + ".float"

// SavePlacement keeps p in d for the next launch.
func SavePlacement(d Defaults, p FloatPlacement) {
	b, _ := json.Marshal(p)
	d.SetString(PlacementKey, string(b))
}

// LoadPlacement is the placement saved in d, at the start size when it was
// saved without one; false when none is, or it does not read.
func LoadPlacement(d Defaults) (FloatPlacement, bool) {
	var p FloatPlacement
	if json.Unmarshal([]byte(d.String(PlacementKey)), &p) != nil || !p.valid() {
		return FloatPlacement{}, false
	}
	if p.W <= 0 || p.H <= 0 {
		p.W, p.H = floatStartW, floatStartH
	}
	return p, true
}

// The messages the float's page sends the host, and the modes it draws in.
const (
	FloatSize = "float:size"
	FloatOpen = "float:open"
	FloatHide = "float:hide"
	ModePill  = "pill"
	ModePanel = "panel"
	ModePeek  = "peek"
)

// FloatMessage is a message of the float's page: the size it draws at and
// the mode it draws in, a route to open in the main window, or a way to
// hide.
type FloatMessage struct {
	Kind  string
	W, H  float64
	Mode  string
	Route string
}

// ParseFloatMessage reads raw, a JSON object naming its message in "type";
// false for any other message.
func ParseFloatMessage(raw string) (FloatMessage, bool) {
	var body struct {
		Type  string
		W, H  float64
		Mode  string
		Route string
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return FloatMessage{}, false
	}
	m := FloatMessage{Kind: body.Type, W: body.W, H: body.H, Mode: body.Mode, Route: body.Route}
	switch m.Kind {
	case FloatSize:
		if m.W <= 0 || m.H <= 0 {
			return FloatMessage{}, false
		}
		return m, true
	case FloatOpen, FloatHide:
		return m, true
	}
	return FloatMessage{}, false
}

// FloatKeyable says whether the float may take the keyboard in mode: only
// the open panel does, so hovering or dragging the pill never takes it from
// the app the person types in.
func FloatKeyable(mode string) bool { return mode == ModePanel }

// ShowFloat shows the floating widget where the person left it, loading its
// page unless the page loaded at the same URL has reported in. When the
// daemon cannot serve the page yet the float stays wanted, and Run shows it
// once the daemon's events answer.
func (c *Controller) ShowFloat() error {
	c.mu.Lock()
	c.floatWanted = true
	c.mu.Unlock()
	u, err := c.url(FloatRoute)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.floatWanted {
		return nil
	}
	reload := u != c.floatURL || !c.pageReady
	if reload {
		c.floatURL, c.pageReady = u, false
	}
	c.floatShown, c.tellPlace = true, true
	c.placeFloat()
	c.cfg.Host.ShowFloat(u, reload)
	return nil
}

// refloat shows the wanted float again as the daemon's events answer: after
// they were lost, or while its page has not reported in, so a page the daemon
// refused or could not serve loads anew.
func (c *Controller) refloat(back bool) {
	c.mu.Lock()
	again := c.floatWanted && (back || !c.pageReady)
	c.mu.Unlock()
	if !again {
		return
	}
	if err := c.ShowFloat(); err != nil {
		c.cfg.Log("float: %v", err)
	}
}

// HideFloat hides the floating widget.
func (c *Controller) HideFloat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatWanted, c.floatShown = false, false
	c.cfg.Host.HideFloat()
}

// FloatReady says whether the float shows a page that has reported in since
// it last loaded, so it carries what waits on the user.
func (c *Controller) FloatReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.floatShown && c.pageReady
}

// OnFloatReady sets what runs when the float's page first reports in after a
// load; until set, nothing.
func (c *Controller) OnFloatReady(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatReady = fn
}

// OnFloatHide sets what a hide the float's page asks for does; until set,
// nothing.
func (c *Controller) OnFloatHide(fn func(mode string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatHide = fn
}

// OnFloatHotkey sets what the float's hotkey does; until set, nothing.
func (c *Controller) OnFloatHotkey(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatHotkey = fn
}

func (c *Controller) pressFloatHotkey() {
	c.mu.Lock()
	press := c.floatHotkey
	c.mu.Unlock()
	if press != nil {
		press()
	}
}

// placeFloat moves a shown float to its placement on the known screens,
// unless the person is dragging it, then tells a loaded page where it sits
// when its placement was set anew; it runs under c.mu.
func (c *Controller) placeFloat() {
	if !c.floatShown || len(c.screens) == 0 || c.dragging {
		return
	}
	if !c.placed {
		size := c.placement
		c.placement, c.placed = defaultPlacement(c.screens[0]), true
		if size.W > 0 {
			c.placement.W, c.placement.H = size.W, size.H
		}
	}
	frame := ClampToScreens(c.placement, c.screens)
	c.cfg.Host.SetFloatFrame(frame)
	if c.tellPlace && c.pageReady {
		w, h := RoomOf(c.placement, c.screens)
		c.cfg.Host.PlaceFloat(FloatPlace{Edge: c.placement.Edge, Up: strings.HasPrefix(c.placement.Corner, "b"),
			Leftward: strings.HasSuffix(c.placement.Corner, "r"), MaxW: w, MaxH: h})
		c.tellPlace = false
	}
}

func (c *Controller) floatMessage(m FloatMessage) {
	c.mu.Lock()
	ready := c.floatReady
	if c.pageReady || !c.floatShown {
		ready = nil
	}
	c.pageReady = true
	c.mu.Unlock()
	if ready != nil {
		ready()
	}
	switch m.Kind {
	case FloatSize:
		c.mu.Lock()
		defer c.mu.Unlock()
		c.placement.W, c.placement.H = m.W, m.H
		c.placeFloat()
	case FloatOpen:
		_ = c.Show(m.Route)
	case FloatHide:
		c.mu.Lock()
		hide := c.floatHide
		c.mu.Unlock()
		if hide != nil {
			hide(m.Mode)
		}
	}
}

func (c *Controller) floatMoving() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dragging = true
}

// floatDropped keeps where the person dropped the float and snaps it there,
// at the size the page last sent.
func (c *Controller) floatDropped(frame Rect) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dragging = false
	if len(c.screens) == 0 {
		return
	}
	p := PlacementOf(frame, c.screens)
	if c.placement.W > 0 {
		p.W, p.H = c.placement.W, c.placement.H
	}
	c.placement, c.placed, c.tellPlace = p, true, true
	if c.cfg.Defaults != nil {
		SavePlacement(c.cfg.Defaults, c.placement)
	}
	c.placeFloat()
}

func (c *Controller) setScreens(screens []Screen) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.screens, c.tellPlace = screens, true
	c.placeFloat()
}
