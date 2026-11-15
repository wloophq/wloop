import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, expect, test } from "vitest";
import { applyStrings } from "../../lib/i18n.svelte";
import { net } from "../../state/api";
import { clock } from "../../state/clock.svelte";
import { put } from "../../state/loaders.svelte";
import { drafts, detailState, state, tasksState } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { routeUnit } from "../../lib/routes";
import { nav } from "../../state/route.svelte";
import UnitDetail from "./UnitDetail.svelte";
import { wire } from "./wire.test-data";
import { settle } from "../../testing/settle";

const KEY = "github:acme/api#7";
const HASH = "#unit=github/acme%2Fapi%237";
const unit = { provider: "github", key: "acme/api#7" };

let fetched: { url: string; body: any }[] = [];

beforeEach(() => {
  resetStore(Date.parse("2026-11-07T12:00:00Z"), HASH);
  fetched = [];
  net.fetch = async (url: string, init?: RequestInit) => {
    fetched.push({ url, body: init && init.body ? JSON.parse(String(init.body)) : null });
    return { ok: true, status: 200, json: async () => ({ ok: true }) } as Response;
  };
});


function useSpecs(s: any) {
  state.specs = { version: s.version, byId: Object.fromEntries(s.specs.map((x: any) => [x.id, x])) };
}

function baseDetail(over: any = {}) {
  const row = { label: "acme/api#7", title: "Batch export", link: "https://github.com/acme/api/pull/7", unit, kind: "review", summary: { waits_on: "me", reason: "review requested" },
    payload: { author: "ana", reasons: ["review-requested"] }, actions: [{ id: "review", enabled: true }] };
  return { events: [{ kind: "push", at: "2026-11-07T10:00:00Z", actor: "ana", data: "" }], items: [], ...over, row: { ...row, ...(over.row || {}) } };
}

function show(d: any) {
  state.detail = d;
  const out = render(UnitDetail);
  flushSync();
  return out;
}

// sync is what a load brings: a new title, a new event, a new action, applied in place as the loader does.
function sync() {
  const next = JSON.parse(JSON.stringify(state.detail));
  next.row.title = "Batch export, take two";
  next.row.actions = [...(next.row.actions || []), { id: "pause", enabled: true }];
  next.events = [{ kind: "comment", at: "2026-11-07T11:00:00Z", actor: "bo", data: "looks good" }, ...(next.events || [])];
  put(state, "detail", next);
  flushSync();
}

function typeAndShape(box: HTMLTextAreaElement, text: string) {
  box.focus();
  box.value = text;
  box.dispatchEvent(new Event("input", { bubbles: true }));
  box.style.height = "240px";
  box.scrollTop = 40;
  box.setSelectionRange(3, 5);
  flushSync();
}

function expectUntouched(before: HTMLTextAreaElement, after: HTMLTextAreaElement | null, text: string) {
  expect(after).toBe(before);
  expect(before.isConnected).toBe(true);
  expect(before.value).toBe(text);
  expect(before.style.height).toBe("240px");
  expect(before.scrollTop).toBe(40);
  expect(document.activeElement).toBe(before);
  expect([before.selectionStart, before.selectionEnd]).toEqual([3, 5]);
}

test("a sync leaves the composer the same node with its text, size, scroll, focus and caret", () => {
  useSpecs(wire().specs);
  const { container } = show(baseDetail());
  const box = container.querySelector('[data-focus="unit:reply:text"]') as HTMLTextAreaElement;
  typeAndShape(box, "Thanks, merging after lunch");
  expect(detailState.compose).toEqual({ key: "unit:github:acme/api#7", text: "Thanks, merging after lunch", reaction: "" });
  sync();
  expect(container.querySelector("h1")?.textContent).toBe("Batch export, take two");
  expect(container.querySelector(".timeline")?.textContent).toContain("looks good");
  expect(container.querySelector('[data-focus="detail:pause"]')).not.toBeNull();
  expectUntouched(box, container.querySelector('[data-focus="unit:reply:text"]'), "Thanks, merging after lunch");
  state.detail = JSON.parse(JSON.stringify(state.detail));
  flushSync();
  expectUntouched(box, container.querySelector('[data-focus="unit:reply:text"]'), "Thanks, merging after lunch");
});

test("a sync leaves the guidance box the same node with its text, size, scroll, focus and caret", () => {
  const { container } = show(baseDetail());
  const box = container.querySelector("#guidance") as HTMLTextAreaElement;
  typeAndShape(box, "focus on the retries");
  expect(drafts.guidance[KEY]).toBe("focus on the retries");
  sync();
  expectUntouched(box, container.querySelector("#guidance"), "focus on the retries");
});

test("a sync leaves the question card's answer box and an opened long artifact as they were", () => {
  const d = baseDetail({
    row: { kind: "loop_waiting", actions: [{ id: "answer", enabled: true }, { id: "continue", enabled: true }],
      question: { record_id: 3, state: "asked", kind: "choice", text: "Publish?", options: [{ id: "a", label: "Publish" }] } },
    turn: { turn: { seq: 1 }, records: [{ id: 3, record: { summary: "s", artifact: { kind: "review_draft", body: "line\n".repeat(50) } } }], answers: [] },
  });
  const { container } = show(d);
  const details = container.querySelector("details.d-artifact") as HTMLDetailsElement;
  details.open = true;
  const box = container.querySelector(".d-question #guidance") as HTMLTextAreaElement;
  typeAndShape(box, "only the first finding");
  sync();
  expect(container.querySelector("details.d-artifact")).toBe(details);
  expect(details.open).toBe(true);
  expectUntouched(box, container.querySelector(".d-question #guidance"), "only the first finding");
});

test("a sent guidance clears the box", async () => {
  state.specs = { version: "v", byId: { review: { id: "review", label: "Review", group: "start", surfaces: ["detail"], input: { kind: "text" } } } };
  const { container } = show(baseDetail());
  const box = container.querySelector("#guidance") as HTMLTextAreaElement;
  typeAndShape(box, " look at the retries ");
  await fireEvent.click(container.querySelector('[data-focus="detail:review"]') as HTMLButtonElement);
  await settle();
  expect(fetched).toEqual([{ url: "/api/units/github/acme%2Fapi%237/review", body: { guidance: "look at the retries", source: "cockpit" } }]);
  expect(box.value).toBe("");
});

test("the guidance sits between the facts and the actions", () => {
  const { container } = show(baseDetail());
  expect([...container.children].map((c) => c.className || c.id)).toEqual(["d-top", "guidance", "d-acts", "d-log"]);
  expect(container.querySelector(".d-top > label.d-sub:last-child")?.getAttribute("for")).toBe("guidance");
});

test("the detail shows the unit's head, situation, facts and timeline", () => {
  state.health = { viewer: "me" };
  const { container } = show(baseDetail({ events: [
    { kind: "push", at: "2026-11-07T10:00:00Z", actor: "ana", data: "" },
    { kind: "comment", at: "2026-11-07T11:00:00Z", actor: "bo", data: "nit" },
  ] }));
  expect(container.querySelector(".d-head h1")?.textContent).toBe("Batch export");
  expect(container.querySelector(".d-head .meta")?.textContent).toBe("acme/api#7 github by ana");
  expect(container.querySelector('[data-focus="detail:close"]')?.getAttribute("aria-label")).toBe("Close detail");
  expect(container.querySelector(".d-sit .why")?.textContent).toBe("review requested");
  expect(container.querySelector(".d-facts")?.textContent).toBe("In scope asreview-requestedWorkspacenoneControlsnone");
  expect([...container.querySelectorAll(".timeline li")].map((li) => [li.className, li.querySelector(".kind")?.textContent, li.querySelector(".who")?.textContent])).toEqual([
    ["ev-comment", "comment", " bo"],
    ["ev-push", "push", " ana"],
  ]);
});

test("closing the detail drops the unit from the route", async () => {
  let hash = "";
  const { container } = show(baseDetail());
  const { win } = await import("../../state/route.svelte");
  const was = win.location;
  win.location = () => ({ set hash(h: string) { hash = h; } }) as unknown as Location;
  await fireEvent.click(container.querySelector('[data-focus="detail:close"]') as HTMLButtonElement);
  win.location = was;
  expect(hash).not.toContain("unit=");
});

test("a pending round shows its countdown, which follows the clock", () => {
  tasksState.catalog = { lens: { countdown_seconds: "{{.s}}s", countdown_minutes: "{{.m}}:{{.s}}", countdown_title: "sends at {{.time}}", countdown_sending: "sending" } };
  state.roundGates = new Map([[KEY, { deadline: "2026-11-07T12:02:05Z" }]]);
  const { container } = show(baseDetail());
  const c = container.querySelector(".d-clock .countdown") as HTMLElement;
  expect(c.dataset.deadline).toBe("2026-11-07T12:02:05Z");
  expect(c.dataset.provider).toBe("github");
  expect(c.textContent).toBe("2:05");
  clock.now = Date.parse("2026-11-07T12:01:30Z");
  flushSync();
  expect(container.querySelector(".d-clock .countdown")).toBe(c);
  expect(c.textContent).toBe("35s");
  clock.now = Date.parse("2026-11-07T12:03:00Z");
  flushSync();
  expect(c.textContent).toBe("sending");
});

test("the detail lists the failing checks with the steps they failed at", () => {
  state.checks[KEY] = [
    { name: "test", url: "https://ci.example/1", summary: "2 failed", steps: ["go test", "lint"] },
    { name: "build", url: "javascript:alert(1)" },
  ];
  const { container } = show(baseDetail({ row: { kind: "checks" } }));
  const items = [...container.querySelectorAll(".d-checks li")];
  expect(items.map((li) => li.textContent)).toEqual(["test · 2 failed · failed at go test, lint", "build"]);
  expect(items[0].querySelector("a")?.getAttribute("href")).toBe("https://ci.example/1");
  expect(items[1].querySelector("a")).toBeNull();
  const other = show(baseDetail({ row: { kind: "review" } }));
  expect(other.container.querySelector(".d-checks")).toBeNull();
});

test("the turn detail names the trimmed blocks", () => {
  const { container } = show(baseDetail({ turn: { turn: { id: "r2", seq: 1 }, answers: [] }, events: [{ kind: "guide_trimmed", key: "r2", at: "2026-11-07T10:00:00Z", data: "user: intent=review.md" }] }));
  expect(container.querySelector(".d-trimmed.muted.small")?.textContent).toBe("Guide trimmed over the judgment limit: user: intent=review.md");
});

test("the detail shows the brief, its pills filtering by their fields", () => {
  const { detail } = wire();
  const d = baseDetail({ brief: { ...detail.brief, brief: { ...detail.brief.brief, tags: ["lang:go", "area:export"] } } });
  const { container } = show(d);
  const brief = container.querySelector("section.d-brief") as HTMLElement;
  expect(brief.getAttribute("aria-label")).toBe("Brief");
  expect([...brief.querySelectorAll(".d-brief-tags .pill")].map((p) => [p.className, p.textContent])).toEqual([
    ["pill", "feature"], ["pill", "size m"], ["pill risk-low", "low risk"], ["pill tag", "lang:go"], ["pill tag", "area:export"],
  ]);
  expect(brief.querySelector(".goal")?.textContent).toBe("Export in batches");
  expect(brief.textContent).toContain("exports time out");
  expect(brief.textContent).toContain("Risk: behind a flag");
  expect([...brief.querySelectorAll(".questions li")].map((li) => li.textContent)).toEqual(["batch size?"]);
  expect(brief.querySelector(".muted.small")?.textContent).toContain("haiku · ");
  expect(brief.querySelector(".muted.small")?.textContent).toContain("refreshing");
});

test("a brief that is pending, failed or out of the specs' words says so", () => {
  const pending = show(baseDetail({ brief: { state: "pending" } }));
  expect(pending.container.querySelector(".d-brief.muted")?.textContent?.trim()).toBe("Brief pending");
  const failed = show(baseDetail({ brief: { state: "failed", error: "quota" } }));
  expect(failed.container.querySelector(".d-brief.muted")?.textContent?.trim()).toBe("Brief failed: quota. It is tried again later.");
  const stale = show(baseDetail({ brief: { state: "failed", error: "quota", brief: { kind: "fix", size: "huge", risk: "odd", goal: "g" } } }));
  const pills = [...stale.container.querySelectorAll(".d-brief-tags .pill")].map((p) => p.className);
  expect(pills).toEqual(["pill", "pill"]);
  expect(stale.container.querySelector("section.d-brief .muted.small")?.textContent).toContain("last refresh failed: quota");
});

test("the detail shows the served brief refresh without a brief", () => {
  useSpecs({ version: "v1", specs: [{ id: "brief_refresh", label: "Refresh brief", group: "brief", surfaces: ["detail"] }] });
  const off = show(baseDetail({ row: { actions: [{ id: "brief_refresh", reason: "the brief is off; set brief.enabled in the config" }] } }));
  const b = off.container.querySelector(".d-brief.muted button") as HTMLButtonElement;
  expect(b.title).toContain("the brief is off");
  expect(b.disabled).toBe(true);
  const pending = show(baseDetail({ brief: { state: "pending" }, row: { actions: [{ id: "brief_refresh", enabled: true }] } }));
  expect(pending.container.querySelector(".d-brief.muted")?.textContent).toContain("Refresh brief");
  const none = show(baseDetail({ row: { actions: [] } }));
  expect(none.container.querySelector(".d-brief")).toBeNull();
});

test("the detail's action buttons follow the served states, warnings and confirms; the card's groups stay out", async () => {
  const { specs, rows } = wire();
  useSpecs(specs);
  const groups = ["turn", "intake", "brief"];
  for (const [name, row] of Object.entries(rows)) {
    nav.hash = "#unit=" + routeUnit(row.unit.provider + ":" + row.unit.key);
    const { container, unmount } = show({ row: structuredClone(row), events: [], items: [] });
    const want = row.actions
      .filter((st: any) => state.specs.byId[st.id] && state.specs.byId[st.id].surfaces.includes("detail") && !groups.includes(state.specs.byId[st.id].group))
      .map((st: any) => {
        const sp = state.specs.byId[st.id];
        return [st.label || sp.label, !st.enabled, [sp.summary, st.enabled ? st.note : st.reason].filter(Boolean).join("\n"), !st.enabled ? "act off" : st.primary ? "act primary" : "act"];
      });
    const got = [...container.querySelectorAll(".d-acts > .d-actions > button")].map((b: any) => [b.textContent, b.disabled, b.title, b.className]);
    expect(got, name).toEqual(want);
    expect([...container.querySelectorAll(".d-acts .act-note")].map((n) => n.textContent), name).toEqual(
      row.actions.filter((st: any) => st.enabled && st.warning && want.length && state.specs.byId[st.id]?.surfaces.includes("detail") && !groups.includes(state.specs.byId[st.id].group)).map((st: any) => st.warning),
    );
    for (const label of ["Answer", "Continue", "Dismiss"]) expect(got.map((g) => g[0]), name).not.toContain(label);
    unmount();
  }
});

test("a confirm asks before the action runs", async () => {
  useSpecs({ version: "v", specs: [{ id: "take_over", label: "Take over", group: "control", surfaces: ["detail"] }] });
  const { overlays } = await import("../../state/overlays.svelte");
  const { container } = show(baseDetail({ row: { actions: [{ id: "take_over", enabled: true, confirm: "Take over acme/api#7?" }] } }));
  await fireEvent.click(container.querySelector('[data-focus="detail:take_over"]') as HTMLButtonElement);
  await settle();
  expect(JSON.stringify(overlays.stack)).toContain("Take over acme/api#7?");
  expect(fetched).toEqual([]);
});

test("a disabled action looks not allowed and says why", () => {
  const { specs } = wire();
  useSpecs(specs);
  const { container } = show(baseDetail({ row: { actions: [{ id: "address", enabled: false, primary: true, reason: "Not yours: take it over first" }, { id: "open", enabled: true, note: "Open the workspace" }] } }));
  const [address, open] = [...container.querySelectorAll(".d-acts > .d-actions > button")] as HTMLButtonElement[];
  expect([address.disabled, address.className, address.title]).toEqual([true, "act off", [state.specs.byId.address.summary, "Not yours: take it over first"].filter(Boolean).join("\n")]);
  expect([open.disabled, open.className, open.title]).toEqual([false, "act", [state.specs.byId.open.summary, "Open the workspace"].filter(Boolean).join("\n")]);
});

test("the intake and brief buttons follow the served states", async () => {
  const { specs, rows } = wire();
  useSpecs(specs);
  const withIntake = (name: string, source: string) => ({ ...rows[name], payload: { ...(rows[name].payload || {}), intake: { seq: 1, intent: "review", state: "briefing", source } } });
  const cases: Record<string, any> = {
    intake_waiting_cli: withIntake("intake_waiting_cli", "cli"),
    intake_waiting_session: withIntake("intake_waiting_session", "session"),
    brief_off: rows.brief_off,
    tracked: rows.tracked,
  };
  for (const [name, row] of Object.entries(cases)) {
    fetched = [];
    const { container, unmount } = show({ row: structuredClone(row), events: [], items: [] });
    const view = (sel: string) => [...container.querySelectorAll(sel)].map((b: any) => [b.textContent, b.disabled, b.title, b.className]);
    const wantOf = (ids: string[]) => row.actions.filter((st: any) => ids.includes(st.id)).map((st: any) => {
      const sp = state.specs.byId[st.id];
      return [st.label || sp.label, !st.enabled, [sp.summary, st.enabled ? st.note : st.reason].filter(Boolean).join("\n"), st.enabled ? "act" : "act off"];
    });
    expect(view(".d-intake .q-turn button"), name).toEqual(name.startsWith("intake_") ? wantOf(["intake_without", "intake_cancel"]) : []);
    expect(view(".d-brief button"), name).toEqual(wantOf(["brief_refresh", "copy_review_request"]));
    const enabled = [...container.querySelectorAll(".d-intake button, .d-brief button")].filter((b: any) => !b.disabled) as HTMLButtonElement[];
    for (const b of enabled) {
      await fireEvent.click(b);
      await settle();
    }
    const want = [...wantOf(["intake_without", "intake_cancel"]).length && name.startsWith("intake_") ? row.actions.filter((st: any) => ["intake_without", "intake_cancel"].includes(st.id) && st.enabled) : [],
      ...row.actions.filter((st: any) => ["brief_refresh", "copy_review_request"].includes(st.id) && st.enabled)]
      .map((st: any) => `/api/units/github/acme%2Fapi%237/${st.id} ` + JSON.stringify(st.basis ? { source: "cockpit", basis: st.basis } : { source: "cockpit" }));
    expect(fetched.map((f) => f.url + " " + JSON.stringify(f.body)), name).toEqual(want);
    unmount();
  }
  const session = show({ row: structuredClone(cases.intake_waiting_session), events: [], items: [] });
  const b = [...session.container.querySelectorAll(".d-intake .q-turn button")] as HTMLButtonElement[];
  expect(b.length).toBe(2);
  expect(b[0].disabled).toBe(true);
  expect(b[0].title).toContain("turn by hand");
  expect(b[1].disabled).toBe(false);
  expect([...session.container.querySelectorAll(".d-intake > div:not(.q-turn)")].map((x) => x.textContent)).toEqual(["Intake 1 (review) waits for the brief"]);
});

test("the plugin's detail fields show as the catalog words them", async () => {
  const { catalog } = await import("../../state/store.svelte");
  catalog.details = [{ provider: "github", field: "next_guidance", label: "Next round guidance" }, { provider: "github", field: "profile_chain", label: "Review profile", title: "the profiles" }];
  const { container } = show(baseDetail({ profile_chain: "base > acme", row: { payload: { reasons: [], override: { next_guidance: "focus on tests" } } } }));
  const dts = [...container.querySelectorAll(".d-facts dt")].slice(3);
  expect(dts.map((dt) => [dt.textContent, dt.getAttribute("title"), dt.nextElementSibling?.textContent])).toEqual([
    ["Next round guidance", null, "focus on tests"],
    ["Review profile", "the profiles", "base > acme"],
  ]);
});

test("a strings change while the detail is open rewords it", () => {
  const { container } = show(baseDetail());
  applyStrings({ locale: "pt-BR", strings: { Timeline: "Linha do tempo" } });
  flushSync();
  expect(container.querySelector(".d-log h2")?.textContent).toBe("Linha do tempo");
});

test("nothing shows for a route that opens another unit or a task", () => {
  state.detail = baseDetail();
  const { container } = render(UnitDetail);
  flushSync();
  expect(container.querySelector(".d-top")).not.toBeNull();
  resetStore(Date.parse("2026-11-07T12:00:00Z"), "#unit=github/acme%2Fapi%238");
  state.detail = baseDetail();
  flushSync();
  expect(container.querySelector(".d-top")).toBeNull();
});
