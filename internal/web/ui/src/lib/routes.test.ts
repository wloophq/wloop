import { beforeEach, describe, expect, test } from "vitest";
import { goFixtures, viewsCatalog } from "../testing/fixtures";
import { API_VERSION } from "../state/api";
import { queueKey } from "../state/loaders.svelte";
import { hashString, nav, readHash } from "../state/route.svelte";
import { catalog, tasksState } from "../state/store.svelte";
import { resetStore } from "../testing/reset.svelte";
import { readRoute } from "./routes";

// Ported from routes_test.go and catalog_test.go's route checks.

beforeEach(() => resetStore());

describe("routes", () => {
  test("a task hash opens the task; the old entry= hash does not", () => {
    tasksState.catalog = viewsCatalog();
    nav.hash = "#view=fits-now&entry=e-1";
    expect(readHash().task).toBe("");
    nav.hash = "#view=fits-now&task=e-1";
    const r = readHash();
    expect([r.view, r.task, r.pr]).toEqual(["fits-now", "e-1", ""]);
    expect(hashString({ ...r, task: "" })).toBe("view=fits-now");
  });

  test("readRoute reads the panel", () => {
    const p = readRoute("#panel=omnibox&q=abc");
    expect([p.panel, p.q, readRoute("#view=inbox").panel]).toEqual(["omnibox", "abc", ""]);
  });

  test("hash writes keep the queue", () => {
    tasksState.catalog = viewsCatalog();
    for (const [from, change] of [["#queue=list&ids=t-1,t-2", { task: "t-1", pr: "" }], ["#queue=answer", { task: "t-3", pr: "" }],
      ["#queue=send-draft&task=t-3", { task: "" }], ["#queue=list&ids=t-1,t-2&task=t-1", { q: "x" }]] as [string, object][]) {
      nav.hash = from;
      const before = queueKey(readRoute(nav.hash));
      nav.hash = "#" + hashString({ ...readHash(), ...change });
      expect(queueKey(readRoute(nav.hash)), `${from} became ${nav.hash}`).toBe(before);
    }
  });

  test("a route's path form opens its task", () => {
    expect(readRoute("#task/draft:1606").task).toBe("draft:1606");
    expect(readRoute("#task=draft:1606").task).toBe("draft:1606");
    expect(readRoute("#unit/github/acme%2Fapi%237").unit).toBe("github:acme/api#7");
    expect(readRoute("#view/outbox").view).toBe("outbox");
  });

  test("a deep-linked view survives until the catalog arrives", () => {
    nav.hash = "#view=stalled&q=x";
    expect(readHash().view).toBe("stalled");
    catalog.views = [{ id: "needs-me" }, { id: "stalled" }];
    expect(readHash().view).toBe("stalled");
    nav.hash = "#view=nope";
    expect(readHash().view).toBe("needs-me");
    nav.hash = "";
    expect(readHash().view).toBe("home");
  });

  test("the page speaks the daemon's API version", () => {
    expect(API_VERSION).toBe(goFixtures().apiVersion);
  });
});
