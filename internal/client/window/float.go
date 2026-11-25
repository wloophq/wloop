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

func (r Rect) contains(x, y float64) bool {
	return x >= r.X && x < r.right() && y >= r.Y && y < r.bottom()
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

// screenOf is the visible frame of the screen holding r's center, the main
// screen (the first) when none does.
func screenOf(r Rect, screens []Rect) Rect {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	for _, s := range screens {
		if s.contains(cx, cy) {
			return s
		}
	}
	return screens[0]
}

// ClampToScreens is the frame p puts the float in: on the screen holding it,
// or on the main screen, the first of screens, when that screen is gone;
// flush with its docked edge, and inside the screen's visible frame.
func ClampToScreens(p FloatPlacement, screens []Rect) Rect {
	r := p.frame()
	if len(screens) == 0 {
		return r
	}
	s := screenOf(r, screens)
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

// PlacementOf is the placement of a frame the person dropped: docked on the
// edge it was dropped near, anchored at the corner of its screen's quarter,
// so a panel opening from it grows toward the screen's middle.
func PlacementOf(frame Rect, screens []Rect) FloatPlacement {
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
func defaultPlacement(main Rect) FloatPlacement {
	return FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: main.right(), Y: main.Y + main.H/4, W: floatStartW, H: floatStartH}
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

// LoadPlacement is the placement saved in d; false when none is, or it does
// not read.
func LoadPlacement(d Defaults) (FloatPlacement, bool) {
	var p FloatPlacement
	if json.Unmarshal([]byte(d.String(PlacementKey)), &p) != nil || !p.valid() {
		return FloatPlacement{}, false
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

// ParseFloatMessage reads raw, a message's name then its JSON payload,
// apart by a space or a colon; false for any other message.
func ParseFloatMessage(raw string) (FloatMessage, bool) {
	for _, kind := range []string{FloatSize, FloatOpen, FloatHide} {
		rest, ok := strings.CutPrefix(raw, kind)
		if !ok || rest == "" || rest[0] != ' ' && rest[0] != ':' {
			continue
		}
		var body struct {
			W, H  float64
			Mode  string
			Route string
		}
		if json.Unmarshal([]byte(rest[1:]), &body) != nil {
			return FloatMessage{}, false
		}
		m := FloatMessage{Kind: kind, W: body.W, H: body.H, Mode: body.Mode, Route: body.Route}
		if kind == FloatSize && (m.W <= 0 || m.H <= 0) {
			return FloatMessage{}, false
		}
		return m, true
	}
	return FloatMessage{}, false
}

// FloatKeyable says whether the float may take the keyboard in mode: only
// the open panel does, so hovering the pill never takes it from the app the
// person types in. A page that says no mode keeps the keyboard it was given.
func FloatKeyable(mode string) bool {
	return mode != ModePill && mode != ModePeek
}

// ShowFloat shows the floating widget where the person left it.
func (c *Controller) ShowFloat() error {
	u, err := c.url(FloatRoute)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatShown = true
	c.placeFloat()
	c.cfg.Host.ShowFloat(u)
	return nil
}

// HideFloat hides the floating widget.
func (c *Controller) HideFloat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatShown = false
	c.cfg.Host.HideFloat()
}

// OnFloatHide sets what a hide the float's page asks for does; until set,
// nothing.
func (c *Controller) OnFloatHide(fn func(mode string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatHide = fn
}

// placeFloat moves a shown float to its placement on the known screens; it
// runs under c.mu.
func (c *Controller) placeFloat() {
	if !c.floatShown || len(c.screens) == 0 {
		return
	}
	if !c.placed {
		size := c.placement
		c.placement, c.placed = defaultPlacement(c.screens[0]), true
		if size.W > 0 {
			c.placement.W, c.placement.H = size.W, size.H
		}
	}
	c.cfg.Host.SetFloatFrame(ClampToScreens(c.placement, c.screens))
}

func (c *Controller) floatMessage(m FloatMessage) {
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

// floatMoved keeps where the person dropped the float; the frame snaps to
// a docked edge on the next resize, not under the dragging mouse.
func (c *Controller) floatMoved(frame Rect) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.screens) == 0 {
		return
	}
	c.placement, c.placed = PlacementOf(frame, c.screens), true
	if c.cfg.Defaults != nil {
		SavePlacement(c.cfg.Defaults, c.placement)
	}
}

func (c *Controller) setScreens(screens []Rect) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.screens = screens
	c.placeFloat()
}
