import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test } from "vitest";
import { I18N } from "../../lib/i18n.svelte";
import type { J } from "../../lib/util";
import { clipboard, scheduler } from "../../state/actions.svelte";
import { clock } from "../../state/clock.svelte";
import { overlays } from "../../state/overlays.svelte";
import { busy, notice, state } from "../../state/store.svelte";
import { answer, clone, fixtures, settle, useCatalog } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { actOnPlan, planCount, planGroups, planWhen } from "./plan";
import PlanItem from "./PlanItem.svelte";
import PlanPage from "./PlanPage.svelte";

beforeEach(() => {
  resetStore(Date.parse("2026-10-14T15:00:00Z"));
  scheduler.load = () => {};
});

const textOf = (el: Element | null) => (el ? el.textContent || "" : "");

describe("a plan item", () => {
  for (const c of [
    { locale: "en", does: "Resolve its open items as no longer relevant", posts: "Posts nothing", gone: "" },
    { locale: "pt-BR", does: "Resolver os itens abertos como sem efeito", posts: "Não publica nada", gone: "waits for you" },
  ]) {
    test(`says what applying it does, to which task and why (${c.locale})`, () => {
      useCatalog();
      I18N.locale = c.locale;
      I18N.strings = clone(c.locale === "en" ? fixtures.stringsEn : fixtures.stringsPtBR);
      const { container } = render(PlanItem, { it: clone(fixtures.quietClose) });
      const li = container.querySelector("li") as HTMLElement;
      const links = [...li.querySelectorAll("a")].map((a) => [a.getAttribute("href") || "", a.textContent]);
      expect(links.some(([href, text]) => href.includes("task=t-06bec1c03a26fadf") && text === "Answer Ana about the retries")).toBe(true);
      expect(links).toContainEqual(["https://chat.example/c1", "#general"]);
      expect(li.textContent).toContain("#ops");
      expect(li.textContent).not.toContain("task:t-06bec1c03a26fadf");
      expect([...li.querySelectorAll(".plan-do")].map((x) => textOf(x.querySelector(".plan-do-what")))).toEqual([c.does]);
      expect([...li.querySelectorAll(".plan-does li.plan-do-item")].map(textOf)).toEqual(["Ana asks for the retry numbers", "Bea asks when it ships"]);
      expect(textOf(li.querySelector(".plan-posts"))).toBe(c.posts);
      const why = li.querySelector(".plan-why") as HTMLElement;
      expect(why.tagName).toBe("DETAILS");
      const facts = [...why.querySelectorAll("li")].map(textOf);
      expect(facts.length).toBe(1);
      expect(why.querySelector("li")!.getAttribute("title")).toContain("task.items_open");
      if (c.gone) expect(li.textContent).not.toContain(c.gone);
    });
  }

  test("names its unit by label and title, its workflow by its summary with its id in the title", () => {
    const { container } = render(PlanItem, {
      it: {
        id: "slack-follow-up/slack/T1%2FC1%2F171.1/ee", workflow: "slack-follow-up", decision: "pending", default: "wait",
        summary: "Nudge a thread that waits on someone; posts after you approve", unit: { provider: "slack", key: "T1/C1/171.1" }, label: "#general",
        title: "Launch plan for the export", link: "https://chat.example/T1/C1/171.1", actions: [],
      },
    });
    const unit = container.querySelector(".plan-unit")!;
    expect(unit.textContent).toBe("#general");
    expect(unit.querySelector("a")!.getAttribute("href")).toBe("https://chat.example/T1/C1/171.1");
    expect(textOf(container.querySelector(".plan-title"))).toBe("Launch plan for the export");
    const wf = container.querySelector(".plan-wf")!;
    expect(wf.textContent).toBe("Nudge a thread that waits on someone; posts after you approve");
    expect(wf.getAttribute("title")).toBe("slack-follow-up");
    const bare = render(PlanItem, { it: { id: "fake/x", workflow: "fake-close", decision: "pending", unit: { provider: "task", key: "t-1" }, actions: [] } });
    expect(textOf(bare.container.querySelector(".plan-wf"))).toBe("fake-close");
  });

  test("counts down in place as the clock moves", () => {
    const it = { id: "soon", workflow: "w", decision: "pending", default: "run", deadline: "2026-10-14T15:00:42Z", unit: { provider: "task", key: "t-1" }, actions: [] };
    const { container } = render(PlanItem, { it });
    const when = container.querySelector(".plan-when") as HTMLElement;
    expect(when.textContent).toBe("runs in 42 s");
    expect(when.className).toBe("plan-when plan-left");
    clock.now += 2000;
    flushSync();
    expect(container.querySelector(".plan-when")).toBe(when);
    expect(when.textContent).toBe("runs in 40 s");
  });
});

describe("the plan", () => {
  test("lists pending first, those that run soonest first, with the time left", () => {
    const items = [
      { id: "removal", decision: "blocked" },
      { id: "manual", decision: "pending", default: "wait" },
      { id: "later", decision: "pending", default: "run", deadline: "2026-10-14T15:04:10Z" },
      { id: "ran", decision: "run" },
      { id: "soon", decision: "pending", default: "run", deadline: "2026-10-14T15:00:42Z" },
      { id: "past", decision: "pending", default: "run", deadline: "2026-10-14T14:59:00Z" },
    ];
    state.plan.items = items;
    state.plan.loaded = true;
    expect(planGroups(items).map((g) => ({ decision: g.decision, ids: g.items.map((it) => it.id) }))).toEqual([
      { decision: "pending", ids: ["past", "soon", "later", "manual"] },
      { decision: "blocked", ids: ["removal"] },
    ]);
    expect(Object.fromEntries(items.map((it) => [it.id, planWhen(it)]))).toEqual({
      removal: "blocked", manual: "waits for you", later: "runs in 4m 10s", ran: "waits for you", soon: "runs in 42 s", past: "running",
    });
    expect(planCount()).toBe(5);
  });

  test("posts its own actions to the plan and runs the others on the item's unit", async () => {
    const calls = answer((url) => (url === "/api/plan" ? { items: [], errors: [] } : { output: [] }));
    const it = { id: "review-follow-up/github/acme%2Fapi%237/aa", workflow: "review-follow-up", unit: { provider: "github", key: "acme/api#7" }, label: "acme/api#7", actions: [] };
    await actOnPlan(it, "plan_apply");
    await actOnPlan(it, "plan_dismiss");
    const guided = actOnPlan(it, "guide");
    await settle();
    const input = overlays.stack[0] as J;
    expect(input.kind).toBe("input");
    input.finish("look at the retry");
    await guided;
    await actOnPlan(it, "unblock");
    await actOnPlan({ ...it, unit: { provider: "session", key: "term_1" }, label: "session:term_1" }, "trust_run");
    const posts = calls.filter((c) => c.url === "/api/plan" && c.method === "POST").map((c) => c.body);
    expect(posts).toEqual([
      { ids: [it.id], action: "apply" },
      { ids: [it.id], action: "dismiss" },
      { ids: [it.id], action: "guide", guidance: "look at the retry" },
    ]);
    expect(calls.filter((c) => c.url.startsWith("/api/units/")).map((c) => c.url)).toEqual([
      "/api/units/github/acme%2Fapi%237/unblock",
      "/api/units/session/term_1/trust_run",
    ]);
    expect(notice.text).toContain("trust_run");
    expect(busy.size).toBe(0);
  });

  test("names a closed session and copies its resume line", async () => {
    const copied: string[] = [];
    clipboard.writeText = async (s: string) => {
      copied.push(s);
    };
    state.plan.loaded = true;
    state.plan.closed = [{ session: "term_1", at: "2026-10-14T13:00:00Z", reason: "its pull request merged", resume: "claude --resume b" }];
    const { container } = render(PlanPage);
    const item = container.querySelector(".plan-item.closed")!;
    expect(item.textContent).toContain("term_1");
    expect(item.textContent).toContain("claude --resume b");
    expect(textOf(item.querySelector("time"))).toBe("2h 0m");
    await fireEvent.click(item.querySelector('[data-focus="plan:closed:term_1"]')!);
    await settle();
    expect(copied).toEqual(["claude --resume b"]);
    expect(notice.text).toBe("copied the resume line of term_1");
    expect(textOf(container.querySelector(".empty"))).toBe("Nothing pending or blocked in the plan.");
  });

  test("keeps each item's node, and a why left open, as the plan reloads", () => {
    useCatalog();
    state.plan.loaded = true;
    state.plan.items = [clone(fixtures.quietClose), { ...clone(fixtures.quietClose), id: "other", decision: "blocked" }];
    const { container } = render(PlanPage);
    const why = container.querySelector(".plan-why") as HTMLDetailsElement;
    why.open = true;
    state.plan.items = [{ ...clone(fixtures.quietClose), reason: "changed" }, { ...clone(fixtures.quietClose), id: "other", decision: "blocked" }];
    flushSync();
    expect(container.querySelector(".plan-why")).toBe(why);
    expect(why.open).toBe(true);
    expect([...container.querySelectorAll("h2.plan-head")].map(textOf)).toEqual(["Pending (1)", "Blocked (1)"]);
  });
});

test("TestAPlanItemReadsAsItsServedLabel: a plan item reads as its served label, else its unit", async () => {
  const { itemLabel } = await import("./plan");
  expect([itemLabel({ unit: { provider: "linear", key: "PAY-88" }, label: "PAY-88 Retry budget" }), itemLabel({ unit: { provider: "acme", key: "web#7" } })])
    .toEqual(["PAY-88 Retry budget", "acme:web#7"]);
});
