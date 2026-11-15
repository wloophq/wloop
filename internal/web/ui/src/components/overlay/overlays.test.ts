import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { askConfirm, askInput, overlays, showPalette, type PaletteItem } from "../../state/overlays.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { omni, showHelp, showOmnibox } from "../../state/overlays.svelte";
import Overlays from "./Overlays.svelte";
import { settle } from "../../testing/settle";


// track follows a promise: what it settled to, or "open".
function track<T>(p: Promise<T>): { v: T | "open" } {
  const s: { v: T | "open" } = { v: "open" };
  void p.then((v) => { s.v = v; });
  return s;
}

const shades = () => [...document.querySelectorAll<HTMLElement>(".pal-back")];

afterEach(cleanup);

beforeEach(() => {
  resetStore();
  omni.open = null;
  document.body.replaceChildren();
});

function page() {
  const before = document.createElement("button");
  document.body.append(before);
  before.focus();
  const r = render(Overlays);
  return { ...r, before };
}

describe("overlay_test.go", () => {
  test("TestAQuestionAskedFromAnOverlayLeavesItOpen", async () => {
    const answers: Record<string, unknown> = {};
    for (const [name, ask, answer] of [
      ["cancel", () => askConfirm("Remove acme/api#8?"), (s: HTMLElement) => fireEvent.keyDown(s.querySelector('[role="alertdialog"]')!, { key: "Escape" })],
      ["confirm", () => askConfirm("Remove acme/api#8?"), (s: HTMLElement) => fireEvent.click(s.querySelectorAll("button")[1])],
      ["input", () => askInput({ label: "Snooze for" }), (s: HTMLElement) => { const i = s.querySelector("input")!; i.value = "2d"; return fireEvent.keyDown(i, { key: "Enter" }); }],
    ] as const) {
      const p = page();
      showOmnibox();
      await settle();
      const field = document.querySelector<HTMLInputElement>(".omni-input")!;
      expect(document.activeElement).toBe(field);
      const asked = track<unknown>(ask());
      await settle();
      const open = shades().length;
      await answer(shades().at(-1)!);
      await settle();
      answers[name] = asked.v;
      expect(open * 10 + shades().length, name).toBe(21);
      expect(omni.open, name).not.toBeNull();
      expect(document.activeElement, name).toBe(field);
      p.unmount();
      omni.open = null;
    }
    expect(answers).toEqual({ cancel: false, confirm: true, input: "2d" });
  });

  test("TestAQuestionWhoseOverlayGoesAwayAnswersCancel", async () => {
    for (const [name, ask, away, left] of [
      ["confirm replaced", () => askConfirm("Remove acme/api#8?"), () => { void showPalette({ title: "Area", items: [] }); }, 1],
      ["input replaced", () => askInput({ label: "Snooze for" }), () => { void showHelp([]); }, 1],
      ["under its overlay closed", () => askConfirm("Remove acme/api#8?"), () => fireEvent.keyDown(document.querySelector(".omni-input")!, { key: "Escape" }), 0],
    ] as const) {
      const p = page();
      showOmnibox();
      await settle();
      const asked = track<unknown>(ask());
      await settle();
      await away();
      await settle();
      expect(asked.v === false || asked.v === null, name).toBe(true);
      expect(omni.open, name).toBeNull();
      expect(shades(), name).toHaveLength(left);
      p.unmount();
      resetStore();
    }
  });

  test("TestTheConfirmKeepsTheKeysAndTheSecondClick", async () => {
    page();
    const asked = track(askConfirm("Remove acme/api#8?"));
    await settle();
    const [no, yes] = [...shades()[0].querySelectorAll("button")];
    expect(document.activeElement).toBe(yes);
    const tab = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    yes.dispatchEvent(tab);
    expect(document.activeElement).toBe(no);
    expect(tab.defaultPrevented).toBe(true);
    await fireEvent.keyDown(no, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(yes);
    const shade = shades()[0];
    await fireEvent.mouseDown(shade, { detail: 2 });
    await settle();
    expect(asked.v).toBe("open");
    await fireEvent.mouseDown(shade, { detail: 1 });
    await settle();
    expect(asked.v).toBe(false);
    const again = track(askConfirm("Remove acme/api#8?"));
    await settle();
    const cancel = shades()[0].querySelectorAll("button")[0];
    cancel.focus();
    const enter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    cancel.dispatchEvent(enter);
    if (!enter.defaultPrevented) cancel.click();
    await settle();
    expect(again.v).toBe(false);
  });

  test("a confirm answered gives the focus back where it was", async () => {
    const p = page();
    const asked = track(askConfirm("Remove acme/api#8?"));
    await settle();
    await fireEvent.keyDown(shades()[0].querySelector('[role="alertdialog"]')!, { key: "Enter" });
    await settle();
    expect(asked.v).toBe(true);
    expect(document.activeElement).toBe(p.before);
    expect(overlays.stack).toHaveLength(0);
  });

  test("the input answers null when it loses the focus or on a click outside", async () => {
    page();
    const blurred = track(askInput({ label: "Snooze for", value: "2d" }));
    await settle();
    const i = shades()[0].querySelector("input")!;
    expect(i.value).toBe("2d");
    await fireEvent.blur(i);
    await settle();
    expect(blurred.v).toBeNull();
    const outside = track(askInput({ label: "Snooze for" }));
    await settle();
    await fireEvent.mouseDown(shades()[0], { detail: 1 });
    await settle();
    expect(outside.v).toBeNull();
  });

  test("the help lists every binding by context and closes on Esc or ?", async () => {
    page();
    void showHelp([{ head: "Everywhere", bindings: [{ key: "?", action: "help", help: "help" }, { key: "/", action: "filter" }] }]);
    await settle();
    const box = shades()[0].querySelector<HTMLElement>('[role="dialog"]')!;
    expect(document.activeElement).toBe(box);
    expect([...box.querySelectorAll("dt")].map((x) => x.textContent)).toEqual(["Everywhere"]);
    expect([...box.querySelectorAll("dd")].map((x) => x.textContent)).toEqual(["help?", "filter/"]);
    await fireEvent.keyDown(box, { key: "?" });
    await settle();
    expect(shades()).toHaveLength(0);
  });

  test("a palette narrows its items as typed, arrows move, Enter runs the highlighted", async () => {
    page();
    const ran: string[] = [];
    const item = (label: string, key = ""): PaletteItem => ({ label, key, run: () => ran.push(label) });
    void showPalette({ title: "Actions", items: [item("to Next", "x"), item("done", "e"), item("to Waiting", "w")] });
    await settle();
    const input = shades()[0].querySelector("input")!;
    expect(document.activeElement).toBe(input);
    input.value = "to";
    await fireEvent.input(input);
    await settle();
    expect([...shades()[0].querySelectorAll("li")].map((x) => x.textContent)).toEqual(["to Nextx", "to Waitingw"]);
    await fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(input.getAttribute("aria-activedescendant")).toBe("pal-opt-1");
    await fireEvent.keyDown(input, { key: "Enter" });
    await settle();
    expect(ran).toEqual(["to Waiting"]);
    expect(shades()).toHaveLength(0);
  });
});

describe("merge_test.go", () => {
  test("TestThePaletteShowsTheLatestTextsAnswerAndEnterWaitsForIt", async () => {
    page();
    const runs: string[] = [];
    const item = (label: string, meta: string): PaletteItem => ({ label, meta, run: () => runs.push(label) });
    // opened shows a palette whose source answers each text when the test says, by answer(text, items).
    const opened = async (items: PaletteItem[]) => {
      const waiting = new Map<string, (v: PaletteItem[]) => void>();
      void showPalette({ title: "Merge with which task", items, empty: "No task holds the text", source: (q) => new Promise((r) => waiting.set(q, r)) });
      await settle();
      const shade = shades().at(-1)!;
      const input = shade.querySelector("input")!;
      return {
        shade, input,
        list: shade.querySelector("ol")!,
        empty: shade.querySelector(".pal-empty")!,
        type: async (q: string) => { input.value = q; await fireEvent.input(input); },
        answer: async (q: string, next: PaletteItem[]) => { waiting.get(q)!(next); await settle(); },
        shown: () => [...shade.querySelectorAll("li")].map((x) => x.textContent || ""),
      };
    };
    const a = await opened([item("Write the runbook", "Next")]);
    expect(a.input.getAttribute("role")).toBe("combobox");
    expect(a.input.getAttribute("aria-controls")).toBe(a.list.id);
    expect(a.input.getAttribute("aria-activedescendant")).toBe(a.list.querySelector("li")!.id);
    expect([...a.list.querySelectorAll(".pill")].map((x) => x.textContent)).toEqual(["Next"]);
    expect(a.list.querySelectorAll("kbd")).toHaveLength(0);
    await a.type("la");
    await a.type("launch hall");
    await a.answer("launch hall", [item("Book the launch hall", "Inbox")]);
    await a.answer("la", [item("Book the launch venue", "Inbox")]);
    expect(a.shown()).toHaveLength(1);
    expect(a.shown()[0].startsWith("Book the launch hall")).toBe(true);
    resetStore();

    const b = await opened([item("Book the launch venue", "Inbox")]);
    await b.type("launch hall");
    await fireEvent.keyDown(b.input, { key: "Enter" });
    await settle();
    expect(runs).toEqual([]);
    await b.answer("launch hall", [item("Book the launch hall", "Inbox")]);

    const d = await opened([item("Book the launch venue", "Inbox")]);
    await d.type("launch hall");
    await fireEvent.keyDown(d.input, { key: "Enter" });
    await fireEvent.keyDown(d.input, { key: "Escape" });
    await d.answer("launch hall", [item("Book the launch hall again", "Inbox")]);
    const e = await opened([item("Book the launch venue", "Inbox")]);
    await e.type("launch hall");
    await fireEvent.keyDown(e.input, { key: "Enter" });
    await fireEvent.mouseDown(e.shade, { detail: 1 });
    await e.answer("launch hall", [item("Book the launch hall from the backdrop", "Inbox")]);

    const c = await opened([item("Write the runbook", "Next")]);
    await c.type("zz");
    await c.answer("zz", []);
    expect(runs).toEqual(["Book the launch hall"]);
    expect(c.empty.textContent).toBe("No task holds the text");
    expect(c.empty.getAttribute("aria-live")).toBe("polite");
  });
});

describe("dialogs_test.go", () => {
  test("TestThePagesAskNothingThroughBrowserDialogs", () => {
    const native = /(^|[^.\w$]|\b(window|globalThis|self)\.)(confirm|prompt|alert)\s*\(/;
    for (const [line, asks] of [
      [`if (!confirm("Remove?")) return;`, true], [`window.confirm("Remove?")`, true], [`globalThis.prompt ("Days?")`, true], [`alert("x")`, true],
      [`await askConfirm("Remove?")`, false], [`env.confirm(text)`, false], [`askInput({ label })`, false],
    ] as const) {
      expect(native.test(line), line).toBe(asks);
    }
    const walk = (dir: string): string[] => readdirSync(dir).flatMap((n) => {
      const p = join(dir, n);
      return statSync(p).isDirectory() ? walk(p) : /\.(ts|svelte)$/.test(n) && !n.endsWith(".test.ts") ? [p] : [];
    });
    const asking: string[] = [];
    for (const file of walk(resolve(process.cwd(), "src"))) {
      readFileSync(file, "utf8").split("\n").forEach((line, i) => {
        if (native.test(line) && !line.trim().startsWith("//")) asking.push(`${file}:${i + 1}: ${line.trim()}`);
      });
    }
    expect(asking).toEqual([]);
  });

  test("window.confirm is never called: the confirm asks in the page", async () => {
    page();
    const called: string[] = [];
    const saved = { confirm: window.confirm, prompt: window.prompt, alert: window.alert };
    window.confirm = () => { called.push("confirm"); return true; };
    window.prompt = () => { called.push("prompt"); return ""; };
    window.alert = () => { called.push("alert"); };
    try {
      void askConfirm("Remove acme/api#8?");
      void askInput({ label: "Snooze for" });
      await settle();
      expect(shades()).toHaveLength(2);
      expect(called).toEqual([]);
    } finally {
      Object.assign(window, saved);
    }
  });
});
