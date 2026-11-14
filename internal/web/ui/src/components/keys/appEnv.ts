import { isCoreView } from "../../lib/catalog";
import { t } from "../../lib/i18n.svelte";
import { groupedRows, pipeline } from "../../lib/lens";
import { PLAN, readRoute } from "../../lib/routes";
import { taskById } from "../../lib/tasks";
import { keyOf, unitOf, type J } from "../../lib/util";
import { readLens } from "../../lib/view";
import { act, setNotice } from "../../state/actions.svelte";
import { net } from "../../state/api";
import { askInput, overlays } from "../../state/overlays.svelte";
import { currentHash, readHash, win, writeHash } from "../../state/route.svelte";
import { rescueFocus } from "./focus";
import { catalog, detailState, plugins, state, tasksState } from "../../state/store.svelte";
import { scheduleLoad } from "../../state/sync.svelte";
import { actOnPlan, planGroups } from "../chrome/plan";
import { openFilterPalette } from "../chrome/toolbar";
import { resolveGate } from "../gates/gates";
import { omni, showHelp, showPalette, showOmnibox } from "../../state/overlays.svelte";
import { reviewKey } from "../screens/review";
import { stepTab } from "../task/task";
import { keyContextOf, sameUnit, type KeyEvent, type KeysEnv } from "./keys";
import { answerableQuestion, nextTaskNode, pageQuestion } from "./page";
import { stateLabel, taskAct } from "../../state/verbs";
import { openSlot } from "../../lib/lens";
import { taskSlot } from "../../lib/tasks";

// keyHooks are the keys the page's other parts answer: the queue's, a plugin view's, the window's detach and
// link copy. Each part sets its own when it mounts; one unset lets the key go on to its other bindings.
export const keyHooks: {
  queue?: (action: string) => boolean;
  pluginKey?: (e: KeyEvent) => boolean;
  detach?: () => unknown;
  copyLink?: () => unknown;
} = {};

const active = (): HTMLElement | null => (typeof document === "undefined" ? null : (document.activeElement as HTMLElement | null));
const detailOnly = (): boolean => readRoute(currentHash()).layout === "detail";

function setHashParam(name: string, value: string): void {
  const loc = win.location();
  const p = new URLSearchParams(loc.hash.slice(1));
  if (value) p.set(name, value);
  else p.delete(name);
  loc.hash = p.toString();
}

// focusedTask is the task whose row holds the focus, else the task the detail shows.
function selectedTask(): J {
  const el = active();
  const row = el && el.closest ? el.closest<HTMLElement>('[data-row^="task:"]') : null;
  const m = row && /^task:(.+?)(?:@.*)?$/.exec(row.dataset.row || "");
  if (m) return taskById(m[1], detailState.task, detailState.unitTasks);
  return readHash().task ? detailState.task : null;
}

function rowOf(u: J): J {
  return state.rows.find((r) => sameUnit(unitOf(r), u)) || (state.detail && sameUnit(unitOf(state.detail.row), u) ? state.detail.row : null);
}

function focusedRow(): J {
  const el = active();
  const row = el && el.closest ? el.closest<HTMLElement>('[data-row^="row:"], [data-row^="card:"]') : null;
  const m = row && /^(?:row|card):(.+?)(?:@.*)?$/.exec(row.dataset.row || "");
  return m ? state.rows.find((r) => keyOf(r) === m[1]) || null : null;
}

function selectedRow(): J {
  const pr = readHash().pr;
  return focusedRow() || (pr && state.rows.find((r) => keyOf(r) === pr)) || (state.detail && state.detail.row) || null;
}

const planItems = (): J[] => planGroups(state.plan.items).flatMap((g) => g.items);

function focusedPlan(): J {
  const el = active();
  const m = /^plan:(.+):[^:]+$/.exec((el && el.dataset && el.dataset.focus) || "");
  return m ? planItems().find((it) => it.id === m[1]) || null : null;
}

function focusSlot(slot: string): boolean {
  const el = document.querySelector<HTMLElement>(`[data-focus="${CSS.escape(slot)}"]`);
  if (el) el.focus();
  return !!el;
}

function focusPlan(it: J): void {
  const acts: J[] = it.actions || [];
  const id = acts.some((a) => a.id === "plan_apply" && a.enabled) ? "plan_apply" : (acts.find((a) => a.enabled) || {}).id;
  focusSlot(`plan:${it.id}:${id}`);
}

function move(delta: number): boolean {
  const view = readHash().view;
  if (isCoreView(view)) {
    const nodes = [...document.querySelectorAll<HTMLElement>('#tasks [data-focus^="task:"]')];
    const el = active();
    const row = el && el.closest ? el.closest<HTMLElement>('[data-row^="task:"]') : null;
    const at = row ? nodes.find((n) => n.dataset.focus === row.dataset.row) || null : null;
    const sel = selectedTask();
    const next = nextTaskNode(nodes, at, sel ? sel.id : "", delta);
    if (!next) return false;
    next.focus();
    return true;
  }
  if (view === PLAN) {
    const items = planItems();
    const at = items.indexOf(focusedPlan());
    const next = items[Math.max(0, Math.min(items.length - 1, at < 0 ? 0 : at + delta))];
    if (next) focusPlan(next);
    return !!next;
  }
  const rows = groupedRows(pipeline(state.rows, readLens()).groups);
  if (!rows.length) return false;
  const cur = selectedRow();
  const at = cur ? rows.findIndex((r) => keyOf(r) === keyOf(cur)) : -1;
  const next = rows[Math.max(0, Math.min(rows.length - 1, at < 0 ? 0 : at + delta))];
  if (!focusSlot(`row:${keyOf(next)}`)) focusSlot(`card:${keyOf(next)}`);
  return true;
}

// appEnv binds the key dispatcher to the cockpit's page.
export function appEnv(): KeysEnv {
  return {
    fetch: (url, init) => net.fetch(url, init),
    selected: () => selectedTask() || selectedRow(),
    act: (verb, args, task) => taskAct(task, verb, args),
    verbs: () => ((tasksState.catalog && tasksState.catalog.verbs) || []).map((v: J) => v.verb),
    verbSpecs: () => (tasksState.catalog && tasksState.catalog.verbs) || [],
    resolveGate: (task, gate, intent) => resolveGate(task, gate, intent),
    rowOf,
    spec: (id) => state.specs.byId[id],
    unitAction: (row, id, extra) => act(row, id, extra),
    planStep: focusedPlan,
    planAction: (it, id) => actOnPlan(it, id),
    overlay: () => overlays.stack.length > 0 || !!omni.open,
    pluginKey: (e) => !!keyHooks.pluginKey && keyHooks.pluginKey(e),
    viewContext: () => keyContextOf(readHash().view),
    queue: (action) => !!keyHooks.queue && keyHooks.queue(action),
    step: (action) => reviewKey(action),
    detailAt: (el) => !!(readHash().task || readHash().pr) && (detailOnly() || !!(el && (el as Element).closest && (el as Element).closest("#detail"))),
    detach: () => (keyHooks.detach ? keyHooks.detach() : false),
    copyLink: () => (keyHooks.copyLink ? keyHooks.copyLink() : false),
    tab: (dir) => (readHash().task && detailState.task ? stepTab(detailState.task, dir === "prev" ? -1 : 1, true) : false),
    questionAt: (el) => {
      const gate = pageQuestion(el as Element);
      if (gate || readHash().task) return gate;
      const d = state.detail;
      const q = answerableQuestion(d);
      const n = el as Element | null;
      if (!n || !n.closest || !n.closest(".d-question") || !q) return null;
      return { row: d.row, question: q };
    },
    move,
    open: () => {
      const e = selectedTask();
      if (e) {
        writeHash({ task: e.id, pr: "" });
        return true;
      }
      const r = selectedRow();
      if (!r) return false;
      state.openSlot = `row:${keyOf(r)}`;
      writeHash({ pr: keyOf(r), task: "" });
      return true;
    },
    close: () => {
      if (detailOnly()) return false;
      const { task, pr } = readHash();
      if (task) {
        writeHash({ task: "" });
        void rescueFocus(document, taskSlot(task));
        return true;
      }
      if (!pr) return false;
      writeHash({ pr: "" });
      void rescueFocus(document, openSlot(pr) || `row:${pr}`);
      return true;
    },
    navigate: (view) => setHashParam("view", view),
    views: () => catalog.views,
    providers: () => [...plugins.names.keys()],
    areas: async () => ((tasksState.catalog && tasksState.catalog.areas) || []) as string[],
    setArea: (a) => setHashParam("area", a),
    filter: () => {
      const box = document.getElementById("search") as HTMLInputElement | null;
      if (!box) return false;
      box.focus();
      box.select();
      return true;
    },
    addFilter: () => openFilterPalette(),
    openURL: (u) => window.open(u, "_blank", "noopener"),
    leave: (el) => (el as HTMLElement).blur(),
    ask: (o) => askInput(o),
    palette: (p) => showPalette(p),
    omnibox: (items) => showOmnibox({ items }),
    stateLabel: (name) => stateLabel(name),
    help: (groups) => showHelp(groups),
    notice: (text, err) => setNotice(text, err),
    hint: (text) => setNotice(text),
    changed: () => scheduleLoad(),
    extraItems: () => ["group", "sort"].map((id) => ({
      label: id === "group" ? t("Group by...") : t("Sort by..."),
      key: "",
      run: () => {
        const pick = document.querySelector<HTMLElement>(`[data-focus="lens:${id}"]`);
        if (pick && !pick.closest("[hidden]")) pick.focus();
      },
    })),
  };
}
