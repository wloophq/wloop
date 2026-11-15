import { afterEach, beforeEach, expect, test } from "vitest";
import { resetStore } from "../../testing/reset.svelte";
import { afterSync } from "../../state/sync.svelte";
import { followFocus, rescueFocus } from "./focus";

let stop = () => {};
beforeEach(() => {
  resetStore();
  document.body.textContent = "";
  stop = followFocus(document);
});
afterEach(() => {
  stop();
  document.body.textContent = "";
});

function row(key: string): HTMLElement {
  const r = document.createElement("div");
  r.setAttribute("data-row", "row:" + key);
  r.setAttribute("data-focus", "row:" + key);
  r.tabIndex = 0;
  const b = document.createElement("button");
  b.setAttribute("data-focus", `row:${key}:review`);
  r.append(b);
  document.body.append(r);
  return r;
}

test("a row's control that goes away after a read hands the focus to its row", async () => {
  const r = row("acme:web#7");
  const b = r.querySelector("button") as HTMLButtonElement;
  b.focus();
  b.remove();
  for (const fn of afterSync) fn();
  await new Promise((res) => setTimeout(res, 0));
  expect(document.activeElement).toBe(r);
});

test("closing a detail gives the focus to the row it was opened from, never over a focus still on the page", async () => {
  const a = row("acme:web#1");
  const b = row("acme:web#2");
  await rescueFocus(document, "row:acme:web#2");
  expect(document.activeElement).toBe(b);
  a.focus();
  await rescueFocus(document, "row:acme:web#2");
  expect(document.activeElement).toBe(a);
});
