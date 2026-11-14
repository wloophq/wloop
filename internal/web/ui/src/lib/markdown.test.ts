import { afterEach, describe, expect, test } from "vitest";
import { foldedBlocks, markdownBlocks, markdownInline } from "./markdown";

// The work counter counts the characters the string and regexp primitives may scan, so a test tells linear from
// quadratic growth without a clock.
const originals = {
  indexOf: String.prototype.indexOf,
  slice: String.prototype.slice,
  charAt: String.prototype.charAt,
  exec: RegExp.prototype.exec,
};
let work = 0;

function count(): void {
  const { indexOf, slice, charAt, exec } = originals;
  String.prototype.charAt = function (this: string, i: number) {
    work++;
    return charAt.call(this, i);
  };
  String.prototype.indexOf = function (this: string, x: string, from?: number) {
    const r = indexOf.call(this, x, from);
    work += (r < 0 ? this.length : r) - (from || 0) + 1;
    return r;
  };
  String.prototype.slice = function (this: string, a?: number, b?: number) {
    const r = slice.call(this, a, b);
    work += r.length + 1;
    return r;
  };
  RegExp.prototype.exec = function (this: RegExp, s: string) {
    const from = this.sticky || this.global ? this.lastIndex : 0;
    const m = exec.call(this, s);
    work += (this.sticky && m ? m[0].length : String(s).length - from) + 1;
    return m;
  };
}

function restore(): void {
  Object.assign(String.prototype, { indexOf: originals.indexOf, slice: originals.slice, charAt: originals.charAt });
  RegExp.prototype.exec = originals.exec;
}

function workOf(text: string): number {
  work = 0;
  count();
  try {
    for (const b of markdownBlocks(text)) if (b.kind === "p") markdownInline(b.text, {});
  } finally {
    restore();
  }
  return work;
}

afterEach(restore);

describe("markdown", () => {
  test("splits blocks and folds them without parsing what stays hidden", () => {
    const blocks = markdownBlocks("a\n\nb\n\nc");
    expect(blocks.map((b) => b.kind)).toEqual(["p", "p", "p"]);
    expect(foldedBlocks(blocks, 2)).toEqual({ shown: blocks.slice(0, 2), folded: true });
    expect(foldedBlocks(blocks, 3)).toEqual({ shown: blocks, folded: false });
    expect(foldedBlocks(blocks, 0)).toEqual({ shown: blocks, folded: false });
  });

  test("an unsafe link keeps its label as text", () => {
    expect(markdownInline("[a *b*](http://x.test)")).toEqual(["a ", { tag: "em", children: ["b"] }]);
  });

  // Each shape is a unit repeated n times, a middle, then a tail unit repeated n times.
  const shapes: Record<string, [string, string, string]> = {
    "lone stars": ["*a ", "", ""],
    "lone underscores": [" _a", "", ""],
    "lone doubles": ["**a ", "", ""],
    globs: ["*.go ", "", ""],
    "open brackets": ["[", "", ""],
    "open links": ["[a](b", "", ""],
    labels: ["[a] ", "", ""],
    "bare h": ["h", "", ""],
    backticks: ["` a ", "", ""],
    "nested emphasis": ["*a **b _c ", "", ""],
    "openers sharing a url": ["[", "a](", "x"],
    "open mentions": ["@{a", "", ""],
    "mentions ending late": ["@{", "a", "}"],
  };
  const build = ([unit, middle, tail]: [string, string, string], n: number) => unit.repeat(n) + middle + tail.repeat(n);

  test.each(Object.entries(shapes))("inline parsing of %s is linear", (_, shape) => {
    const w2 = workOf(build(shape, 2000));
    const w4 = workOf(build(shape, 4000));
    expect(w2).toBeGreaterThan(0);
    expect(w4).toBeLessThanOrEqual(2.5 * w2);
  });
});
