import { flushSync } from "svelte";
import { expect, test } from "vitest";
import { clock, minuteNow } from "./clock.svelte";

test("what reads the minute redraws once a minute, a countdown every second", () => {
  clock.now = Date.parse("2026-11-07T12:00:00Z");
  let ages = 0;
  let countdowns = 0;
  const stop = $effect.root(() => {
    $effect(() => {
      void minuteNow();
      ages++;
    });
    $effect(() => {
      void clock.now;
      countdowns++;
    });
  });
  flushSync();
  for (let s = 1; s <= 61; s++) {
    clock.now += 1000;
    flushSync();
  }
  expect(countdowns).toBe(62);
  expect(ages).toBe(2);
  stop();
});
