import { cleanup, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { overlays } from "../../state/overlays.svelte";
import { nav } from "../../state/route.svelte";
import { detailState, notice, queueState, state, tasksState } from "../../state/store.svelte";
import { BINDINGS } from "../../testing/keys";
import { resetStore } from "../../testing/reset.svelte";
import { served, servedKey } from "../../state/keycaps.svelte";
import { omni } from "../../state/overlays.svelte";
import Overlays from "../overlay/Overlays.svelte";
import { appEnv, keyHooks } from "./appEnv";
import { cockpit, installKeys, openOmnibox } from "./install";
import Keys from "./Keys.svelte";
import { pageQuestion } from "./page";
import { settle } from "../../testing/settle";


// daemon answers GET /api/keys with the fixture's bindings, or refuses it while keys is null, and records the
// posts.
function daemon(keys: J[] | null = BINDINGS) {
  const posts: { url: string; body: J }[] = [];
  const ctl = { keys };
  net.fetch = async (url: string, init?: RequestInit) => {
    if (init && init.method === "POST") {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return new Response("{}");
    }
    if (url === "/api/keys?surface=web") return ctl.keys ? new Response(JSON.stringify({ bindings: ctl.keys })) : new Response("{}", { status: 500 });
    return new Response("{}");
  };
  return { posts, ctl };
}

const press = (key: string, init: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  document.body.dispatchEvent(e);
  return e;
};

let stop: (() => void) | null = null;

beforeEach(() => {
  resetStore();
  served.bindings = [];
  omni.open = null;
  for (const k of Object.keys(keyHooks)) delete keyHooks[k as keyof typeof keyHooks];
});

afterEach(() => {
  if (stop) stop();
  stop = null;
  cleanup();
  document.body.replaceChildren();
});

describe("installKeys", () => {
  test("TestTheToolbarRedrawsWhenTheKeysLoad / TestTheFilterHintIsTheServedKey / TestTheTabsShowTheKeysThatStepThem: a hint that reads the served keys follows their load", async () => {
    daemon(BINDINGS.map((b) => (b.action === "tab:next" ? { ...b, key: "}" } : b)));
    const hints: string[] = [];
    const cleanupRoot = $effect.root(() => {
      $effect(() => {
        hints.push(`${servedKey("filter:add")}|${servedKey("tab:prev")}|${servedKey("tab:next")}`);
      });
    });
    flushSync();
    const installed = installKeys(appEnv());
    stop = installed.stop;
    await installed.loaded;
    flushSync();
    cleanupRoot();
    expect(hints).toEqual(["||", "+|[|}"]);
  });

  test("a failed read says why, and a key five seconds later reads the keys again", async () => {
    const d = daemon(null);
    let now = 0;
    const installed = installKeys(appEnv(), document, () => now);
    stop = installed.stop;
    await installed.loaded;
    expect(notice.err).toBe(true);
    expect(notice.text).toContain("Could not load the keys");
    d.ctl.keys = BINDINGS;
    press("j");
    await settle();
    expect(served.bindings).toHaveLength(0);
    now = 5001;
    press("j");
    await settle();
    expect(served.bindings).toHaveLength(BINDINGS.length);
  });

  test("Keys.svelte listens while mounted, and Cmd+K opens the omnibox over the selection's actions", async () => {
    daemon();
    const k = render(Keys);
    render(Overlays);
    await settle();
    expect(cockpit.keys).not.toBeNull();
    press("k", { metaKey: true });
    await settle();
    expect(omni.open).not.toBeNull();
    expect(document.querySelector(".pal.omni")).not.toBeNull();
    expect(omni.open!.items.map((i) => i.label)).toContain("help");
    const held = press("?");
    await settle();
    expect(held.defaultPrevented).toBe(false);
    expect(overlays.stack).toHaveLength(0);
    k.unmount();
    expect(cockpit.keys).toBeNull();
  });

  test("the rail's openOmnibox opens the palette of the keys, the omnibox alone before they install", async () => {
    openOmnibox();
    expect(omni.open && omni.open.items).toEqual([]);
    omni.open = null;
    daemon();
    const installed = installKeys(appEnv());
    stop = installed.stop;
    await installed.loaded;
    openOmnibox();
    expect(omni.open!.items.length).toBeGreaterThan(0);
  });

  test("? opens the help with every served binding", async () => {
    daemon();
    render(Overlays);
    const installed = installKeys(appEnv());
    stop = installed.stop;
    await installed.loaded;
    press("?");
    await settle();
    const help = overlays.stack.find((o) => o.kind === "help");
    expect(help && help.kind === "help" && help.groups.reduce((n, g) => n + g.bindings.length, 0)).toBe(BINDINGS.length);
    expect(document.querySelectorAll(".pal dd")).toHaveLength(BINDINGS.length);
  });

  test("TestPluginKeyRunsUnitAction: on the page a served key runs the unit's action through act, as its button does", async () => {
    const d = daemon();
    const row = { unit: { provider: "alpha", key: "acme/api#8" }, label: "acme/api#8", actions: [{ id: "pause", enabled: true }, { id: "resume", enabled: false }] };
    state.rows = [row];
    state.specs = { version: "", byId: { pause: { id: "pause", surfaces: ["list"] } } };
    nav.hash = "#unit=alpha/acme%2Fapi%238";
    const installed = installKeys(appEnv());
    stop = installed.stop;
    await installed.loaded;
    press("H", { shiftKey: true });
    await settle();
    expect(d.posts.map((p) => [p.url, p.body])).toEqual([["/api/units/alpha/acme%2Fapi%238/pause", { source: "cockpit" }]]);
  });

  test("the queue's own keys come from its hook, and pass on when it offers nothing", async () => {
    daemon();
    const asked: string[] = [];
    keyHooks.queue = (a) => { asked.push(a); return true; };
    nav.hash = "#queue=answer";
    const installed = installKeys(appEnv());
    stop = installed.stop;
    await installed.loaded;
    const skip = BINDINGS.find((b) => b.context === "queue" && b.action === "queue:skip")!;
    press(skip.key);
    await settle();
    expect(asked).toEqual(["queue:skip"]);
  });
});

describe("queue_test.go", () => {
  test("TestDigitsAnswerAGateCardOutsideTheDetail", () => {
    const gate = (id: number) => ({ id, kind: "question", intents: [{ id: "answer" }, { id: "continue" }] });
    const card = (id: number) => {
      const el = document.createElement("div");
      el.className = "d-gate";
      el.setAttribute("data-gate", String(id));
      const inner = document.createElement("button");
      el.append(inner);
      document.body.append(el);
      return inner;
    };
    nav.hash = "#queue=answer";
    queueState.key = "answer?";
    queueState.queue = { items: [{ task: { id: "t-1" }, gate: gate(7) }] };
    tasksState.gates = [{ ...gate(8), task: "t-2" }];
    tasksState.tasks = [{ id: "t-2" }];
    detailState.task = { id: "t-3", gates: [gate(9)] };
    const at = (id: number) => {
      const q = pageQuestion(card(id));
      return q ? q.task.id + ":" + q.gate.id : "";
    };
    expect([at(7), at(8), at(9), at(10)]).toEqual(["t-1:7", "t-2:8", "t-3:9", ""]);
    const loose = document.createElement("button");
    document.body.append(loose);
    expect(pageQuestion(loose)?.gate.id).toBe(7);
    nav.hash = "#queue=answer&task=t-9";
    const detail = document.createElement("div");
    detail.id = "detail";
    const inDetail = document.createElement("button");
    detail.append(inDetail);
    document.body.append(detail);
    expect(pageQuestion(inDetail)).toBeNull();
    expect(pageQuestion(loose)).toBeNull();
  });

  test("a draft's gate on the queue is no question the digits answer", () => {
    nav.hash = "#queue=send-draft";
    queueState.key = "send-draft?";
    queueState.queue = { items: [{ task: { id: "t-1" }, gate: { id: 7, kind: "draft", delivery: "held", subject: { id: 4 } } }] };
    expect(pageQuestion(document.body)).toBeNull();
  });
});
