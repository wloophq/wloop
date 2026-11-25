import { afterEach, expect, test } from "vitest";
import { reviewDraft } from "../../testing/gates";
import { draftSummary, duration, FLOAT_BRIDGE, floatPost, installFloat, oldest, progress } from "./float";
import { floatDeps, floatState, resetFloat } from "../../state/float.svelte";

const saved = { ...floatDeps };
afterEach(() => {
  Object.assign(floatDeps, saved);
  resetFloat();
});

test("a review draft reads as its verdict, its comments where they sit, and their tally", () => {
  const d = reviewDraft();
  d.fields[1].fields[0].fields[3].value = "issue (blocking): return the error.";
  const s = draftSummary(d);
  expect([s.verdict, s.tone]).toEqual(["Request changes", ""]);
  expect(s.comments.map((c) => [c.at, c.label || "", !!c.blocking])).toEqual([["api/handler.go:42", "issue", true], ["api/retry.go:50", "", false]]);
  expect(s.tally).toEqual([{ label: "issue", blocking: true, n: 1 }]);
});

test("a draft with no payload reads as no verdict and no comments", () => {
  expect(draftSummary({ text: "Thanks!" })).toEqual({ verdict: "", tone: "", comments: [], tally: [] });
});

test("durations read in the figure's compact words, and progress stops at the usual", () => {
  expect([duration(42_000), duration(5 * 60_000), duration(90 * 60_000), duration(2 * 3600_000)]).toEqual(["42s", "5m", "1h 30m", "2h"]);
  expect([progress(30, 60), progress(90, 60), progress(5, 0)]).toEqual([0.5, 1, 0]);
  expect(oldest([{ since: "2026-11-20T10:00:00Z" }, { since: "2026-11-19T10:00:00Z" }, {}])).toBe(Date.parse("2026-11-19T10:00:00Z"));
});

test("the host bridge sets the mode, and the page posts JSON through the Wails handler, nowhere in a browser", () => {
  const sent: string[] = [];
  const win: Record<string, unknown> = { webkit: { messageHandlers: { external: { postMessage: (m: string) => sent.push(m) } } } };
  const bridge = installFloat(win);
  expect(win[FLOAT_BRIDGE]).toBe(bridge);
  bridge.setMode("panel", { edge: "right", up: true });
  expect([floatState().mode, floatState().place]).toEqual(["panel", { edge: "right", up: true }]);
  floatDeps.post({ type: "float:size", w: 112, h: 32 });
  expect(sent).toEqual(['{"type":"float:size","w":112,"h":32}']);
  expect(() => floatPost({ type: "float:hide", mode: "quiet" }, {})).not.toThrow();
});
