import { beforeEach, expect, test } from "vitest";
import { resetStore } from "../testing/reset.svelte";
import { APIError } from "./api";
import { unitFallback } from "./loaders.svelte";

beforeEach(() => resetStore(Date.parse("2026-11-07T12:00:00Z"), "#view=all"));

test("a unit with no detail opens the first open task that holds it, only when the read says it is gone", () => {
  const gone = new APIError("session:0b9c7d1e is not in the inventory", 404);
  expect(unitFallback(gone, [{ id: "t-01" }, { id: "t-02" }])).toBe("#view=all&task=t-01");
  expect(unitFallback(gone, [])).toBe("");
  expect(unitFallback(new APIError("boom", 500), [{ id: "t-01" }])).toBe("");
});
