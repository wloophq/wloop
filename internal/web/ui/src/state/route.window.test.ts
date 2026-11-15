import { afterEach, beforeEach, expect, test } from "vitest";
import { resetStore } from "../testing/reset.svelte";
import { followHash, nav, readHash, writeHash } from "./route.svelte";

let stop = () => {};
beforeEach(() => {
  resetStore();
  history.replaceState(null, "", "#view=home&q=https://github.com/acme/web/pull/7");
  stop = followHash(window);
});
afterEach(() => stop());

test("a route written and then written in place in the same tick keeps both, as the browser has not reported the first yet", async () => {
  writeHash({ view: "explore", q: "", x: "acme/web#7" });
  expect(readHash().view).toBe("explore");
  writeHash({ x: "acme/web#7 is:open" }, true);
  await new Promise((r) => setTimeout(r, 0));
  expect(readHash()).toMatchObject({ view: "explore", x: "acme/web#7 is:open", q: "" });
  expect(nav.hash).toBe(location.hash);
});
