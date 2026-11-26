package window

import (
	"encoding/json"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

// The cockpit routes the floating widget's two windows load: the tab, which
// carries the counts, and the panel beside it, which shows the queue or a
// peek.
const (
	FloatTabRoute   = "/float/tab"
	FloatPanelRoute = "/float/panel"
)

// The windows of the floating widget, as a FloatMessage names its sender.
const (
	FloatTab   = "tab"
	FloatPanel = "panel"
)

// Rect is a frame in points, its origin at the top left of the main screen
// and Y growing down.
type Rect struct{ X, Y, W, H float64 }

func (r Rect) right() float64  { return r.X + r.W }
func (r Rect) bottom() float64 { return r.Y + r.H }

// Screen is a screen's frame and its visible frame, without the menu bar and
// the Dock.
type Screen struct{ Frame, Visible Rect }

// FloatPlace is what the tab's page needs of its placement: the edge it is
// docked on, which stands it upright, "" when free.
type FloatPlace struct{ Edge string }

// PanelPlace is what the panel's page is told to show: its Mode, "" to
// close, the Item to open on or peek at by its key ("" keeps the current
// one), the Side of the tab it sits on, and the room it may fill there.
type PanelPlace struct {
	Mode, Item, Side string
	MaxW, MaxH       float64
}

// FloatBridge is the global each float page exposes for the host to call.
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

// The edges the tab docks on, and the corners it is anchored at.
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

// PlacementOf is the placement of a frame the person dropped: docked on the
// edge it was dropped near, anchored at the corner of its screen's quarter,
// so the tab grows toward the screen's middle as its counts widen it.
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

// The sides of the tab the panel goes on.
const (
	SideLeft  = "left"
	SideRight = "right"
	SideAbove = "above"
	SideBelow = "below"
)

// PanelRoom is the side of the tab the panel goes on and the part of the
// tab's screen's visible frame it may fill there: away from a docked edge,
// or below or above a free tab, whichever has more room. It depends on the
// tab alone, so the panel's page can be bounded by it before it draws.
func PanelRoom(tab Rect, edge string, screens []Screen) (side string, room Rect) {
	s := tab
	if len(screens) > 0 {
		s = screenOf(tab, screens)
	}
	room = s
	switch {
	case edge == EdgeLeft:
		side, room.X = SideRight, tab.right()
		room.W = s.right() - room.X
	case edge == EdgeRight:
		side, room.W = SideLeft, tab.X-s.X
	case s.bottom()-tab.bottom() >= tab.Y-s.Y:
		side, room.Y = SideBelow, tab.bottom()
		room.H = s.bottom() - room.Y
	default:
		side, room.H = SideAbove, tab.Y-s.Y
	}
	room.W, room.H = max(room.W, 0), max(room.H, 0)
	return side, room
}

// PanelFrame is where a panel w by h goes beside tab: in PanelRoom, against
// the tab, lined up with the tab's side nearer the screen's middle, and no
// larger than the room.
func PanelFrame(tab Rect, edge string, w, h float64, screens []Screen) Rect {
	side, room := PanelRoom(tab, edge, screens)
	s := room
	if len(screens) > 0 {
		s = screenOf(tab, screens)
	}
	r := Rect{W: min(w, room.W), H: min(h, room.H)}
	switch side {
	case SideLeft, SideRight:
		r.X = room.X
		if side == SideLeft {
			r.X = room.right() - r.W
		}
		r.Y = tab.Y
		if tab.Y+tab.H/2 >= s.Y+s.H/2 {
			r.Y = tab.bottom() - r.H
		}
	default:
		r.Y = room.Y
		if side == SideAbove {
			r.Y = room.bottom() - r.H
		}
		r.X = tab.X
		if tab.X+tab.W/2 >= s.X+s.W/2 {
			r.X = tab.right() - r.W
		}
	}
	r.X = max(min(r.X, room.right()-r.W), room.X)
	r.Y = max(min(r.Y, room.bottom()-r.H), room.Y)
	return r
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

// The messages the float's pages send the host, and the modes the panel
// draws in. The tab sends its size (float:size), asks for the panel
// (float:panel, toggling it on a click) and for a peek at an item that
// arrived (float:peek); the panel sends its size and mode (float:size) and
// that it closed (float:close), as it does once as it loads. Either opens a
// route in the main window (float:open) or hides the widget (float:hide).
const (
	FloatSize      = "float:size"
	FloatOpen      = "float:open"
	FloatHide      = "float:hide"
	FloatOpenPanel = "float:panel"
	FloatPeek      = "float:peek"
	FloatClose     = "float:close"
	ModePanel      = "panel"
	ModePeek       = "peek"
)

// FloatMessage is a message of one of the float's pages, the Window the
// host read it from.
type FloatMessage struct {
	Window string
	Kind   string
	W, H   float64
	Mode   string
	Route  string
	Item   string
	Toggle bool
}

// ParseFloatMessage reads raw, a JSON object naming its message in "type";
// false for any other message.
func ParseFloatMessage(raw string) (FloatMessage, bool) {
	var body struct {
		Type   string
		W, H   float64
		Mode   string
		Route  string
		Item   string
		Toggle bool
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return FloatMessage{}, false
	}
	m := FloatMessage{Kind: body.Type, W: body.W, H: body.H, Mode: body.Mode, Route: body.Route, Item: body.Item, Toggle: body.Toggle}
	ok := false
	switch m.Kind {
	case FloatSize:
		ok = m.W > 0 && m.H > 0
	case FloatPeek:
		ok = m.Item != ""
	case FloatOpen, FloatHide, FloatOpenPanel, FloatClose:
		ok = true
	}
	if !ok {
		return FloatMessage{}, false
	}
	return m, true
}

// FloatKeyable says whether the panel may take the keyboard in mode: only
// the full panel does, so a peek never takes it from the app the person
// types in. The tab never does.
func FloatKeyable(mode string) bool { return mode == ModePanel }

// ShowFloat shows the floating widget's tab where the person left it,
// loading its page unless the page loaded at the same URL has reported in.
// When the daemon cannot serve the page yet the float stays wanted, and Run
// shows it once the daemon's events answer.
func (c *Controller) ShowFloat() error {
	c.mu.Lock()
	c.floatWanted = true
	c.mu.Unlock()
	u, err := c.url(FloatTabRoute)
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
// refused or could not serve loads anew. A panel page that never reported in
// loads anew on its next open.
func (c *Controller) refloat(back bool) {
	c.mu.Lock()
	again := c.floatWanted && (back || !c.pageReady)
	if !c.panel.ready {
		c.panel.url, c.panel.pending = "", nil
	}
	c.mu.Unlock()
	if !again {
		return
	}
	if err := c.ShowFloat(); err != nil {
		c.cfg.Log("float: %v", err)
	}
}

// HideFloat hides the floating widget, its panel included.
func (c *Controller) HideFloat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closePanel()
	c.floatWanted, c.floatShown = false, false
	c.cfg.Host.HideFloat()
}

// FloatReady says whether the float shows a tab whose page has reported in
// since it last loaded, so it carries what waits on the user.
func (c *Controller) FloatReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.floatShown && c.pageReady
}

// OnFloatReady sets what runs when the tab's page first reports in after a
// load; until set, nothing.
func (c *Controller) OnFloatReady(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floatReady = fn
}

// OnFloatHide sets what a hide the float's pages ask for does; until set,
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

// placeFloat moves a shown tab to its placement on the known screens, unless
// the person is dragging it, then tells a loaded page where it sits when its
// placement was set anew; it runs under c.mu.
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
	c.tabFrame = ClampToScreens(c.placement, c.screens)
	c.cfg.Host.SetFloatFrame(c.tabFrame)
	if c.tellPlace && c.pageReady {
		c.cfg.Host.PlaceFloat(FloatPlace{Edge: c.placement.Edge})
		c.tellPlace = false
	}
}

func (c *Controller) floatMessage(m FloatMessage) {
	if m.Window == FloatPanel {
		c.panelMessage(m)
		return
	}
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
		if m.W == c.placement.W && m.H == c.placement.H && !c.tellPlace {
			return
		}
		c.placement.W, c.placement.H = m.W, m.H
		c.placeFloat()
		c.placePanel()
	case FloatOpenPanel:
		c.mu.Lock()
		defer c.mu.Unlock()
		if m.Toggle && c.panel.want == ModePanel {
			c.closePanel()
			return
		}
		c.openPanel(PanelPlace{Mode: ModePanel})
	case FloatPeek:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.panel.want != ModePanel {
			c.openPanel(PanelPlace{Mode: ModePeek, Item: m.Item})
		}
	default:
		c.shared(m)
	}
}

// shared handles what either page may ask: a route in the main window, or a
// hide.
func (c *Controller) shared(m FloatMessage) {
	switch m.Kind {
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

// floatPanel is the panel window as the controller knows it: the URL its
// page was loaded at, whether that page has reported in since, the mode it
// shows or was asked to show ("" while closed), the request its page has not
// been told yet, the size it last drew at for that mode (0 until it drew)
// and the room it was last told.
type floatPanel struct {
	url     string
	ready   bool
	want    string
	pending *PanelPlace
	w, h    float64
	side    string
	room    Rect
}

// openPanel asks the panel window to show p beside the shown tab, loading
// its page on the first open, or anew at a new address; the page is told
// once it has reported in, and the window shows at the size it then sends.
// It runs under c.mu.
func (c *Controller) openPanel(p PanelPlace) {
	if !c.floatShown || len(c.screens) == 0 || c.dragging {
		return
	}
	u, err := c.url(FloatPanelRoute)
	if err != nil {
		return
	}
	if u != c.panel.url || !c.panel.ready && c.panel.pending == nil {
		c.panel.url, c.panel.ready = u, false
		c.cfg.Host.LoadFloatPanel(u)
	}
	p.Side, c.panel.room = PanelRoom(c.tabFrame, c.placement.Edge, c.screens)
	p.MaxW, p.MaxH, c.panel.side = c.panel.room.W, c.panel.room.H, p.Side
	if p.Mode != c.panel.want {
		c.panel.w, c.panel.h = 0, 0
	}
	c.panel.want, c.panel.pending = p.Mode, &p
	c.tellPanel()
}

func (c *Controller) tellPanel() {
	if c.panel.ready && c.panel.pending != nil {
		c.cfg.Host.TellFloatPanel(*c.panel.pending)
		c.panel.pending = nil
	}
}

// closePanel hides a panel that was asked to show and tells its page; it
// runs under c.mu.
func (c *Controller) closePanel() {
	if c.panel.want == "" {
		return
	}
	c.panel.want, c.panel.pending, c.panel.w, c.panel.h = "", nil, 0, 0
	c.cfg.Host.HideFloatPanel()
	if c.panel.ready {
		c.cfg.Host.TellFloatPanel(PanelPlace{})
	}
}

// placePanel moves a shown panel beside the tab's frame at the size its page
// last sent, telling the page a room that changed; it runs under c.mu.
func (c *Controller) placePanel() {
	if c.panel.want == "" || c.panel.w == 0 {
		return
	}
	side, room := PanelRoom(c.tabFrame, c.placement.Edge, c.screens)
	if side != c.panel.side || room != c.panel.room {
		c.panel.side, c.panel.room = side, room
		c.panel.pending = &PanelPlace{Mode: c.panel.want, Side: side, MaxW: room.W, MaxH: room.H}
		c.tellPanel()
	}
	c.cfg.Host.ShowFloatPanel(PanelFrame(c.tabFrame, c.placement.Edge, c.panel.w, c.panel.h, c.screens))
}

// panelMessage follows the panel's page: its first message says it loaded,
// a size shows the window it asked for beside the tab, and a close hides it.
func (c *Controller) panelMessage(m FloatMessage) {
	c.mu.Lock()
	loaded := !c.panel.ready
	c.panel.ready = true
	switch m.Kind {
	case FloatSize:
		defer c.mu.Unlock()
		if c.panel.want == "" || !c.floatShown || m.Mode != ModePeek && m.Mode != ModePanel {
			return
		}
		c.panel.want, c.panel.w, c.panel.h = m.Mode, m.W, m.H
		c.placePanel()
		return
	case FloatClose:
		defer c.mu.Unlock()
		if loaded {
			c.tellPanel()
			return
		}
		c.closePanel()
		return
	}
	c.mu.Unlock()
	c.shared(m)
}

func (c *Controller) floatMoving() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dragging = true
	c.closePanel()
}

// floatDropped keeps where the person dropped the tab and snaps it there,
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
	c.placePanel()
}

func (c *Controller) setScreens(screens []Screen) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.screens, c.tellPlace = screens, true
	c.placeFloat()
	c.placePanel()
}
