import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test } from "vitest";
import { I18N } from "../../lib/i18n.svelte";
import { resetStore } from "../../testing/reset.svelte";
import Markdown from "./Markdown.svelte";

beforeEach(() => resetStore());

function md(text: string, props: Record<string, unknown> = {}): HTMLElement {
  const { container } = render(Markdown, { text, ...props });
  return container.querySelector(".md") as HTMLElement;
}

const tags = (root: Element) => [...root.children].map((c) => c.tagName.toLowerCase());
const texts = (root: Element, sel: string) => [...root.querySelectorAll(sel)].map((n) => n.textContent);

describe("Markdown", () => {
  test("draws the agents' subset", () => {
    const root = md("Use `retry()` **now**.\n\n- one\n- two\n\n```\nx := 1\n```");
    expect(root.tagName).toBe("DIV");
    expect(root.className).toBe("md");
    expect(tags(root)).toEqual(["p", "ul", "pre"]);
    const p = root.children[0];
    expect(texts(p, "code")).toEqual(["retry()"]);
    expect(texts(p, "strong")).toEqual(["now"]);
    expect(p.textContent).toBe("Use retry() now.");
    expect(texts(root.children[1], "li")).toEqual(["one", "two"]);
    expect(texts(root.children[2], "code")).toEqual(["x := 1"]);
  });

  test("draws headings, quotes, emphasis and ordered lists", () => {
    const root = md("## Plan\n\n> a *quoted* line\n> goes on\n\n1. first\n2. second_one with snake_case");
    expect(tags(root)).toEqual(["h4", "blockquote", "ol"]);
    expect(texts(root.children[1], "em")).toEqual(["quoted"]);
    expect(texts(root.children[2], "li")).toEqual(["first", "second_one with snake_case"]);
  });

  test("a line break inside a paragraph is a br", () => {
    const root = md("one\ntwo");
    expect(root.querySelectorAll("p > br")).toHaveLength(1);
  });

  test("never builds markup from text", () => {
    const raw = "<img src=x onerror=alert(1)> <b>hi</b>";
    const root = md(raw);
    expect(tags(root)).toEqual(["p"]);
    expect(root.children[0].textContent).toBe(raw);
    expect(root.querySelectorAll("img, b")).toHaveLength(0);
  });

  test("links only to https", () => {
    const root = md("[a](https://example.test/x) [b](javascript:alert(1)) [c](http://x.test) see https://example.test/y.");
    const links = [...root.querySelectorAll("a")];
    expect(links).toHaveLength(2);
    const [a, bare] = links;
    expect(a.textContent).toBe("a");
    expect(a.getAttribute("href")).toBe("https://example.test/x");
    expect(a.getAttribute("target")).toBe("_blank");
    expect(a.getAttribute("rel")).toBe("noopener noreferrer");
    expect(bare.getAttribute("href")).toBe("https://example.test/y");
    expect(bare.textContent).toBe("https://example.test/y");
    expect(root.children[0].textContent).toBe("a b c see https://example.test/y.");
  });

  test("folds long prose until the reader opens it", async () => {
    const text = Array.from({ length: 12 }, (_, i) => "para " + String.fromCharCode(97 + i)).join("\n\n");
    const root = md(text, { fold: 3, key: "gate:7" });
    expect(root.querySelectorAll("p")).toHaveLength(3);
    const button = root.querySelector("button.md-more") as HTMLButtonElement;
    expect(button.textContent).toBe("Read all");
    expect(button.type).toBe("button");
    const first = root.querySelector("p");
    await fireEvent.click(button);
    expect(root.querySelectorAll("p")).toHaveLength(12);
    expect(root.querySelectorAll("button")).toHaveLength(0);
    expect(root.querySelector("p")).toBe(first);
    const other = md(text, { fold: 3, key: "gate:8" });
    expect(other.querySelectorAll("p")).toHaveLength(3);
  });

  test("an opened text stays open when its text grows", async () => {
    const text = Array.from({ length: 6 }, (_, i) => "para " + i).join("\n\n");
    const { container, rerender } = render(Markdown, { text, fold: 3 });
    await fireEvent.click(container.querySelector("button.md-more") as HTMLButtonElement);
    const first = container.querySelector("p");
    await rerender({ text: text + "\n\npara 6", fold: 3 });
    expect(container.querySelectorAll("p")).toHaveLength(7);
    expect(container.querySelector("p")).toBe(first);
  });

  test("the fold button reads in the interface language", () => {
    const text = Array.from({ length: 4 }, (_, i) => "para " + i).join("\n\n");
    const root = md(text, { fold: 2 });
    I18N.strings = { "Read all": "Ler tudo" };
    flushSync();
    expect((root.querySelector("button.md-more") as HTMLElement).textContent).toBe("Ler tudo");
  });

  test("folds a huge answer cheaply", () => {
    const text = Array.from({ length: 5000 }, () => "line with `code` and **bold**").join("\n\n");
    expect(md(text, { fold: 4 }).querySelectorAll("p")).toHaveLength(4);
  });

  test("caps nesting", () => {
    const root = md(">".repeat(10000) + " x");
    const quotes = root.querySelectorAll("blockquote").length;
    expect(quotes).toBeGreaterThan(0);
    expect(quotes).toBeLessThanOrEqual(8);
    expect((root.textContent || "").trim().endsWith("x")).toBe(true);
    const deep = md("*a ".repeat(5000) + "b" + " a*".repeat(5000));
    expect(deep.querySelectorAll("em").length).toBeLessThanOrEqual(8);
  });

  test("a link label makes no link inside", () => {
    const root = md("[https://a.test](https://b.test) and [x [y](https://c.test)](https://d.test)");
    const links = [...root.querySelectorAll("a")];
    for (const a of links) expect(a.querySelectorAll("a")).toHaveLength(0);
    expect(links[0].getAttribute("href")).toBe("https://b.test");
    expect(links[0].textContent).toBe("https://a.test");
  });

  test.each(["a **b and `c", "2 * 3 * 4", "[label](no close", "_x"])("keeps the unclosed markers of %j as text", (raw) => {
    const root = md(raw);
    expect(root.children).toHaveLength(1);
    expect(root.children[0].textContent).toBe(raw);
    expect(root.querySelectorAll("strong, em, code, a")).toHaveLength(0);
  });

  test("keeps braces as written without names", () => {
    const raw = "Run git rebase @{upstream} or git reset --hard HEAD@{1}.";
    const root = md(raw);
    expect(root.textContent?.trim()).toBe(raw);
    expect(root.querySelectorAll(".mention")).toHaveLength(0);
  });

  test("names mentions as text", () => {
    const root = md("Hi @{slack:UANA}, see [@{slack:UBRUNO}](https://a.test), `@{slack:UANA}` and @{group:x y} or @{see below} or @{<b>}", {
      names: { "slack:UANA": "Ana", "<b>": "<i>x</i>" },
    });
    const mentions = [...root.querySelectorAll(".mention")];
    for (const m of mentions) expect(m.children).toHaveLength(0);
    expect(mentions.map((m) => m.textContent + "|" + m.getAttribute("title"))).toEqual([
      "@Ana|slack:UANA",
      "@slack:UBRUNO|slack:UBRUNO",
      "@group:x y|group:x y",
      "@<i>x</i>|<b>",
    ]);
    const links = root.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0].querySelectorAll(".mention")).toHaveLength(1);
    expect(texts(root, "code")).toEqual(["@{slack:UANA}"]);
  });
});

test("the text carries no whitespace the prose lacks", () => {
  const c = render(Markdown, { text: "One para.\n\nTwo." }).container;
  expect(c.querySelector(".md")?.textContent).toBe("One para.Two.");
});

test("an opened text folds again when another text, of another key, takes its place", async () => {
  const long = Array.from({ length: 8 }, (_, i) => `Paragraph ${i}.`).join("\n\n");
  const props = $state({ text: long, fold: 2, key: "a" });
  const c = render(Markdown, props).container;
  await fireEvent.click(c.querySelector(".md-more") as HTMLButtonElement);
  expect(c.querySelector(".md-more")).toBeNull();
  props.text = long + "\n\nMore.";
  flushSync();
  expect(c.querySelector(".md-more")).toBeNull();
  props.key = "b";
  flushSync();
  expect(c.querySelector(".md-more")).not.toBeNull();
});

test("a text the reader opened stays open when it is drawn again under its key", async () => {
  const long = Array.from({ length: 8 }, (_, i) => `Paragraph ${i}.`).join("\n\n");
  const first = render(Markdown, { text: long, fold: 2, key: "brief:t-1" });
  await fireEvent.click(first.container.querySelector(".md-more") as HTMLButtonElement);
  first.unmount();
  const again = render(Markdown, { text: long, fold: 2, key: "brief:t-1" }).container;
  expect(again.querySelector(".md-more")).toBeNull();
  const other = render(Markdown, { text: long, fold: 2, key: "brief:t-2" }).container;
  expect(other.querySelector(".md-more")).not.toBeNull();
});
