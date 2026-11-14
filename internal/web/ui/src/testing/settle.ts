import { flushSync } from "svelte";

// settle lets every read, answer and render already set off run to its end: timers queued at 0 ms and the
// promises each of them resolves, then a flush of the page.
export async function settle(rounds = 20): Promise<void> {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
  flushSync();
}
