import { fireEvent, render } from "@testing-library/svelte";
import { createRawSnippet, flushSync } from "svelte";
import { beforeEach, expect, test } from "vitest";
import { resetStore } from "../../testing/reset.svelte";
import ActionButton from "./ActionButton.svelte";

const label = createRawSnippet(() => ({ render: () => "<span>Go</span>" }));

beforeEach(() => resetStore());

test("a click runs the action once, off and busy until it answers", async () => {
  let release!: () => void;
  let runs = 0;
  const { container } = render(ActionButton, {
    slot: "detail:review",
    run: () => {
      runs++;
      return new Promise<void>((r) => (release = r));
    },
    children: label,
  });
  const b = container.querySelector("button") as HTMLButtonElement;
  await fireEvent.click(b);
  flushSync();
  expect(b.disabled).toBe(true);
  expect(b.getAttribute("aria-busy")).toBe("true");
  await fireEvent.click(b);
  expect(runs).toBe(1);
  release();
  await Promise.resolve();
  await Promise.resolve();
  flushSync();
  expect(b.disabled).toBe(false);
  expect(b.hasAttribute("aria-busy")).toBe(false);
  expect(b.dataset.focus).toBe("detail:review");
});
