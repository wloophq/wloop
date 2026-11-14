import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { applyCatalog, applyPlugins, eventLabel, eventTone, loopEvent } from "../../lib/catalog";
import { I18N } from "../../lib/i18n.svelte";
import { activityPasses, eventRef, fieldOf, fillWord, lensOf } from "../../lib/lens";
import { resetStore } from "../../testing/reset.svelte";
import { nav, readHash } from "../../state/route.svelte";
import { catalog, state } from "../../state/store.svelte";
import Activity from "./Activity.svelte";
import { activityEmptyNote, activityView, dayLabel, LOOP_ONLY_KEY, setLoopOnly, storage } from "./feed.svelte";
import { emptyNote } from "./model";
import { useCatalogs } from "./testdata/fixtures";

const NOW = Date.parse("2026-10-27T18:00:00Z");

afterEach(cleanup);

const noteText = (parts: ReturnType<typeof emptyNote>) => parts.map((p) => p.text).join("");

describe("the recent activity", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=recent");
    useCatalogs();
    activityView.loopOnly = false;
    activityView.selected = "";
    state.activityLoaded = true;
  });

  const lens = () => lensOf(readHash());

  it("reads each event's words and tone from the catalog", () => {
    applyCatalog({ events: [{ provider: "", kind: "held", label: "pausado", tone: "me" }, { provider: "", kind: "turn_sent", label: "turno enviado", loop: true, tone: "loop" },
      { provider: "acme", kind: "review", label: "revisou", tone: "people" }] }, fieldOf);
    expect([eventLabel("held"), eventTone("held"), eventTone("review"), loopEvent("turn_sent"), loopEvent("held"), eventLabel("some_kind"), loopEvent("some_kind")])
      .toEqual(["pausado", "me", "people", true, false, "some kind", false]);
  });

  it("draws each event in its served tone", () => {
    applyCatalog({ events: [{ provider: "", kind: "held", label: "paused", tone: "me" }, { provider: "", kind: "turn_sent", label: "turn sent", loop: true, tone: "loop" }] }, fieldOf);
    state.activity = [
      { event: { unit: { provider: "github", key: "acme/api#8" }, kind: "held", key: "a", at: "2026-10-27T15:00:00Z" }, label: "acme/api#8", title: "Retry flaky uploads" },
      { event: { unit: { provider: "github", key: "acme/api#8" }, kind: "turn_sent", key: "b", at: "2026-10-27T15:01:00Z" }, label: "acme/api#8", title: "Retry flaky uploads" },
    ];
    const { container } = render(Activity, { lens: lens() });
    const evs = Array.from(container.querySelectorAll(".a-ev"));
    expect(evs.map((li) => li.className)).toEqual(["a-ev ev-held me", "a-ev ev-turn_sent loop"]);
    expect(evs[0].textContent).toContain("paused");
    expect(evs[1].textContent).toContain("turn sent");
    expect(container.querySelector(".a-day")!.textContent).toBe("Today");
  });

  it("draws another source's unit by its card, its link kept on the link", async () => {
    applyCatalog({ events: [{ provider: "chat", kind: "message", label: "wrote", tone: "people" }] }, fieldOf);
    applyPlugins([{ id: "chat", name: "Chatter", enabled: true }]);
    state.activity = [{ event: { unit: { provider: "chat", key: "W1/C2/1700.1" }, kind: "message", key: "m1", actor: "rio", at: "2026-10-27T15:00:00Z" },
      label: "#launches", title: "Pick the launch date", url: "https://chat.example/archives/C2/p17001", author: "" }];
    const { container } = render(Activity, { lens: lens() });
    const ev = container.querySelector(".a-ev")!;
    for (const want of ["wrote", "rio", "#launches", "Pick the launch date"]) expect(ev.textContent).toContain(want);
    const a = ev.querySelector("a")!;
    expect([a.getAttribute("href"), a.textContent]).toEqual(["https://chat.example/archives/C2/p17001", "Chatter"]);
    const before = location.hash;
    container.addEventListener("click", (e) => e.preventDefault(), true);
    await fireEvent.click(a);
    expect(location.hash).toBe(before);
    await fireEvent.click(ev);
    expect(location.hash).toContain("unit=chat");
  });

  it("names an event by its unit's label", () => {
    expect([eventRef({ label: "acme/api#7" }), eventRef({ label: "#general" }), eventRef({})]).toEqual(["acme/api#7", "#general", ""]);
    const l = lensOf({ ...readHash(), q: "where:acme/api" });
    expect(activityPasses({ label: "acme/api#7", event: { kind: "push" } }, l)).toBe(true);
    expect(activityPasses({ label: "#general", event: { kind: "push" } }, l)).toBe(false);
  });

  it("keeps the loop's own events once asked, remembered in the browser, each node kept", async () => {
    applyCatalog({ events: [{ provider: "", kind: "held", label: "paused", tone: "me" }, { provider: "", kind: "turn_sent", label: "turn sent", loop: true, tone: "loop" }] }, fieldOf);
    const saved: Record<string, string> = {};
    storage.set = (k, v) => void (saved[k] = v);
    state.activity = [
      { event: { unit: { provider: "github", key: "acme/api#8" }, kind: "held", key: "a", at: "2026-10-27T15:00:00Z" }, title: "one" },
      { event: { unit: { provider: "github", key: "acme/api#8" }, kind: "turn_sent", key: "b", at: "2026-10-27T15:01:00Z" }, title: "two" },
    ];
    const { container } = render(Activity, { lens: lens() });
    const loopEv = container.querySelectorAll(".a-ev")[1];
    await fireEvent.click(container.querySelector('[data-focus="activity:loop-only"]')!);
    expect(saved[LOOP_ONLY_KEY]).toBe("1");
    expect(container.querySelectorAll(".a-ev").length).toBe(1);
    expect(container.querySelector(".a-ev")).toBe(loopEv);
    setLoopOnly(false);
    flushSync();
    expect(container.querySelectorAll(".a-ev").length).toBe(2);
  });

  it("names today, yesterday and an older day", () => {
    expect(dayLabel("2026-10-27T15:00:00Z", NOW)).toBe("Today");
    expect(dayLabel("2026-10-26T15:00:00Z", NOW)).toBe("Yesterday");
    expect(dayLabel("2026-10-20T15:00:00Z", NOW)).not.toMatch(/Today|Yesterday/);
  });

  it("is hidden off the recent view", () => {
    nav.hash = "#view=mine";
    const { container } = render(Activity, { lens: lens(), hidden: true });
    expect((container.querySelector("#activity") as HTMLElement).hidden).toBe(true);
  });
});

describe("empty notes", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=recent");
    useCatalogs();
    applyPlugins([{ id: "github", name: "GitHub", enabled: true }]);
    state.loaded = true;
    state.activityLoaded = true;
    state.activity = [{ event: { kind: "push" } }];
    activityView.loopOnly = false;
  });

  it("name the filter", () => {
    expect(noteText(activityEmptyNote("zzz"))).toBe('Nothing in Recent matches "zzz".');
    expect(activityEmptyNote("https://github.com/acme/web/pull/3").map((p) => (p.kind === "text" ? p.text : { [p.kind === "button" ? "button" : "a"]: p.text })))
      .toEqual(["acme/web#3 is not in the inventory.", " ", { button: "Explore it" }, " ", { a: "Open it on GitHub" }]);
    activityView.loopOnly = true;
    expect(noteText(activityEmptyNote(""))).toBe("No loop events in the recent activity.");
    activityView.loopOnly = false;
    state.activity = [];
    expect(noteText(activityEmptyNote("zzz"))).toBe("Nothing happened in the inventory yet.");
    expect(noteText(emptyNote("mine", "zzz"))).toBe('Nothing in My PRs matches "zzz".');
    expect(emptyNote("mine", "waits-on:bob zzz").map((p) => p.kind)).toEqual(["text", "text", "button"]);
    expect(noteText(emptyNote("mine", ""))).toBe("Nothing in My PRs right now.");
    expect(catalog.views.some((v) => v.id === "mine")).toBe(true);
  });
});

describe("a row word", () => {
  beforeEach(() => resetStore(NOW));

  it("picks the plural form of its locale", () => {
    const en = '{{.n}} open {{plural .n "one" "question" "other" "questions"}} in the brief';
    const pt = '{{.n}} {{plural .n "one" "pergunta aberta" "other" "perguntas abertas"}}';
    const out = [fillWord(en, { n: 1 }), fillWord(en, { n: 2 })];
    I18N.locale = "pt-BR";
    out.push(fillWord(pt, { n: 0 }), fillWord(pt, { n: 3 }), fillWord("{{.unit}}: {{.shown}}", { unit: "Item", shown: 2 }));
    expect(out).toEqual(["1 open question in the brief", "2 open questions in the brief", "0 pergunta aberta", "3 perguntas abertas", "Item: 2"]);
  });
});
