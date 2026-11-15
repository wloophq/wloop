import { mount } from "svelte";
import App from "./App.svelte";
import { pluginKeyDown } from "./components/chrome/plugins.svelte";
import { explore } from "./components/chrome/explore";
import { keyHooks } from "./components/keys/appEnv";
import PanelOmnibox from "./components/overlay/PanelOmnibox.svelte";
import { omniboxAct } from "./components/overlay/omnibox";
import { exploring } from "./components/rows/model";
import { queueDeps, queueKeyAction } from "./components/screens/queue";
import { panelInstall, postWindow } from "./components/window/panel";
import { copyViewLink, detachView, followWindowChrome, installWindow, tellLinkNotice } from "./components/window/window.svelte";
import { loadStrings } from "./lib/i18n.svelte";
import { readRoute } from "./lib/routes";
import { getJSON } from "./state/api";
import { startClock } from "./state/clock.svelte";
import { servedKey } from "./state/keycaps.svelte";
import { loadActivity, loadDetail, loadExploreRecent, loadHome, loadPlan, loadQueue, loadTasks } from "./state/loaders.svelte";
import { followHash, readHash } from "./state/route.svelte";
import { connect, load } from "./state/sync.svelte";
import { state } from "./state/store.svelte";

// wire hands each part what another part answers.
function wire(): void {
  keyHooks.queue = queueKeyAction;
  keyHooks.pluginKey = (e) => pluginKeyDown(e as KeyboardEvent);
  keyHooks.detach = detachView;
  keyHooks.copyLink = copyViewLink;
  queueDeps.act = omniboxAct;
  queueDeps.keyOf = servedKey;
  exploring.explore = explore;
}

// followRoute reads what a move to another route shows: the detail, the lists, and on a new view what only it
// shows. A route written in place, as the filter writes, reads nothing.
function followRoute(win: Window): void {
  let lastView = readHash().view;
  win.addEventListener("hashchange", async () => {
    const { view } = readHash();
    const changed = view !== lastView;
    lastView = view;
    await Promise.all([
      changed || !state.activityLoaded ? loadActivity() : null,
      changed ? loadExploreRecent() : null,
      loadDetail(),
      changed ? loadPlan() : null,
      loadTasks(readHash()),
      loadHome(),
      loadQueue(),
    ]);
  });
}

// start boots the page. The window chrome and the panel install their listeners first, so a detached window's or
// the panel's pin sees each route before the page follows it.
export function start(win: Window = window, doc: Document = document): void {
  const panel = panelInstall(win, doc, postWindow);
  const inset = installWindow(win, doc);
  followWindowChrome(doc.body, inset);
  followHash(win);
  win.addEventListener("hashchange", () => tellLinkNotice(win));
  startClock();
  wire();
  loadStrings(getJSON).then(() => {
    if (panel || readRoute(win.location.hash).panel === "omnibox") {
      mount(PanelOmnibox, { target: doc.body });
      return;
    }
    mount(App, { target: doc.body, props: { inset } });
    tellLinkNotice(win);
    followRoute(win);
    load();
    connect();
  });
}

start();
