package web

import (
	"slices"
	"testing"
)

// panelHarness loads routes.js and panel.js with a fake window and document;
// the cockpit's own hashchange listener is registered after the panel's, as
// index.html orders the scripts.
const panelHarness = `
function target() {
  const ls = {};
  return {
    ls,
    addEventListener(type, fn) { (ls[type] ||= []).push(fn); },
    dispatch(type, e) { for (const fn of ls[type] || []) { fn(e); if (e.stopped) break; } },
  };
}
function event(extra) {
  return { defaultPrevented: false, stopped: false, preventDefault() { this.defaultPrevented = true; }, stopImmediatePropagation() { this.stopped = true; }, ...extra };
}
function link(href) { return { closest: (sel) => (sel === 'a[href^="#"]' ? { getAttribute: () => href } : null) }; }
const posted = [];
let appSaw = 0;
const win = target();
win.location = { hash: "" };
win.history = { replaceState(_, __, h) { win.location.hash = h; } };
const doc = target();
const nativeClose = () => {};
win.close = nativeClose;
function start(hash) {
  win.location.hash = hash;
  const installed = panelInstall(win, doc, (body) => posted.push(body));
  win.addEventListener("hashchange", () => { appSaw++; });
  return installed;
}
`

func panelRun(t *testing.T, main string, out any) {
	t.Helper()
	nodeScript(t, staticJS(t, "routes.js")+staticJS(t, "panel.js")+panelHarness+main, nil, out)
}

type panelResult struct {
	Posted    []map[string]string
	Prevented bool
	Hash      string
	AppSaw    int
	Installed bool
	Closed    bool
}

const panelReport = `process.stdout.write(JSON.stringify({ Posted: posted, Prevented: e.defaultPrevented, Hash: win.location.hash, AppSaw: appSaw, Installed: installed, Closed: win.close !== nativeClose }));`

func TestPanelLinkWithoutPanelGoesToTheMainWindow(t *testing.T) {
	var got panelResult
	panelRun(t, `
const installed = start("#panel=omnibox");
const e = event({ target: link("#task=t-1") });
doc.dispatch("click", e);
`+panelReport, &got)
	if !got.Prevented || len(got.Posted) != 1 || got.Posted[0]["action"] != "open" || got.Posted[0]["route"] != "task=t-1" {
		t.Fatalf("a link to task=t-1 posted %v, default prevented %v", got.Posted, got.Prevented)
	}
	if got.Hash != "#panel=omnibox" {
		t.Errorf("the panel moved to %q", got.Hash)
	}
}

func TestPanelLinkWithPanelStaysInThePanel(t *testing.T) {
	var got panelResult
	panelRun(t, `
const installed = start("#panel=omnibox");
const e = event({ target: link("#panel=omnibox&q=x") });
doc.dispatch("click", e);
`+panelReport, &got)
	if got.Prevented || len(got.Posted) != 0 {
		t.Fatalf("a panel link posted %v, default prevented %v", got.Posted, got.Prevented)
	}
}

func TestPanelHashChangeIsHandedOver(t *testing.T) {
	var got panelResult
	panelRun(t, `
const installed = start("#panel=omnibox&q=ab");
win.location.hash = "#view=needs-me";
const e = event({});
win.dispatch("hashchange", e);
`+panelReport, &got)
	if len(got.Posted) != 1 || got.Posted[0]["action"] != "open" || got.Posted[0]["route"] != "view=needs-me" {
		t.Fatalf("posted %v", got.Posted)
	}
	if got.Hash != "#panel=omnibox&q=ab" {
		t.Errorf("the panel's hash is %q, want it restored", got.Hash)
	}
	if got.AppSaw != 0 {
		t.Errorf("the cockpit's listener saw the change %d times", got.AppSaw)
	}
}

func TestPanelHashChangeWithinThePanelReachesTheCockpit(t *testing.T) {
	var got panelResult
	panelRun(t, `
const installed = start("#panel=omnibox");
win.location.hash = "#panel=omnibox&q=ab";
win.dispatch("hashchange", event({}));
win.location.hash = "#task=t-2";
const e = event({});
win.dispatch("hashchange", e);
`+panelReport, &got)
	if got.AppSaw != 1 {
		t.Errorf("the cockpit saw %d panel hash changes, want 1", got.AppSaw)
	}
	if got.Hash != "#panel=omnibox&q=ab" {
		t.Errorf("handing over restored %q, want the panel's latest hash", got.Hash)
	}
}

func TestPanelCloseHidesThePanel(t *testing.T) {
	var got panelResult
	panelRun(t, `
const installed = start("#panel=omnibox");
win.close();
const e = event({});
`+panelReport, &got)
	if len(got.Posted) != 1 || got.Posted[0]["action"] != "hide-panel" {
		t.Fatalf("window.close() posted %v", got.Posted)
	}
}

func TestOutsideThePanelNothingIsIntercepted(t *testing.T) {
	var got panelResult
	panelRun(t, `
const installed = start("#view=inbox");
const e = event({ target: link("#task=t-1") });
doc.dispatch("click", e);
`+panelReport, &got)
	if got.Installed || got.Closed || got.Prevented || len(got.Posted) != 0 {
		t.Fatalf("outside the panel: installed %v, close replaced %v, prevented %v, posted %v", got.Installed, got.Closed, got.Prevented, got.Posted)
	}
}

func TestThePanelFreshensEachTimeItShowsAgain(t *testing.T) {
	var got struct{ Steps []string }
	panelRun(t, staticJS(t, "omnibox.js")+`
const steps = [];
doc.visibilityState = "visible";
panelFollow(win, doc, {
  reset: () => steps.push("reset"),
  health: async () => ({ strings: "pt-2", build: "b-2" }),
  strings: (stamp) => steps.push("strings " + stamp),
  build: (h) => steps.push("build " + h.build),
});
const tick = () => new Promise((r) => setTimeout(r, 5));
(async () => {
  win.dispatch("focus", event());
  await tick();
  steps.push("|");
  win.dispatch("blur", event());
  win.dispatch("focus", event());
  doc.dispatch("visibilitychange", event());
  await tick();
  steps.push("|");
  doc.visibilityState = "hidden";
  doc.dispatch("visibilitychange", event());
  doc.visibilityState = "visible";
  doc.dispatch("visibilitychange", event());
  await tick();
  process.stdout.write(JSON.stringify({ Steps: steps }));
})();`, &got)
	want := []string{"|", "reset", "strings pt-2", "build b-2", "|", "reset", "strings pt-2", "build b-2"}
	if !slices.Equal(got.Steps, want) {
		t.Fatalf("steps %v, want %v: the panel freshens once each time it shows again, never while it stays", got.Steps, want)
	}
}

func TestThePanelRendersItsOwnPage(t *testing.T) {
	var got struct {
		Renders int
		Shown   []string
		Lang    string
	}
	app := appJS(t)
	nodeScript(t, jsFunction(t, app, "panelPage")+jsFunction(t, app, "i18nChanged")+jsFunction(t, app, "refreshClock")+staticJS(t, "routes.js")+`
let renders = 0;
const shown = [];
function render() { renders++; }
function translateStatic() {}
function showOmnibox(opts) { shown.push(String(opts.panel) + " " + opts.text); }
globalThis.location = { hash: "#panel=omnibox" };
globalThis.document = { documentElement: {}, querySelector: (sel) => (sel === ".omni-input" ? { value: "retr" } : null) };
I18N.locale = "pt-BR";
i18nChanged();
refreshClock();
location.hash = "#view=inbox";
i18nChanged();
refreshClock();
process.stdout.write(JSON.stringify({ Renders: renders, Shown: shown, Lang: document.documentElement.lang }));`, nil, &got)
	if got.Renders != 2 || !slices.Equal(got.Shown, []string{"true retr"}) || got.Lang != "pt-BR" {
		t.Fatalf("%+v: in the panel new strings redraw the omnibox, keeping its text, and the cockpit's render never runs", got)
	}
}
