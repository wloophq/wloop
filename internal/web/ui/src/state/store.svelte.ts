import { SvelteMap, SvelteSet } from "svelte/reactivity";
import type { J } from "../lib/util";

// The cockpit's one store: what the daemon last served, applied in place by reconcile so a sync touches only the
// values that changed, and what the page holds that a reload would lose. Components read it and never keep a
// copy of server data of their own.

export interface Field {
  id: string;
  label: string;
  provider?: string;
  fact?: string;
  group?: boolean;
  sort?: boolean;
  menu?: boolean;
  rank?: string[];
  empty?: string;
  match?: (v: string, q: string) => boolean;
  buckets?: J[] | null;
  show?: (k: string) => string;
  groupBy?: (raw: string) => string;
  keys?: (it: J) => string[];
  key?: (it: J) => string;
  sortValue?: (it: J) => number;
  values?: (it: J) => string[];
  tagKey?: string;
}

// catalog is the daemon's served presentation (GET /api/catalog): its views for the web, the lens's fields
// built from its facets, and the situations' labels in rank order.
export const catalog = $state({
  version: "",
  views: [] as J[],
  specs: [] as J[],
  fields: [] as Field[],
  lens: [] as J[],
  reasons: [] as J[],
  cardSignals: [] as J[],
  kinds: new Map<string, string>(),
  kindOrder: [] as string[],
  events: new Map<string, string>(),
  eventData: new Map<string, string>(),
  eventTones: new Map<string, string>(),
  details: [] as J[],
  search: null as J,
  searchProvider: "",
});

// state is the unit rows and everything the page shows beside them.
export const state = $state({
  rows: [] as J[],
  epoch: "",
  rev: 0,
  loaded: false,
  health: null as J,
  specs: { version: "", byId: {} as Record<string, J> },
  planSpecs: {} as Record<string, J>,
  plan: { items: [] as J[], closed: [] as J[], error: "", loaded: false },
  roundGates: new Map<string, J>(),
  activity: [] as J[],
  activityLoaded: false,
  activityError: "",
  detail: null as J,
  detailError: "",
  checks: {} as Record<string, J[]>,
  conn: "connecting",
  authLost: false,
  error: "",
  actionError: "",
  actionDetail: "",
  actionSource: "",
  pausing: false,
  openSlot: "",
  explore: { result: null as J, busy: false, error: "", recent: [] as string[], sel: "", status: {} as Record<string, J> },
});

// busy are the subjects (unit keys, plan item ids) whose action is on its way; answered the questions whose
// answer was accepted, by unit key, with the question they answered.
export const busy = new SvelteSet<string>();
export const answered = new SvelteMap<string, string>();
// collapsed are the groups of the list folded, by "<field>:<key>", for the grouping collapsedFor.
export const collapsed = new SvelteSet<string>();
export const lists = $state({ collapsedFor: "" });

// drafts is the text the page holds that a reload would lose and no other state keeps: guidance typed for a
// unit, by unit key, and the home's capture field.
export const drafts = $state({ guidance: {} as Record<string, string>, capture: "" });

// notice is the status line's text.
export const notice = $state({ text: "", err: false, title: "" });

// keptLists are the lists of the task views left last, by query key, each as tasksState held it, to show again at
// once on coming back while a page since its position catches it up. Each owns its arrays: a page applies in place.
export const keptLists = new Map<string, { tasks: J[]; gates: J[]; loading: J[]; page: { epoch: string; rev: number } }>();

// tasksState is the task views' catalog and the list of the view shown.
export const tasksState = $state({
  catalog: null as J,
  catalogError: "",
  key: "",
  page: null as { epoch: string; rev: number } | null,
  units: [] as string[],
  tasks: [] as J[],
  gates: [] as J[],
  loading: [] as J[],
  counters: null as J,
  filtered: null as J,
  filterSeq: 0,
  error: "",
  rev: 0,
});

// detailState is the task the detail shows, the open tasks of the unit it shows and the drafts that wait on that
// unit, and the text being written in it: the composer, the drafts being edited, by task id.
export const detailState = $state({
  id: "",
  task: null as J,
  error: "",
  seenFor: "",
  unitKey: "",
  unitTasks: [] as J[],
  unitDrafts: [] as J[],
  compose: { key: "", text: "", reaction: "" },
  answer: "",
  guidance: "",
});
export const editing = new SvelteMap<string, string>();
export const editPayloads = new SvelteMap<string, J>();
export const editRevs = new SvelteMap<string, number>();

// taskState is the task page's timeline, runs and drafts.
export const taskState = $state({
  id: "",
  timeline: null as J,
  timelineError: "",
  kinds: [] as string[],
  kindsSeen: [] as string[],
  text: "",
  broadcast: null as J,
  sending: false,
  transcript: null as J,
  drafts: [] as J[],
  tab: "",
});

// gateState are the answers typed into the gates' cards, and the options picked, by gate id.
export const gateState = { answers: new SvelteMap<string, string>(), picked: new SvelteMap<string, string>() };

export const homeState = $state({ home: null as J, error: "" });

// queueState is where the queue screen stands: the queue of key it shows, the item at, why its last action
// failed and why its last load failed.
export const queueState = $state({ key: "", queue: null as J, at: 0, error: "", loadError: "", busy: false });

// outboxState is the last Outbox page read, why the last read failed, and the workflow whose "Stop drafting
// these" waits for its confirm.
export const outboxState = $state({ page: null as J, error: "", confirming: "" });

// reviewState is the review on screen: the daemon's Review, the step shown and its tasks.
export const reviewState = $state({ review: null as J, step: "", error: "" });

export const removeState = $state({ busy: false });

// plugins are the enabled plugins' vocabulary: the web views each draws, each provider's service name and which
// providers post reactions.
export const plugins = $state({
  views: new Map<string, { plugin: string; module: string }>(),
  names: new Map<string, string>(),
  reacting: new Set<string>(),
});
