package web

import (
	"reflect"
	"testing"
)

// windowRun runs main after routes.js, panel.js and window.js, with no
// document: window.js installs nothing then.
func windowRun(t *testing.T, main string, out any) {
	t.Helper()
	nodeScript(t, staticJS(t, "routes.js")+staticJS(t, "panel.js")+staticJS(t, "window.js")+panelHarness+main, nil, out)
}

func TestALinkCarriesTheRouteWithoutWhatOnlyAWindowShows(t *testing.T) {
	var got []string
	windowRun(t, `
process.stdout.write(JSON.stringify([
  deepLink("loop", "#view=inbox&task=t-1"),
  deepLink("loop", "#task=t-1&layout=detail"),
  deepLink("loop", "#view=inbox&layout=board"),
  deepLink("loop", "#panel=omnibox&q=ab"),
  deepLink("loop", "#link=malformed"),
  deepLink("loop", ""),
]));`, &got)
	want := []string{
		"loop://open/#view=inbox&task=t-1",
		"loop://open/#task=t-1",
		"loop://open/#view=inbox&layout=board",
		"loop://open/#q=ab",
		"loop://open/#",
		"loop://open/#",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("links %q, want %q", got, want)
	}
}

func TestDetachAsksTheWindowNativelyAndOpensATabInABrowser(t *testing.T) {
	var got []any
	windowRun(t, `
process.stdout.write(JSON.stringify([
  detachTarget("#view=inbox&task=t-1", true),
  detachTarget("#view=inbox&task=t-1", false),
  detachTarget("#task=t-1&layout=detail", false),
]));`, &got)
	want := []any{
		map[string]any{"post": map[string]any{"action": "detach", "route": "view=inbox&task=t-1"}},
		map[string]any{"open": "#view=inbox&task=t-1&layout=detail"},
		map[string]any{"open": "#task=t-1&layout=detail"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets %v, want %v", got, want)
	}
}

func TestOnlyTheHostMarksTheInsetTitleBar(t *testing.T) {
	var got []bool
	windowRun(t, `
process.stdout.write(JSON.stringify([insetTitleBar("?titlebar=inset"), insetTitleBar("?x=1&titlebar=inset"), insetTitleBar(""), insetTitleBar("?titlebar=other")]));`, &got)
	if want := []bool{true, true, false, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// dragHarness is a page whose elements carry the drag property as Wails'
// runtime reads it, and the bridge the native window injects.
const dragHarness = `
const sent = [];
const bridge = { webkit: { messageHandlers: { external: { postMessage: (m) => sent.push(m) } } } };
const el = (drag) => ({ drag });
const host = { ...bridge, getComputedStyle: (n) => ({ getPropertyValue: (p) => (p === "--wails-draggable" ? (n.drag || "") : "") }) };
const page = target();
`

func TestTheTitleBarDragsTheWindowAndItsControlsDoNot(t *testing.T) {
	var got struct {
		Installed bool
		Sent      []string
	}
	windowRun(t, dragHarness+`
const installed = installDrag(page, host);
page.dispatch("mousedown", event({ button: 0, detail: 1, target: el("no-drag") }));
page.dispatch("mousemove", event({ buttons: 1, target: el("no-drag") }));
page.dispatch("mousedown", event({ button: 0, detail: 1, target: el("drag") }));
page.dispatch("mousemove", event({ buttons: 1, target: el("drag") }));
page.dispatch("mousemove", event({ buttons: 1, target: el("drag") }));
page.dispatch("mousedown", event({ button: 2, detail: 1, target: el("drag") }));
page.dispatch("mousemove", event({ buttons: 2, target: el("drag") }));
page.dispatch("dblclick", event({ target: el("drag") }));
page.dispatch("dblclick", event({ target: el("") }));
process.stdout.write(JSON.stringify({ Installed: installed, Sent: sent }));`, &got)
	if !got.Installed || !reflect.DeepEqual(got.Sent, []string{"wails:drag", "wails:drag:doubleclick"}) {
		t.Fatalf("installed %v, sent %q", got.Installed, got.Sent)
	}
}

func TestWithoutTheNativeBridgeNothingDrags(t *testing.T) {
	var got bool
	windowRun(t, dragHarness+`
process.stdout.write(JSON.stringify(installDrag(page, { getComputedStyle: host.getComputedStyle })));`, &got)
	if got {
		t.Fatal("installed a drag with no native bridge")
	}
}

func TestADetachedWindowHandsAnotherItemToTheMainWindow(t *testing.T) {
	var got panelResult
	windowRun(t, `
win.location.hash = "#task=t-1&layout=detail";
const installed = detailWindowInstall(win, doc, (body) => posted.push(body));
win.addEventListener("hashchange", () => { appSaw++; });
const e = event({ target: link("#task=t-2") });
doc.dispatch("click", e);
win.location.hash = "#unit=github/acme%2Fapi%238";
win.dispatch("hashchange", event({}));
`+panelReport, &got)
	if !got.Installed || !got.Prevented {
		t.Fatalf("installed %v, the link's default prevented %v", got.Installed, got.Prevented)
	}
	if len(got.Posted) != 2 || got.Posted[0]["route"] != "task=t-2" || got.Posted[1]["route"] != "unit=github/acme%2Fapi%238" || got.Posted[0]["action"] != "open" {
		t.Fatalf("posted %v", got.Posted)
	}
	if got.Hash != "#task=t-1&layout=detail" || got.AppSaw != 0 {
		t.Errorf("the window moved to %q, the cockpit saw %d changes", got.Hash, got.AppSaw)
	}
}

func TestADetachedWindowKeepsMovesWithinItsItem(t *testing.T) {
	var got panelResult
	windowRun(t, `
win.location.hash = "#task=t-1&layout=detail";
const installed = detailWindowInstall(win, doc, (body) => posted.push(body));
win.addEventListener("hashchange", () => { appSaw++; });
const e = event({ target: link("#task=t-1&layout=detail&ctx=home") });
doc.dispatch("click", e);
win.location.hash = "#task=t-1&layout=detail&ctx=home";
win.dispatch("hashchange", event({}));
`+panelReport, &got)
	if got.Prevented || len(got.Posted) != 0 || got.AppSaw != 1 {
		t.Fatalf("prevented %v, posted %v, the cockpit saw %d changes", got.Prevented, got.Posted, got.AppSaw)
	}
}

func TestOnlyADetailLayoutRouteIsADetachedWindow(t *testing.T) {
	var got []bool
	windowRun(t, `
const out = [];
for (const h of ["#task=t-1", "#view=inbox&layout=detail", "#view=inbox&layout=board"]) {
  win.location.hash = h;
  out.push(detailWindowInstall(win, target(), () => {}));
}
process.stdout.write(JSON.stringify(out));`, &got)
	if want := []bool{false, false, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed %v, want %v", got, want)
	}
}

func TestAMalformedLinkNoticeIsTakenOnce(t *testing.T) {
	var got struct {
		First, Second bool
		Hash          string
	}
	windowRun(t, `
win.location.hash = "#link=malformed&view=inbox";
const first = takeLinkNotice(win);
const second = takeLinkNotice(win);
process.stdout.write(JSON.stringify({ First: first, Second: second, Hash: win.location.hash }));`, &got)
	if !got.First || got.Second || got.Hash != "#view=inbox" {
		t.Fatalf("first %v, second %v, hash %q", got.First, got.Second, got.Hash)
	}
}
