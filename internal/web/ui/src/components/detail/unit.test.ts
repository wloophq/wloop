import { beforeEach, expect, test } from "vitest";
import { applyCatalog } from "../../lib/catalog";
import { applyStrings } from "../../lib/i18n.svelte";
import { eventLabel, loopEvent } from "../../lib/catalog";
import { fieldOf } from "../../lib/lens";
import { catalog, state } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import {
  answerableQuestion, answerSaid, detailFacts, guidanceLabel, intakeLines, questionChoices, questionPins, timelineEvents, trimmedBlocks,
  unitHomeView,
} from "./unit";

beforeEach(() => resetStore());

const row = { unit: { provider: "github", key: "acme/api#7" }, actions: [{ id: "answer", enabled: true }] };

test("answerSaid joins the choice and the note", () => {
  const said = [{ yes: true }, { yes: true, text: "but squash" }, { option_id: "b", text: "after CI" }, { no: true, text: "drop the nit" }, { text: "look elsewhere" }, {}].map(answerSaid);
  expect(said).toEqual(["yes", "yes. but squash", "option b. after CI", "no. drop the nit", "look elsewhere", "no"]);
});

test("the timeline shows an answer once when the turn has no sent_at", () => {
  const got = timelineEvents({
    turn: { turn: { seq: 1 }, answers: [{ turn_seq: 1, text: "go", source: "tui", at: "2026-10-18T12:00:00Z" }] },
    events: [{ kind: "answered", at: "2026-10-18T12:00:00Z", data: "go" }],
  });
  expect(got.map((e) => e.data)).toEqual(["go (from tui)"]);
});

test("the timeline says what the answer chose and that it is queued, newest first", () => {
  const answers = [
    { turn_seq: 1, yes: true, text: "but thank them", source: "cockpit", route: "typed", at: "2026-10-18T12:01:00Z" },
    { turn_seq: 1, no: true, source: "notification", route: "typed", at: "2026-10-18T12:02:00Z" },
    { turn_seq: 1, option_id: "publish", text: "Publish (option publish)", source: "cockpit", route: "typed", at: "2026-10-18T12:03:00Z" },
    { turn_seq: 1, option_id: "publish", text: "shorten it", source: "cockpit", route: "typed", at: "2026-10-18T12:04:00Z" },
    { turn_seq: 1, text: "wait for CI", source: "cockpit", route: "queued", at: "2026-10-18T12:05:00Z" },
  ];
  const got = timelineEvents({ turn: { turn: { seq: 1 }, answers }, events: [] });
  expect(got.map((e) => e.data)).toEqual([
    "wait for CI (from cockpit, queued until the agent stops)",
    "option publish. shorten it (from cockpit)",
    "Publish (option publish) (from cockpit)",
    "no (from notification)",
    "yes. but thank them (from cockpit)",
  ]);
});

test("timeline events keep distinct ids when two look alike", () => {
  const e = { kind: "push", at: "2026-10-18T12:00:00Z", actor: "ana", data: "" };
  const got = timelineEvents({ events: [e, { ...e }] });
  expect(new Set(got.map((x) => x.id)).size).toBe(2);
});

test("the turn detail names the trimmed blocks of the current turn only", () => {
  expect(trimmedBlocks({ turn: { turn: { id: "r2" } }, events: [{ kind: "guide_trimmed", key: "r1", data: "old" }, { kind: "guide_trimmed", key: "r2", data: "user: tag=lang=go/intent=review.md" }] }))
    .toBe("user: tag=lang=go/intent=review.md");
  expect(trimmedBlocks({ turn: { turn: { id: "r3" } }, events: [] })).toBe("");
});

test("intake lines say the attempt, the last error and the next attempt", () => {
  expect(intakeLines({ seq: 42, intent: "review", state: "briefing", attempt: 2, last_error: "rate limited", next: "2026-10-18T14:05:00Z" })).toEqual([
    "Intake 42 (review) waits for the brief, attempt 2",
    "Last error: rate limited",
    expect.stringMatching(/^Next attempt at .+/),
  ]);
  expect(intakeLines({ seq: 43, intent: "review" })).toEqual(["Intake 43 (review) waits for the brief"]);
});

test("a row's pinned answer is no button, and the question says it pins", () => {
  const choice = { state: "asked", kind: "choice", options: [{ id: "post", label: "Post it", draft: { id: 7, rev: 3 } }, { id: "wait", label: "Wait" }] };
  const confirm = { state: "asked", kind: "confirm", draft: { id: 7, rev: 3 } };
  expect(questionChoices(row, choice).map((c) => c.label)).toEqual(["Wait"]);
  expect(questionChoices(row, confirm).map((c) => c.slot)).toEqual(["no"]);
  expect(questionPins(confirm)).toBe(true);
  expect(questionPins({ kind: "choice", options: [{ id: "a" }] })).toBe(false);
});

test("choices put the recommended option first and need the served answer", () => {
  const q = { state: "asked", kind: "choice", recommend: "b", options: [{ id: "a", label: "A" }, { id: "b", label: "B", consequence: "does b" }] };
  expect(questionChoices(row, q).map((c) => [c.slot, c.rec, c.title])).toEqual([["option:b", true, "does b"], ["option:a", false, undefined]]);
  expect(questionChoices({ ...row, actions: [] }, q)).toEqual([]);
});

test("the guidance box's label follows the question and the pending round", () => {
  const asked = { ...row, question: { state: "asked", kind: "choice", options: [{ id: "a", label: "A" }] } };
  expect(guidanceLabel(asked)).toBe("Or answer in your own words");
  expect(guidanceLabel({ ...row, question: { state: "unrecorded" } })).toBe("Answer in your own words");
  expect(guidanceLabel({ ...row, question: { state: "asked", queued: true } })).toBe("Replace the queued answer");
  expect(guidanceLabel(row)).toBe("Guidance for the next round (optional)");
  state.roundGates = new Map([["github:acme/api#7", { deadline: "2026-11-07T12:05:00Z" }]]);
  expect(guidanceLabel(row)).toBe("Guidance for the pending round");
  state.specs = { version: "v", byId: { run_now: { id: "run_now", group: "start", verb: "approve", label: "Run now" } } };
  expect(guidanceLabel(row)).toBe("Guidance for the pending round; Run now sends it at once");
});

test("the keys answer an open question, never a queued one", () => {
  const d = (queued: boolean) => ({ row: { question: { state: "asked", queued } } });
  expect(!!answerableQuestion(d(false))).toBe(true);
  expect(!!answerableQuestion(d(true))).toBe(false);
});

test("the detail shows its plugin's fields as the catalog words them", () => {
  catalog.details = [
    { provider: "github", field: "next_guidance", label: "Next round guidance" },
    { provider: "github", field: "profile_chain", label: "Review profile", title: "the profiles in order" },
    { provider: "slack", field: "channel", label: "Channel" },
  ];
  const facts = [
    ...detailFacts({ unit: { provider: "github" } }, { profile_chain: "base > acme" }, { next_guidance: "focus on tests" }),
    ...detailFacts({ unit: { provider: "slack" } }, { profile_chain: "base" }, {}),
  ];
  expect(facts.map((f) => [f.label, f.value, f.title])).toEqual([
    ["Next round guidance", "focus on tests", undefined],
    ["Review profile", "base > acme", "the profiles in order"],
  ]);
});

test("a blocked round's event shows in the served words", () => {
  applyCatalog({ events: [{ provider: "github", kind: "round_blocked", label: "round blocked", loop: true, tone: "loop" }] }, fieldOf);
  expect(eventLabel("round_blocked")).toBe("round blocked");
  expect(loopEvent("round_blocked")).toBe(true);
});

test("a unit opens in its provider's all view, else its first rows view", () => {
  const views = [{ id: "needs-me", provider: "github", rows: true }, { id: "dash", provider: "github", rows: false }, { id: "all", provider: "github", rows: true }, { id: "threads", provider: "slack", rows: true }];
  expect(unitHomeView("github", views)).toBe("all");
  expect(unitHomeView("slack", views)).toBe("threads");
  expect(unitHomeView("wiki", views)).toBe("all");
});

test("labels follow the locale's strings", () => {
  applyStrings({ locale: "pt-BR", strings: { "Answer in your own words": "Responda com suas palavras" } });
  expect(guidanceLabel({ ...row, question: { state: "unrecorded" } })).toBe("Responda com suas palavras");
});
