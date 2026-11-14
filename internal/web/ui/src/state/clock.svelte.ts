import { untrack } from "svelte";

// Clock is the page's one notion of now, in ms: ages, buckets and countdowns derive from it, so the ticker that
// moves it is the only thing that redraws a time. minute moves only when now enters another minute, so what reads
// it (an age, a time bucket, the lens) redraws once a minute while a countdown redraws every second. Tests set
// clock.now and never read the wall clock.
class Clock {
  #now = $state(0);
  minute = $state(0);

  get now(): number {
    return this.#now;
  }

  set now(ms: number) {
    this.#now = ms;
    this.minute = Math.floor(ms / 60000) * 60000;
  }
}

export const clock = new Clock();

export const TICK_MS = 1000;

// startClock moves clock.now with the wall clock until the returned stop is called.
export function startClock(read: () => number = Date.now, every = TICK_MS): () => void {
  clock.now = read();
  const id = setInterval(() => {
    clock.now = read();
  }, every);
  return () => clearInterval(id);
}

// minute is clock.now to the minute.
export function minute(): number {
  return clock.minute;
}

// minuteNow is clock.now as of the last minute tick: exact, yet what reads it redraws once a minute.
export function minuteNow(): number {
  void clock.minute;
  return untrack(() => clock.now);
}
