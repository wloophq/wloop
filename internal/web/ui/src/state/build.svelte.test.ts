import { beforeEach, describe, expect, test } from "vitest";
import { resetStore } from "../testing/reset.svelte";
import { pending } from "./actions.svelte";
import { checkBuild, checkPanelBuild, page, update } from "./build.svelte";
import { askConfirm, showOmnibox } from "./overlays.svelte";
import { busy, detailState, drafts, editPayloads, editing, gateState, outboxState, taskState } from "./store.svelte";

let stored = "";
let reloads = 0;

beforeEach(() => {
  resetStore();
  stored = "";
  reloads = 0;
  update.shown = false;
  Object.assign(page, {
    build: () => "b1",
    reloadedFor: () => stored,
    remember: (b: string) => {
      stored = b;
    },
    reload: () => {
      reloads++;
    },
  });
});

describe("a new build reloads or offers to", () => {
  const cases: [string, string, () => unknown, { reloads: number; banner: boolean; stored: string }][] = [
    ["same build", "b1", () => {}, { reloads: 0, banner: false, stored: "" }],
    ["no build named", "", () => {}, { reloads: 0, banner: false, stored: "" }],
    ["a new build and nothing held", "b2", () => {}, { reloads: 1, banner: false, stored: "b2" }],
    ["text in the unit composer", "b2", () => (detailState.compose.text = "half a reply"), { reloads: 0, banner: true, stored: "" }],
    ["text in the task chat", "b2", () => (taskState.text = "half a reply"), { reloads: 0, banner: true, stored: "" }],
    ["text in the capture field", "b2", () => (drafts.capture = "buy milk"), { reloads: 0, banner: true, stored: "" }],
    ["an open dialog", "b2", () => void askConfirm("Sure?"), { reloads: 0, banner: true, stored: "" }],
    ["guidance kept for a closed detail", "b2", () => (drafts.guidance["acme:api#1"] = "rebase first"), { reloads: 0, banner: true, stored: "" }],
    ["a draft being edited", "b2", () => (taskState.drafts = [{ id: 7 }], editing.set("draft:7", "Deployed")), { reloads: 0, banner: true, stored: "" }],
    ["an edit of a draft the page no longer shows", "b2", () => (editing.set("draft:7", "Deployed"), editPayloads.set("draft:8", { a: 1 })), { reloads: 1, banner: false, stored: "b2" }],
    ["an answer typed", "b2", () => (detailState.answer = "after ten"), { reloads: 0, banner: true, stored: "" }],
    ["an answer typed in a gate", "b2", () => gateState.answers.set("g-1", "after ten"), { reloads: 0, banner: true, stored: "" }],
    ["a draft's field edited", "b2", () => (outboxState.page = { groups: [{ drafts: [{ id: 7 }] }] }, editPayloads.set("draft:7", { title: "x" })), { reloads: 0, banner: true, stored: "" }],
    ["the omnibox open", "b2", () => showOmnibox({ text: "rev" }), { reloads: 0, banner: true, stored: "" }],
    ["an action on its way", "b2", () => busy.add("acme:api#1"), { reloads: 0, banner: true, stored: "" }],
    ["a button's request on its way", "b2", () => pending.add("detail:review"), { reloads: 0, banner: true, stored: "" }],
    ["only blanks typed", "b2", () => (detailState.compose.text = "  \n"), { reloads: 1, banner: false, stored: "b2" }],
    ["reloaded for this build already", "b2", () => (stored = "b2"), { reloads: 0, banner: true, stored: "b2" }],
  ];
  for (const [name, served, setup, want] of cases) {
    test(name, () => {
      setup();
      checkBuild({ build: served });
      expect({ reloads, banner: update.shown, stored }).toEqual(want);
    });
  }

  test("the banner goes once the page's build is served again", () => {
    busy.add("acme:api#1");
    checkBuild({ build: "b2" });
    expect(update.shown).toBe(true);
    checkBuild({ build: "b1" });
    expect(update.shown).toBe(false);
  });

  test("the panel reloads whatever it holds, once per build", () => {
    detailState.compose.text = "half a reply";
    checkPanelBuild({ build: "b2" });
    checkPanelBuild({ build: "b2" });
    expect({ reloads, stored }).toEqual({ reloads: 1, stored: "b2" });
  });
});
