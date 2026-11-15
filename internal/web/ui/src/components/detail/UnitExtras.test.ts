import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, expect, test } from "vitest";
import { applyPlugins } from "../../lib/catalog";
import { applyStrings } from "../../lib/i18n.svelte";
import { readRoute } from "../../lib/routes";
import { results } from "../../state/actions.svelte";
import { net } from "../../state/api";
import { loadDetailPane } from "../../state/loaders.svelte";
import { catalog, detailState, tasksState } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import UnitCard from "./UnitCard.svelte";
import UnitExtras from "./UnitExtras.svelte";
import { settle } from "../../testing/settle";

const pr = { provider: "github", key: "acme/api#8" };
const thread = { provider: "slack", key: "T1/C1/1700.1" };
let asked: string[] = [];
let posts: { url: string; body: any }[] = [];
let answers: Record<string, any> = {};

beforeEach(() => {
  resetStore(Date.parse("2026-11-07T12:00:00Z"), "#view=all");
  asked = [];
  posts = [];
  answers = {};
  net.fetch = async (url: string, init?: RequestInit) => {
    if (init && init.method === "POST") {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return { ok: true, status: 200, json: async () => ({ id: 41, state: "pending" }) } as Response;
    }
    asked.push(url);
    const ok = url in answers;
    return { ok, status: ok ? 200 : 404, json: async () => (ok ? answers[url] : { error: "not found" }) } as Response;
  };
});


const extras = (d: any) => render(UnitExtras, { d }).container;
const byFocus = (c: HTMLElement, slot: string) => c.querySelector(`[data-focus="${slot}"]`) as HTMLElement | null;

test("the unit composer posts a draft, and offers a reaction only where the plugin reacts", async () => {
  applyPlugins([{ id: "slack", name: "Slack", enabled: true, reacts: true }, { id: "github", name: "GitHub", enabled: true }, { id: "linear", name: "Linear", enabled: true, reacts: true }]);
  const c = extras({ row: { unit: thread }, items: [] });
  const box = byFocus(c, "unit:reply:text") as HTMLTextAreaElement;
  await fireEvent.input(box, { target: { value: "Thanks, merged" } });
  await fireEvent.click(byFocus(c, "unit:reply:send") as HTMLButtonElement);
  await settle();
  expect(posts).toEqual([{ url: "/api/drafts", body: { unit: thread, text: "Thanks, merged", send: true, source: "cockpit" } }]);
  expect(results.get("unit:reply:send")).toEqual({ text: "Draft 41 saved to the Outbox for your approval", err: false });
  expect(box.value).toBe("");
  expect(detailState.compose).toEqual({ key: "unit:slack:T1/C1/1700.1", text: "", reaction: "" });
  const reacts = (u: any) => !!byFocus(extras({ row: { unit: u }, items: [] }), "unit:reply:reaction");
  expect([reacts(thread), reacts({ provider: "linear", key: "PAY-88" }), reacts({ provider: "wiki", key: "Home" }), reacts(pr)]).toEqual([true, true, false, false]);
});

test("the composer says what to write first when it holds nothing", async () => {
  const c = extras({ row: { unit: pr }, items: [] });
  await fireEvent.click(byFocus(c, "unit:reply:save") as HTMLButtonElement);
  await settle();
  expect(posts).toEqual([]);
  expect(results.get("unit:reply:save")).toEqual({ text: "Write a reply or name a reaction first", err: true });
  flushSync();
  expect(c.querySelector(".d-compose .act-result.err, .act-result.err")?.textContent).toBe("Write a reply or name a reaction first");
});

test("the composer starts on the served send", () => {
  for (const send of ["now", "draft"]) {
    tasksState.catalog = { verbs: [{ verb: "respond", handler: "reply", label: "Reply", send }] };
    const c = extras({ row: { unit: thread }, items: [] });
    const primary = ["send", "save"].filter((b) => byFocus(c, "unit:reply:" + b)?.classList.contains("primary"));
    expect(primary, send).toEqual([send === "now" ? "send" : "save"]);
  }
});

test("the composer's text belongs to its unit: another unit's composer starts empty", async () => {
  const a = extras({ row: { unit: pr }, items: [] });
  await fireEvent.input(byFocus(a, "unit:reply:text") as HTMLTextAreaElement, { target: { value: "draft for 8" } });
  const b = extras({ row: { unit: thread }, items: [] });
  expect((byFocus(b, "unit:reply:text") as HTMLTextAreaElement).value).toBe("");
  expect((byFocus(a, "unit:reply:text") as HTMLTextAreaElement).value).toBe("draft for 8");
});

test("the unit detail route reads the unit's tasks in one request and lists them with its card", async () => {
  tasksState.catalog = { views: [{ id: "fits-now" }] };
  answers["/api/units/github/acme%2Fapi%238/tasks"] = [{ id: "e-1", title: "Task e-1", state: "next" }, { id: "e-3", title: "Task e-3", state: "waiting" }];
  const route = readRoute("#unit=github/acme%2Fapi%238");
  expect(route.unit).toBe("github:acme/api#8");
  await loadDetailPane(route);
  expect(asked).toEqual(["/api/units/github/acme%2Fapi%238/tasks"]);
  expect(detailState.unitTasks.map((e: any) => e.id)).toEqual(["e-1", "e-3"]);
  const c = extras({ row: { unit: pr }, items: [], card: { card: { unit: pr, title: "Retry flaky uploads", state: "open" } } });
  for (const s of ["Retry flaky uploads", "Tasks", "Task e-1", "Task e-3"]) expect(c.textContent).toContain(s);
  const link = byFocus(c, "unit-task:e-1") as HTMLAnchorElement;
  expect(link.getAttribute("href")).toContain("task=e-1");
  expect(link.getAttribute("href")).not.toContain("unit=");
});

test("the unit's items say whom they wait on and which task holds them, the resolved past three folded", async () => {
  applyStrings({ locale: "pt-BR", strings: { "Waiting on": "Esperando", open: "aberto", resolved: "resolvido", "no owner": "sem dono", you: "você", "+{n} resolved": { one: "+{n} resolvido", other: "+{n} resolvidos" } } });
  const item = (id: string, summary: string, state: string) => ({ id, unit: pr, kind: "request", summary, state });
  const resolved = ["Rename the flag", "Bump the client", "Drop the shim", "Tag the release", "Close the epic"].map((s) => item("r-" + s, s, "resolved"));
  const d = {
    row: { unit: pr },
    items: [resolved[0], item("i1", "Review the retry change", "open"), item("i2", "Pick a release date", "open"), item("i5", "Ship the docs", "snoozed"), ...resolved.slice(1)],
    item_ties: { i1: { waits_on: [{ key: "github:me", name: "Me", me: true }], task: "t-1", task_title: "Review the retries" }, i2: { waits_on: [] } },
  };
  const c = extras(d);
  const rows = () => [...c.querySelectorAll(".d-items > li")];
  expect(rows().map((li) => li.textContent)).toEqual([
    "aberto Review the retry change Esperando você · Review the retries",
    "aberto Pick a release date sem dono",
    "snoozed Ship the docs sem dono",
    "resolvido Rename the flag",
    "resolvido Bump the client",
    "resolvido Drop the shim",
  ]);
  const links = rows().map((li) => [...li.querySelectorAll("a")].map((a) => a.textContent + " " + a.getAttribute("href")));
  expect(links[0].length).toBe(1);
  expect(links[0][0]).toContain("Review the retries #");
  expect(links[0][0]).toContain("task=t-1");
  expect(links.slice(1).every((l) => l.length === 0)).toBe(true);
  const more = byFocus(c, "unit:items:more") as HTMLButtonElement;
  expect(more.textContent).toBe("+2 resolvidos");
  await fireEvent.click(more);
  expect(rows().length).toBe(8);
  expect(byFocus(c, "unit:items:more")).toBeNull();
});

test("a card's ref opens the unit in its plugin's view, and its link opens the service", () => {
  catalog.views = [
    { id: "needs-me", provider: "github", rows: true }, { id: "dash", provider: "github", rows: false },
    { id: "all", provider: "github", rows: true }, { id: "slack", provider: "slack", rows: true },
  ];
  applyPlugins([{ id: "github", name: "GitHub" }, { id: "slack", name: "Slack" }]);
  const links = (c: HTMLElement, cls: string) => [...c.querySelectorAll("a." + cls)].map((a) => [a.getAttribute("href"), a.textContent, a.getAttribute("target") || ""]);
  const gh = render(UnitCard, { s: { card: { unit: { provider: "github", key: "acme/api#8" }, label: "acme/api#8", link: "https://github.com/acme/api/pull/8" } } }).container;
  expect(links(gh, "ref")).toEqual([["#view=all&unit=github/acme%2Fapi%238", "acme/api#8", ""]]);
  expect(links(gh, "ext")).toEqual([["https://github.com/acme/api/pull/8", "GitHub", "_blank"]]);
  const sl = render(UnitCard, { s: { card: { unit: thread, label: "#eng thread", link: "slack://channel?id=C1" } } }).container;
  expect(links(sl, "ref")).toEqual([["#view=slack&unit=slack/T1%2FC1%2F1700.1", "#eng thread", ""]]);
  // The Go test stubbed safeURL as identity; the page links only https URLs, so a slack:// link has no href.
  expect(links(sl, "ext")).toEqual([[null, "Slack", "_blank"]]);
});

test("a card shows its signals in its plugin's words and whom it waits on", () => {
  catalog.cardSignals = [{ provider: "github", key: "ci", label: "CI", values: { failing: "failing {{.n}}" }, notes: { flaky: "flaky since {{.day}}" } }];
  const c = render(UnitCard, { s: { stale: true, card: { unit: pr, title: "Retry", waits_on: [{ key: "github:bo", name: "Bo" }, { key: "github:ana" }],
    signals: [{ key: "ci", value: "failing", params: { n: 2 }, tone: "bad", notes: [{ code: "flaky", tone: "warn", params: { day: "monday" } }, { code: "slow" }] }] } } }).container;
  expect(c.querySelector("section")?.className).toBe("d-card stale");
  expect(c.querySelector(".signals li")?.textContent).toBe("CI failing 2 · flaky since monday · slow");
  expect(c.querySelector(".signals li")?.className).toBe("tone-bad");
  expect(c.textContent).toContain("Waiting on Bo, github:ana");
  expect(c.querySelector(".flag")?.textContent).toBe("plugin disabled");
  const none = render(UnitCard, { s: { card: { unit: pr, signals: [{ key: "ci", value: "failing" }] } }, signals: false }).container;
  expect(none.querySelector(".signals")).toBeNull();
});
