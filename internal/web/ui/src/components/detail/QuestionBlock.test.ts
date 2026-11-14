import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, expect, test } from "vitest";
import { net } from "../../state/api";
import { answered, drafts, state } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import TurnButton from "./TurnButton.svelte";
import UnitDetail from "./UnitDetail.svelte";
import { wire } from "./wire.test-data";
import { settle } from "../../testing/settle";

const KEY = "github:acme/api#7";
const HASH = "#unit=github/acme%2Fapi%237";
const unit = { provider: "github", key: "acme/api#7" };
const turnActions = () => [{ id: "answer", enabled: true }, { id: "continue", enabled: true }, { id: "dismiss", enabled: true }];

let posts: { url: string; body: any }[] = [];
let refuse = false;

beforeEach(() => {
  resetStore(Date.parse("2026-11-07T12:00:00Z"), HASH);
  posts = [];
  refuse = false;
  state.specs = { version: "v", byId: { answer: { id: "answer", group: "turn", surfaces: ["detail"], input: { kind: "answer" } } } };
  net.fetch = async (url: string, init?: RequestInit) => {
    posts.push({ url, body: JSON.parse(String(init?.body)) });
    const body = refuse ? { error: "the answer was not delivered" } : { ok: true };
    return { ok: !refuse, status: refuse ? 409 : 200, json: async () => body } as Response;
  };
});


function show(d: any) {
  state.detail = d;
  const out = render(UnitDetail);
  flushSync();
  return out;
}

const card = (c: HTMLElement) => c.querySelector(".d-question") as HTMLElement;
const buttons = (c: HTMLElement) => [...card(c).querySelectorAll("button")] as HTMLButtonElement[];
const button = (c: HTMLElement, label: string) => buttons(c).find((b) => (b.textContent || "").startsWith(label)) as HTMLButtonElement;

function askedDetail() {
  return {
    row: {
      label: "acme/api#7", unit, kind: "loop_waiting", actions: turnActions(),
      question: {
        record_id: 3, state: "asked", kind: "choice", text: "Publish the review?\n  It has two findings.",
        options: [
          { id: "a", label: "Merge as is", consequence: "Approves without the findings" },
          { id: "b", label: "Rebase first", consequence: "Asks the author to rebase" },
          { id: "c", label: "Ask the author", consequence: "Comments and waits" },
        ],
        recommend: "b", verdict: "APPROVE", blocking: 0, non_blocking: 2,
      },
    },
    turn: { turn: { seq: 1 }, records: [
      { id: 2, record: { summary: "an older record" } },
      { id: 3, record: { summary: "Reviewed the batch export", artifact: { kind: "review_draft", body: "a finding\n".repeat(45) } } },
    ], answers: [] },
    events: [],
  };
}

test("the question block shows the asked question, its options, its draft folded and the free answer", async () => {
  drafts.guidance[KEY] = " check the CI first ";
  const { container } = show(askedDetail());
  const text = card(container).textContent || "";
  for (const want of ["Agent asks", "Reviewed the batch export", "drafted APPROVE, 0 blocking, 2 non-blocking", "Or answer in your own words"]) expect(text).toContain(want);
  expect(card(container).querySelector("pre.q-text")?.textContent).toBe("Publish the review?\n  It has two findings.");
  expect(text).not.toContain("an older record");
  expect(buttons(container).map((b) => [b.textContent, b.title, b.className])).toEqual([
    ["Rebase first recommended", "Asks the author to rebase", "act primary"],
    ["Merge as is", "Approves without the findings", "act"],
    ["Ask the author", "Comments and waits", "act"],
    ["Send answer", expect.any(String), "act"],
    ["Continue", "The agent goes on with the turn without an answer", "act"],
    ["Dismiss", "Close the question; the pull request goes back to whoever it waits on", "act"],
  ]);
  const details = [...card(container).querySelectorAll("details")] as HTMLDetailsElement[];
  expect(details.length).toBe(1);
  expect(details[0].open).toBe(false);
  expect(details[0].textContent).toContain("45 lines");
  await fireEvent.click(button(container, "Rebase first"));
  await settle();
  expect(posts).toEqual([{ url: "/api/units/github/acme%2Fapi%237/answer", body: { guidance: "check the CI first", option_id: "b", source: "cockpit" } }]);
});

test("a stopped turn shows its last message and the free answer alone", () => {
  const { container } = show({
    row: { unit, kind: "loop_stopped", actions: turnActions(), question: { state: "unrecorded", kind: "", text: "", last_message: "I ran out of context before recording." } },
    turn: { turn: { seq: 1 }, records: [], answers: [] }, events: [],
  });
  const text = card(container).textContent || "";
  expect(text).toContain("Agent stopped");
  expect(text).toContain("I ran out of context before recording.");
  expect(text).toContain("Answer in your own words");
  expect(buttons(container).map((b) => b.textContent)).toEqual(["Send answer", "Continue", "Dismiss"]);
});

test("the card sends the choice with the note, or the text alone", async () => {
  const { container } = show({
    row: { label: "acme/api#7", unit, kind: "loop_waiting", actions: turnActions(), question: { state: "asked", kind: "confirm", text: "Publish?", recommend: "yes" } },
    turn: { turn: { seq: 1 }, records: [], answers: [] }, events: [],
  });
  const click = async (label: string, note: string) => {
    drafts.guidance[KEY] = note;
    flushSync();
    const b = button(container, label);
    const off = b.disabled;
    if (!off) {
      await fireEvent.click(b);
      await settle();
    }
    return off;
  };
  expect(await click("Send answer", " ")).toBe(true);
  await click("Yes", "");
  answered.clear();
  await click("No", "only the first finding");
  answered.clear();
  expect(await click("Send answer", "look at the flaky test instead")).toBe(false);
  expect(posts.map((p) => p.body)).toEqual([
    { yes: true, source: "cockpit" },
    { no: true, guidance: "only the first finding", source: "cockpit" },
    { guidance: "look at the flaky test instead", source: "cockpit" },
  ]);
});

test("the card keeps its controls off once an answer is sent, until the row shows another question", async () => {
  const { container } = show({
    row: { label: "acme/api#7", unit, kind: "loop_waiting", actions: turnActions(),
      question: { state: "asked", kind: "choice", text: "Publish?", record_id: 3, options: [{ id: "publish", label: "Publish" }, { id: "hold", label: "Hold" }] } },
    turn: { turn: { seq: 1 }, records: [], answers: [] }, events: [],
  });
  const off = () => buttons(container).filter((b) => !(b.textContent || "").startsWith("Send answer")).map((b) => b.disabled);
  refuse = true;
  await fireEvent.click(buttons(container)[0]);
  await settle();
  expect(off()).toEqual([false, false, false, false]);
  expect(card(container).querySelector(".act-result.err")?.textContent).toContain("the answer was not delivered");
  refuse = false;
  await fireEvent.click(buttons(container)[0]);
  await settle();
  expect(off()).toEqual([true, true, true, true]);
  state.detail = { ...state.detail, row: { ...state.detail.row, question: { ...state.detail.row.question } } };
  flushSync();
  expect(off()).toEqual([true, true, true, true]);
  state.detail = { ...state.detail, row: { ...state.detail.row, question: { ...state.detail.row.question, queued: true } } };
  flushSync();
  expect(off()).toEqual([false, false]);
});

test("a queued answer hides the question's options", () => {
  const { container } = show({
    row: { unit, kind: "loop_working", actions: turnActions(), question: { record_id: 3, state: "asked", kind: "choice", text: "Publish the review?", queued: true,
      options: [{ id: "a", label: "Publish", consequence: "posts it" }, { id: "b", label: "Hold", consequence: "keeps it" }] } },
    turn: { turn: { seq: 1 }, records: [], answers: [] }, events: [],
  });
  const text = card(container).textContent || "";
  expect(text).toContain("Your answer is queued");
  expect(text).not.toContain("Publish the review?");
  expect(text).not.toContain("Agent asks");
  expect(buttons(container).map((b) => b.textContent)).toEqual(["Send answer", "Continue", "Dismiss"]);
  expect(container.querySelector("label[for=guidance]")?.textContent).toBe("Replace the queued answer");
});

test("a pinned answer is no button and the card says the question's task shows the draft", () => {
  const { container } = show({
    row: { unit, kind: "loop_waiting", actions: turnActions(), question: { state: "asked", kind: "choice", text: "Post?",
      options: [{ id: "post", label: "Post it", draft: { id: 7, rev: 3 } }, { id: "wait", label: "Wait" }] } },
    turn: { turn: { seq: 1 }, records: [], answers: [] }, events: [],
  });
  expect([...card(container).querySelectorAll(".q-options button")].map((b) => b.textContent)).toEqual(["Wait"]);
  expect(card(container).querySelector(".d-note")?.textContent).toContain("An answer approves a draft");
});

test("the turn buttons follow the served states", () => {
  const { specs, rows } = wire();
  state.specs = { version: specs.version, byId: Object.fromEntries(specs.specs.map((x: any) => [x.id, x])) };
  const hidden = { label: "acme/api#7", unit, actions: [{ id: "open", enabled: true }] };
  for (const [name, row] of [["asked_choice", rows.asked_choice], ["blocked", rows.blocked], ["hidden", hidden]] as [string, any][]) {
    for (const id of ["answer", "continue", "dismiss"]) {
      const { container, unmount } = render(TurnButton, { row, slot: id, label: id, name: id, title: "card title" });
      const b = container.querySelector("button");
      const st = (row.actions || []).find((a: any) => a.id === id);
      if (!st) {
        expect(b, `${name} ${id}`).toBeNull();
      } else {
        const sp = state.specs.byId[id];
        const title = st.enabled && !st.note ? "card title" : [sp && sp.summary, st.enabled ? st.note : st.reason].filter(Boolean).join("\n");
        expect([b?.textContent, b?.disabled, b?.title, b?.className], `${name} ${id}`).toEqual([st.label || id, !st.enabled, title, st.enabled ? "act" : "act off"]);
      }
      unmount();
    }
  }
  expect(rows.blocked.actions.some((a: any) => a.id === "continue")).toBe(false);
});
