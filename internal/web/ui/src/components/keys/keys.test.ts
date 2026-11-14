import { beforeEach, describe, expect, test } from "vitest";
import { resetStore } from "../../testing/reset.svelte";
import { BINDINGS, harness, taskFixture, viewsFixture, type Posted } from "../../testing/keys";
import { paletteMatch } from "../../lib/palette";
import { createKeys, helpGroups, sourceLink } from "./keys";

beforeEach(() => resetStore());

const strip = (xs: { label: string; key?: string }[]) => xs.map(({ label, key }) => ({ label, key }));

describe("keys_test.go", () => {
  test("TestMoveOpenClose: j, k move, Enter opens, Esc closes", async () => {
    const k = harness({ selected: taskFixture() });
    for (const key of ["j", "j", "k", "Enter", "Escape"]) await k.press(key);
    expect(k.log.moves).toEqual([1, 1, -1]);
    expect(k.log.detail).toBe(1);
    expect(k.log.closed).toBe(1);
  });

  test("TestWindowKeysDetachInTheDetailAndCopyALinkEverywhere", async () => {
    const k = harness({ selected: taskFixture() });
    await k.press("t");
    await k.press("t", { target: { tagName: "DIV", inDetail: true } });
    await k.press("c");
    await k.press("c", { target: { tagName: "DIV", inDetail: true } });
    expect(k.log.detached).toBe(1);
    expect(k.log.copied).toBe(2);
  });

  describe("TestVerbKeysPostVerbs", () => {
    const cases: [string, string, string | null, Record<string, string> | null][] = [
      ["x", "next", null, null],
      ["w", "waiting", "dana", { on: "dana" }],
      ["w", "waiting", "", null],
      ["s", "schedule", "2026-11-02", { until: "2026-11-02" }],
      ["s", "schedule", "activity", { on_activity: "true" }],
      ["y", "someday", null, null],
      ["l", "reference", null, null],
      ["f", "focus", null, null],
      ["p", "project", "Launch", { title: "Launch" }],
      ["a", "approve", null, null],
      ["d", "delegate", "", null],
      ["d", "delegate", "keep it short", { guidance: "keep it short" }],
      ["r", "reply", "On it", { text: "On it" }],
      ["e", "done", null, null],
      ["Backspace", "dismiss", null, null],
    ];
    for (const [key, verb, answer, args] of cases) {
      test(`${key}-${verb}`, async () => {
        const k = harness({ selected: taskFixture(), answers: answer === null ? [] : [answer] });
        await k.press(key);
        expect(k.log.posts).toHaveLength(1);
        const p = k.log.posts[0];
        expect(p.url).toBe("/api/tasks/e-1/" + verb);
        expect(p.body.source).toBe("cockpit");
        expect(p.api).not.toBe("");
        expect(p.body.args || null).toEqual(args);
        expect(k.log.asked.length === 1).toBe("swpdr".includes(key));
      });
    }

    test("a renamed verb asks by its handler", async () => {
      const e = { ...taskFixture(), verbs: [{ verb: "park", enabled: true }] };
      const k = harness({ selected: e, answers: ["dana"], verbs: ["park"], verbSpecs: [{ verb: "park", handler: "waiting", input: "Parked on whom?" }] });
      k.keys.setBindings([...BINDINGS, { key: "w", action: "park", context: "list" }]);
      await k.press("w");
      expect(k.log.posts.map((p) => [p.url, p.body.args])).toEqual([["/api/tasks/e-1/park", { on: "dana" }]]);
      expect(k.log.asked).toEqual(["Parked on whom?"]);
    });

    test("backing out sends nothing", async () => {
      const k = harness({ selected: taskFixture() });
      await k.press("s");
      await k.press("p");
      await k.press("r");
      expect(k.log.posts).toEqual([]);
    });

    test("a refused verb sends nothing", async () => {
      const e = { ...taskFixture(), verbs: [{ verb: "done", enabled: false, reason: "a draft waits" }] };
      const k = harness({ selected: e });
      await k.press("e");
      expect(k.log.posts).toEqual([]);
    });
  });

  test("TestCaptureAndOpenSource", async () => {
    const k = harness({ selected: taskFixture(), answers: ["  Buy the domain  "] });
    await k.press("n");
    await k.press("o");
    expect(k.log.posts.map((p) => [p.url, p.body.title])).toEqual([["/api/tasks", "Buy the domain"]]);
    expect(k.log.opened).toEqual(["https://github.com/acme/api/pull/8"]);
  });

  test("TestChords: a chord's second key within CHORD_MS jumps, a late one does nothing", async () => {
    const k = harness({ selected: taskFixture(), views: viewsFixture });
    await k.press("g"); k.tick(900); await k.press("i");
    await k.press("g"); await k.press("g");
    await k.press("g"); k.tick(1001); await k.press("i");
    await k.press("g"); k.tick(1001);
    await k.press("g"); await k.press("x");
    expect(k.log.nav).toEqual(["inbox", "needs-me"]);
    expect(k.log.posts).toEqual([]);
  });

  test("TestDigitsAnswerOnlyInAQuestion", async () => {
    const e = taskFixture();
    const q = { kind: "choice", recommend: "b", options: [{ id: "a" }, { id: "b" }, { id: "c" }] };
    const k = harness({ selected: e, question: { task: e, question: q }, areas: ["OSS"] });
    const card = { tagName: "BUTTON", inQuestion: true };
    await k.press("1", { code: "Digit1", target: card });
    await k.press("2", { code: "Digit2", target: card });
    await k.press("9", { code: "Digit9", target: card });
    await k.press("1", { code: "Digit1" });
    await k.press("3", { code: "Numpad3" });
    expect(k.log.area).toEqual([]);
    expect(k.log.posts.map((p) => [p.url, p.body.args.option_id])).toEqual([["/api/tasks/e-1/answer", "b"], ["/api/tasks/e-1/answer", "a"]]);
  });

  test("TestShiftDigitsReadTheCode", async () => {
    const k = harness({ areas: ["a1", "a2", "a3", "a4", "a5", "a6"] });
    await k.press("¨", { code: "Digit6", shift: true });
    await k.press("Dead", { code: "Digit6", shift: true });
    await k.press("!", { code: "Digit1", shift: true });
    await k.press("(", { code: "Digit9", shift: true });
    expect(k.log.area).toEqual(["a6", "a6", "a1"]);
  });

  test("TestTypingSwallowsKeys", async () => {
    const k = harness({ selected: taskFixture(), views: [] });
    const took: boolean[] = [];
    for (const target of [{ tagName: "INPUT", type: "search" }, { tagName: "TEXTAREA" }, { tagName: "DIV", isContentEditable: true }, { tagName: "SELECT" }]) {
      for (const key of ["x", "j", "/", "g", "i", "Backspace", "Enter"]) took.push(await k.press(key, { target }));
      took.push(await k.press("k", { meta: true, target }));
      took.push(await k.press("6", { code: "Digit6", shift: true, target }));
    }
    await k.press("Escape", { target: { tagName: "INPUT", type: "text" } });
    await k.press("x", { target: { tagName: "INPUT", type: "checkbox" } });
    expect(took).not.toContain(true);
    expect(k.log.moves.length + k.log.nav.length + k.log.filter + k.log.closed).toBe(0);
    expect(k.log.palette).toBeNull();
    expect(k.log.left).toBe(1);
    expect(k.log.posts.map((p) => p.url)).toEqual(["/api/tasks/e-1/next"]);
  });

  test("TestTypingSwallowsKeys: a real input's keys go to it, Esc leaves it", async () => {
    const input = document.createElement("input");
    const k = harness({ selected: taskFixture() });
    expect(await k.press("x", { target: input })).toBe(false);
    expect(await k.press("Escape", { target: input })).toBe(true);
    expect(k.log.left).toBe(1);
    expect(k.log.posts).toEqual([]);
  });

  test("TestPluginKeyRunsUnitAction: an uppercase served key runs its unit's action through the row's path", async () => {
    const row = {
      unit: { provider: "github", key: "acme/api#8" }, label: "acme/api#8", link: "https://github.com/acme/api/pull/8",
      actions: [{ id: "pause", enabled: true }, { id: "resume", enabled: false }, { id: "dismiss", enabled: true }],
    };
    const k = harness({ selected: taskFixture(), rows: [row] });
    await k.press("H", { shift: true });
    await k.press("X", { shift: true });
    await k.press("M", { shift: true });
    expect(k.log.unit.map((u) => [u.unit.key, u.id])).toEqual([["acme/api#8", "pause"], ["acme/api#8", "dismiss"]]);
    expect(k.log.posts).toEqual([]);
  });

  test("TestPlanViewReachableAndKeys", async () => {
    const step = {
      id: "p-1", workflow: "review", unit: { provider: "github", key: "acme/api#8" },
      actions: [{ id: "plan_apply", enabled: true }, { id: "plan_dismiss", enabled: true }, { id: "guide", enabled: true }],
    };
    const o: Parameters<typeof harness>[0] = { views: viewsFixture };
    o.onNav = (v) => { o.view = v; };
    const k = harness(o);
    await k.press("g"); await k.press("g");
    o.view = "plan";
    await k.press("X", { shift: true });
    await k.press("Enter");
    expect(k.log.plan).toEqual([]);
    expect(k.log.moves).toEqual([1]);
    o.step = step;
    await k.press("Enter");
    await k.press("X", { shift: true });
    await k.press("Enter", { target: { tagName: "BUTTON" } });
    expect(k.log.nav).toEqual(["needs-me"]);
    expect(k.log.posts).toEqual([]);
    expect(k.log.plan).toEqual([{ id: "p-1", action: "plan_apply" }, { id: "p-1", action: "plan_dismiss" }]);
  });

  test("TestCapsLockKeepsCoreKeys", async () => {
    const row = { unit: { provider: "github", key: "acme/api#8" }, actions: [{ id: "dismiss", enabled: true }, { id: "hold", enabled: true }, { id: "take_over", enabled: true }] };
    const k = harness({ selected: taskFixture(), rows: [row] });
    for (const c of ["X", "H", "A"]) await k.press(c, { code: "Key" + c });
    expect(k.log.unit).toEqual([]);
    expect(k.log.posts.map((p) => p.url)).toEqual(["/api/tasks/e-1/next", "/api/tasks/e-1/approve"]);
  });

  test("TestOverlayHoldsTheKeys", async () => {
    const row = { unit: { provider: "github", key: "acme/api#8" }, actions: [{ id: "dismiss", enabled: true }] };
    const k = harness({ selected: taskFixture(), rows: [row], overlay: true });
    const took = [await k.press("X", { shift: true }), await k.press("j"), await k.press("x")];
    expect(took).not.toContain(true);
    expect(k.log.posts.length + k.log.unit.length + k.log.moves.length).toBe(0);
  });

  test("TestChordHintClearsWhenTheChordExpires", async () => {
    const k = harness({ views: [] });
    await k.press("g");
    k.log.timers.forEach((f) => f());
    await k.press("i");
    expect(k.log.hints).toEqual(["g …", ""]);
    expect(k.log.nav).toEqual([]);
  });

  test("TestPluginViewKeysGoThroughKeys", async () => {
    const ran: string[] = [];
    const pluginKeys = new Map<string, () => void>();
    for (const c of ["H", "Q", "x", "g", "?"]) pluginKeys.set(c, () => ran.push(c));
    let view = "threads";
    const pluginKeyDown = (e: { key: string; metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; isComposing?: boolean }) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.isComposing) return false;
      const fn = pluginKeys.get(e.key);
      if (!fn) return false;
      fn();
      return true;
    };
    const row = { unit: { provider: "github", key: "acme/api#8" }, actions: [{ id: "hold", enabled: true }] };
    const h = harness({ selected: taskFixture(), rows: [row], views: [], pluginKey: (e) => view === "threads" && pluginKeyDown(e) });
    await h.press("H", { shift: true });
    await h.press("Q", { shift: true });
    await h.press("x");
    await h.press("?");
    await h.press("g"); await h.press("i");
    await h.press("Q", { shift: true, target: { tagName: "INPUT", type: "text" } });
    view = "needs-me";
    await h.press("Q", { shift: true });
    expect(ran).toEqual(["H", "Q"]);
    expect(h.log.unit).toEqual([]);
    expect(h.log.posts.map((p) => p.url)).toEqual(["/api/tasks/e-1/next"]);
    expect(h.log.nav).toEqual(["inbox"]);
  });

  test("TestTaskVerbsKeepThePickedTask", async () => {
    const a = { ...taskFixture(), id: "e-a" };
    const b = { ...taskFixture(), id: "e-b" };
    const acted: string[] = [];
    const o: Parameters<typeof harness>[0] = { answers: ["dana"], act: async (verb, args, task) => { acted.push(verb + " " + task.id + " " + JSON.stringify(args)); } };
    let picks = 0;
    Object.defineProperty(o, "selected", { get: () => (picks++ < 2 ? a : b) });
    const k = harness(o);
    await k.press("w");
    expect(acted).toEqual([`waiting e-a {"on":"dana"}`]);
    const plain = harness({ question: { task: a, question: { kind: "confirm", options: [] } } });
    await plain.press("1", { code: "Digit1", target: { inQuestion: true } });
    expect(plain.log.posts.map((p: Posted) => [p.url, p.body])).toEqual([["/api/tasks/e-a/answer", { args: { yes: "true" }, source: "cockpit" }]]);
  });

  test("TestNavigatingToAProviderOpensItsFirstView", async () => {
    const views = [...viewsFixture, { id: "issues", provider: "linear", order: 2 }, { id: "triage", provider: "linear", order: 1 }];
    const k = harness({ views, providers: ["github", "slack", "linear", "wiki"] });
    k.keys.setBindings([...BINDINGS, { key: "g l", action: "nav:linear", context: "global", owner: "linear" }, { key: "g x", action: "nav:wiki", context: "global", owner: "wiki" }]);
    for (const c of ["g", "g", "g", "l", "g", "x", "g", "o"]) await k.press(c);
    expect(k.log.nav).toEqual(["needs-me", "triage", "outbox"]);
  });

  test("TestLensViewsAndKeysComeFromTheCatalog: only the served keys jump, and the help reads their labels", async () => {
    const catalog = [{ id: "needs-me", label: "Needs me", key: "g m" }, { id: "later", label: "Later" }, { id: "stalled", label: "Stalled", key: "g s" }];
    const served = [
      ...BINDINGS.filter((b) => !b.action.startsWith("nav:") || b.owner !== "core"),
      ...catalog.filter((v) => v.key).map((v) => ({ key: v.key as string, action: "nav:" + v.id, context: "global", owner: "core", help: v.label })),
    ];
    const k = harness({ views: catalog });
    k.keys.setBindings(served);
    for (const c of ["g", "m", "g", "i", "g", "s", "g", "n"]) await k.press(c);
    await k.press("?");
    const help = (k.log.help || []).flatMap((g) => g.bindings.map((b) => b.help));
    expect(k.log.nav).toEqual(["needs-me", "stalled"]);
    expect(help).toContain("Needs me");
    expect(help).not.toContain("Inbox");
  });

  test("TestKeysFixtureHoldsTheCoreWebMap (vitest side): the fixture is the served bindings the harness reads", () => {
    expect(BINDINGS.length).toBeGreaterThan(20);
    expect(BINDINGS[0]).toMatchObject({ key: "j", action: "move:down", context: "list", owner: "core" });
  });
});

describe("keys_source_test.go", () => {
  test("TestSourceLinkOpensOnlyHTTPS", () => {
    const u = { provider: "slack", key: "C1/1.1" };
    const task = (link: string) => ({ id: "e1", units: [u], cards: [{ card: { unit: u, link } }] });
    const got = [{ link: "javascript:alert(1)" }, { link: "https://github.com/acme/api/pull/1" }, task("data:text/html,x"), task("https://acme.slack.com/archives/C1/p1")].map(sourceLink);
    expect(got).toEqual(["", "https://github.com/acme/api/pull/1", "", "https://acme.slack.com/archives/C1/p1"]);
  });
});

describe("omnibox_palette_test.go", () => {
  test("TestPaletteShowsEveryKey", async () => {
    const row = {
      unit: { provider: "github", key: "acme/api#8" }, label: "acme/api#8",
      actions: [{ id: "pause", enabled: true, label: "Pause" }, { id: "resume", enabled: false }, { id: "dismiss", enabled: true, label: "Dismiss the turn" }, { id: "act", enabled: true, label: "Act" }],
    };
    const k = harness({ selected: taskFixture(), rows: [row], areas: ["OSS", "Hiring"] });
    await k.press("k", { meta: true });
    const pal = k.log.palette!;
    await k.press("?");
    await pal.items.find((i) => i.label === "Dismiss the turn")!.run();
    await pal.items.find((i) => i.label === "Act")!.run();
    await pal.items.find((i) => i.label === "Area...")!.run();
    const areas = k.log.palette!;
    areas.items[2].run();
    const items = strip(pal.items);
    for (const [label, key] of [
      ["to Next", "x"], ["to Waiting", "w"], ["schedule or snooze", "s"], ["to Someday", "y"], ["to Reference", "l"],
      ["toggle Focus", "f"], ["make a project or attach to one", "p"], ["approve the draft or proposal", "a"],
      ["delegate to an agent", "d"], ["done", "e"], ["dismiss", "Backspace"],
      ["Pause", "H"], ["Dismiss the turn", "X"], ["Act", ""], ["open the source", "o"],
      ["capture", "n"], ["Inbox", "g i"], ["GitHub", "g g"], ["filter", "/"], ["help", "?"], ["Area...", ""],
    ]) {
      expect(items).toContainEqual({ label, key });
    }
    expect(items).not.toContainEqual({ label: "Resume", key: "H" });
    expect(items).not.toContainEqual({ label: "seen", key: "" });
    expect(k.log.unit).toHaveLength(2);
    expect(strip(areas.items)).toEqual([{ label: "No area", key: "" }, { label: "OSS", key: "Shift+1" }, { label: "Hiring", key: "Shift+2" }]);
    expect(k.log.area).toEqual(["Hiring"]);
    expect(strip(paletteMatch(pal.items, "TO next"))).toEqual([{ label: "to Next", key: "x" }]);
    const groups = k.log.help!;
    expect(groups.map((g) => g.context)).toEqual(["global", "list", "detail", "question", "queue", "plan", "review"]);
    expect(groups.reduce((n, g) => n + g.bindings.length, 0)).toBe(BINDINGS.length);
  });
});

describe("precision_test.go", () => {
  test("TestEveryNewKeyIsInTheHelp: ], [ and + are served in their contexts and listed by the help", () => {
    const groups = helpGroups(BINDINGS);
    for (const [key, action, context] of [["]", "tab:next", "detail"], ["[", "tab:prev", "detail"], ["+", "filter:add", "list"]]) {
      const b = BINDINGS.find((x) => x.key === key && x.action === action);
      expect(b, `${key} ${action}`).toBeDefined();
      expect(b!.context).toBe(context);
      expect(b!.help).toBeTruthy();
      expect(groups.find((g) => g.context === context)!.bindings).toContainEqual(b);
    }
  });

  test("TestTheTabsShowTheKeysThatStepThem (keys side): keyOf is empty before the keys load, the served key after", () => {
    const k = createKeys({} as never);
    expect(k.keyOf("tab:next")).toBe("");
    k.setBindings(BINDINGS.map((b) => (b.action === "tab:next" ? { ...b, key: "}" } : b)));
    expect(k.keyOf("tab:prev")).toBe("[");
    expect(k.keyOf("tab:next")).toBe("}");
  });
});

describe("toolbar_test.go", () => {
  test("TestFilterKeyOpensTheFieldPalette (keys side): + is filter:add alone, opens the filter palette, and f keeps toggling Focus", async () => {
    expect(BINDINGS.filter((b) => b.key === "+").map((b) => b.context + " " + b.action)).toEqual(["list filter:add"]);
    expect(BINDINGS.filter((b) => b.key === "f" && b.context === "list").map((b) => b.action)).toEqual(["focus"]);
    let opened = 0;
    const k = harness({ selected: taskFixture(), addFilter: () => { opened++; } });
    await k.press("+", { shift: true });
    await k.press("f");
    expect(opened).toBe(1);
    expect(k.log.posts.map((p) => p.url)).toEqual(["/api/tasks/e-1/focus"]);
  });
});

describe("queue_test.go", () => {
  test("TestQueueKeysRunTheDraftCardsActions / TestQueueDraftKeysPassWhenTheCardOffersNothing (keys side)", async () => {
    const queueKeys = BINDINGS.filter((b) => b.context === "queue");
    expect(queueKeys.length).toBeGreaterThan(0);
    const edit = queueKeys.find((b) => b.action === "queue:edit");
    expect(edit).toBeDefined();
    const asked: string[] = [];
    let offers = true;
    const k = harness({ selected: taskFixture(), view: "queue", queue: (a) => { asked.push(a); return offers; } });
    await k.press(edit!.key);
    expect(asked).toEqual(["queue:edit"]);
    expect(k.log.posts).toEqual([]);
    offers = false;
    await k.press(edit!.key);
    const other = BINDINGS.find((b) => b.key === edit!.key && b.context === "list");
    expect(k.log.posts.map((p) => p.url)).toEqual(other ? ["/api/tasks/e-1/" + other.action] : []);
  });
});

test("TestBracketsSwitchTabsOnlyInTheDetail: ] and [ step the review outside the detail and the tabs inside it", async () => {
  const calls: string[] = [];
  const keys = createKeys({
    selected: () => null, verbs: () => [],
    viewContext: () => "review",
    step: (a: string) => { calls.push("step:" + a); return true; },
    detailAt: (el: unknown) => !!(el as { inDetail?: boolean }).inDetail,
    tab: (dir: string) => { calls.push("tab:" + dir); return true; },
  } as never);
  keys.setBindings(BINDINGS);
  const press = (key: string, inDetail: boolean) => keys.handle({ key, target: { tagName: "DIV", inDetail }, defaultPrevented: false, preventDefault() {} } as never);
  for (const [key, inDetail] of [["]", false], ["[", false], ["]", true], ["[", true]] as const) await press(key, inDetail);
  expect(calls).toEqual(["step:next", "step:prev", "tab:next", "tab:prev"]);
});
