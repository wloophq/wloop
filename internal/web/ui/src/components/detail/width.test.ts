import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { afterEach, beforeEach, expect, test } from "vitest";
import { resetStore } from "../../testing/reset.svelte";
import DetailResize from "./DetailResize.svelte";
import { clampDetailWidth, DETAIL_WIDTH_KEY, defaultDetailWidth, saveDetailWidth, sheet, storedDetailWidth } from "./width";

const original = { ...sheet };
const viewport = (width: number) => (window as unknown as { happyDOM: { setViewport(v: { width: number; height: number }): void } }).happyDOM.setViewport({ width, height: 900 });
const root = () => document.documentElement.style;

function memory(initial: string | null, throws = false): Storage & { value: string | null } {
  const s = {
    value: initial,
    getItem: (k: string) => {
      if (throws) throw new Error("denied");
      return k === DETAIL_WIDTH_KEY ? s.value : null;
    },
    setItem: (_k: string, v: string) => {
      if (throws) throw new Error("denied");
      s.value = v;
    },
    removeItem: () => {
      if (throws) throw new Error("denied");
      s.value = null;
    },
  };
  return s as unknown as Storage & { value: string | null };
}

beforeEach(() => {
  resetStore();
  root().removeProperty("--detail-width");
  viewport(1600);
});
afterEach(() => Object.assign(sheet, original));

test("the width is clamped between the minimum and 60% of the viewport", () => {
  sheet.cssWidth = () => " 380px";
  const cases: [number, number, number][] = [[380, 1600, 380], [100, 1600, 300], [5000, 1600, 960], [960, 1600, 960], [961, 1600, 960], [500, 400, 300], [299.6, 1600, 300]];
  for (const [px, vw, want] of cases) expect(clampDetailWidth(px, vw)).toBe(want);
});

test("a stored width is restored clamped; none, junk or refused storage take the stylesheet's", () => {
  sheet.cssWidth = () => " 412px";
  const cases: [string | null, boolean, number][] = [[null, false, 412], ["520", false, 520], ["50", false, 300], ["99999", false, 960], ["junk", false, 412], ["520", true, 412]];
  for (const [stored, throws, want] of cases) {
    sheet.storage = () =>
      ({
        getItem: () => {
          if (throws) throw new Error("denied");
          return stored;
        },
        setItem: () => {
          throw new Error("denied");
        },
        removeItem: () => {
          throw new Error("denied");
        },
      }) as unknown as Storage;
    saveDetailWidth(400);
    saveDetailWidth(null);
    expect(storedDetailWidth(1600)).toBe(want);
  }
});

test("without the stylesheet's value the width is the minimum", () => {
  sheet.cssWidth = () => "";
  expect(defaultDetailWidth()).toBe(300);
  expect(clampDetailWidth(NaN, 1600)).toBe(300);
});

test("the chosen width survives a shrink then a grow of the window", () => {
  sheet.cssWidth = () => "380px";
  sheet.storage = () => memory("900");
  const { container } = render(DetailResize);
  const seen = [root().getPropertyValue("--detail-width")];
  viewport(700);
  flushSync();
  seen.push(root().getPropertyValue("--detail-width"));
  viewport(1600);
  flushSync();
  seen.push(root().getPropertyValue("--detail-width"));
  expect(seen).toEqual(["900px", "420px", "900px"]);
  const handle = container.querySelector("#detail-resize") as HTMLElement;
  expect(handle.getAttribute("aria-valuenow")).toBe("900");
  expect(handle.getAttribute("aria-valuemin")).toBe("300");
  expect(handle.getAttribute("aria-valuemax")).toBe("960");
  expect(handle.getAttribute("role")).toBe("separator");
});

test("a double click restores the stylesheet's default and forgets the stored width", async () => {
  sheet.cssWidth = () => root().getPropertyValue("--detail-width") || "380px";
  const store = memory("900");
  sheet.storage = () => store;
  const { container } = render(DetailResize);
  expect(root().getPropertyValue("--detail-width")).toBe("900px");
  await fireEvent.dblClick(container.querySelector("#detail-resize") as HTMLElement);
  expect(root().getPropertyValue("--detail-width")).toBe("380px");
  expect(store.value).toBe(null);
});

test("the arrows widen and narrow the panel and keep the width", async () => {
  sheet.cssWidth = () => "380px";
  const store = memory(null);
  sheet.storage = () => store;
  const { container } = render(DetailResize);
  const handle = container.querySelector("#detail-resize") as HTMLElement;
  await fireEvent.keyDown(handle, { key: "ArrowLeft" });
  expect(root().getPropertyValue("--detail-width")).toBe("396px");
  expect(store.value).toBe("396");
  await fireEvent.keyDown(handle, { key: "ArrowRight" });
  await fireEvent.keyDown(handle, { key: "ArrowRight" });
  expect(root().getPropertyValue("--detail-width")).toBe("364px");
  expect(handle.getAttribute("aria-valuenow")).toBe("364");
});
