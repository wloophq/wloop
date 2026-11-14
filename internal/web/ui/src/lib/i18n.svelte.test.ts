import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { goFixtures } from "../testing/fixtures";
import { resetStore } from "../testing/reset.svelte";
import { fmtDate, fmtNumber, followStrings, I18N, loadStrings, t, tn } from "./i18n.svelte";

// Ported from i18n_test.go; the plural rules Go applies come from cockpit.fixtures.json.

beforeEach(() => resetStore());
afterEach(() => vi.useRealTimers());

describe("i18n", () => {
  test("t falls back to the key", () => {
    expect(t("Open cockpit")).toBe("Open cockpit");
    expect(t("{n} of {total}", { n: 2, total: 5 })).toBe("2 of 5");
    expect(t("{n} of {total}", { n: 2 })).toBe("2 of {total}");
    expect(tn("{n} drafts", 3)).toBe("3 drafts");
  });

  test("t and tn in pt-BR", () => {
    I18N.locale = "pt-BR";
    I18N.strings = { "Open cockpit": "Abrir o cockpit", "{n} drafts": { one: "{n} rascunho", other: "{n} rascunhos" }, "{n} items": { other: "{n} itens" } };
    expect(t("Open cockpit")).toBe("Abrir o cockpit");
    expect(tn("{n} drafts", 0)).toBe("0 rascunho");
    expect(tn("{n} drafts", 1)).toBe("1 rascunho");
    expect(tn("{n} drafts", 1234)).toBe("1.234 rascunhos");
    expect(t("{n} drafts")).toBe("{n} rascunhos");
    expect(tn("{n} tasks", 2)).toBe("2 tasks");
    expect(tn("{n} items", 1)).toBe("1 itens");
  });

  test("dates and numbers follow the locale", () => {
    const iso = "2026-11-07T21:05:00Z";
    const tz = process.env.TZ;
    process.env.TZ = "UTC";
    try {
      const at = (opts: string) => fmtDate(iso, opts).replace(/ | /g, " ");
      I18N.locale = "pt-BR";
      expect(at("date")).toBe("07/11");
      expect(at("time")).toBe("21:05");
      expect(at("datetime")).toContain("07/11");
      expect(at("datetime")).toContain("21:05");
      expect(at("long")).toContain("2026");
      expect(at("long")).toContain("nov");
      expect(fmtNumber(1234.5)).toBe("1.234,5");
      expect(fmtDate("bad", "date")).toBe("");
      I18N.locale = "en-US";
      expect(at("date")).toBe("11/07");
      expect(at("time")).toContain("09:05");
      expect(at("time")).toContain("PM");
      expect(fmtNumber(1234.5)).toBe("1,234.5");
    } finally {
      process.env.TZ = tz;
    }
  });

  test("plurals agree with Go", () => {
    const { counts, locales, texts, categories } = goFixtures().plurals;
    for (const l of locales as string[]) {
      I18N.locale = l;
      I18N.strings = { k: { one: "one", other: "other" } };
      (counts as number[]).forEach((n, i) => {
        expect(tn("k", n), `${l} ${n}`).toBe(texts[l][i]);
        // Intl's CLDR is newer than x/text's: it names millions "many" in pt and fr, which a text without a
        // many form reads as other in both.
        if (n < 1000000) expect(new Intl.PluralRules(l).select(n), `${l} ${n}`).toBe(categories[l][i]);
      });
    }
  });

  test("the boot renders in English when the strings fail, and applies them when they arrive late", async () => {
    vi.useFakeTimers();
    const boot = async (get: () => Promise<unknown>, budget: number) => {
      const p = loadStrings(get, budget);
      await vi.advanceTimersByTimeAsync(budget);
      await p;
    };
    await boot(() => Promise.reject(new Error("HTTP 500")), 10);
    expect(I18N.locale).toBe("en");
    expect(t("Open cockpit")).toBe("Open cockpit");
    let release!: (v: unknown) => void;
    const slow = new Promise((r) => (release = r));
    await boot(() => slow, 10);
    expect(t("Open cockpit")).toBe("Open cockpit");
    release({ locale: "pt-BR", strings: { "Open cockpit": "Abrir o cockpit" } });
    await vi.advanceTimersByTimeAsync(20);
    expect(t("Open cockpit")).toBe("Abrir o cockpit");
    await boot(async () => ({ locale: "not a locale!!", strings: { "Open cockpit": "x" } }), 10);
    expect(I18N.locale).toBe("pt-BR");
    const inTime = loadStrings(async () => ({ locale: "en", strings: {} }), 1000);
    await inTime;
    expect(I18N.locale).toBe("en");
  });

  test("a label that read the strings rewords when they change", async () => {
    const seen: string[] = [];
    const stop = $effect.root(() => {
      $effect(() => {
        seen.push(tn("{n} drafts", 2));
      });
    });
    flushSync();
    await followStrings("s1", async () => ({ locale: "pt-BR", stamp: "s1", strings: { "{n} drafts": { one: "{n} rascunho", other: "{n} rascunhos" } } }));
    flushSync();
    expect(seen).toEqual(["2 drafts", "2 rascunhos"]);
    await followStrings("s1", async () => {
      throw new Error("read again");
    });
    stop();
  });

  test("an answer whose stamp is not the latest health's is dropped", async () => {
    let release!: (v: unknown) => void;
    const first = followStrings("s1", () => new Promise((r) => (release = r)));
    await followStrings("s2", async () => ({ locale: "fr", stamp: "s2", strings: {} }));
    release({ locale: "pt-BR", stamp: "s1", strings: {} });
    await first;
    expect(I18N.locale).toBe("fr");
    expect(I18N.stamp).toBe("s2");
  });
});
