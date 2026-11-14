import { applyCatalog } from "../lib/catalog";
import { fieldOf } from "../lib/lens";
import type { J } from "../lib/util";
import { net } from "../state/api";
import { nav, win } from "../state/route.svelte";
import { catalog, state, tasksState } from "../state/store.svelte";
import { readFileSync } from "node:fs";

// fixtures are what the Go tests of the chrome fed the old scripts, dumped from them once: the served catalog,
// the bundled views, rows as the daemon serves them and the wire fixtures of the actions.
// fixtures is internal/web/testdata/cockpit.chrome.json, which TestTheChromeFixturesAreWhatGoServes keeps current.
export const fixtures: J = JSON.parse(readFileSync("../testdata/cockpit.chrome.json", "utf8"));

export const clone = <T>(v: T): T => structuredClone(v);

// useCatalog applies the served GitHub catalog, the bundled views and, with slack, Slack's facets beside it.
export function useCatalog({ slack = false, views = true }: { slack?: boolean; views?: boolean } = {}): void {
  applyCatalog(clone(fixtures.catalog), fieldOf);
  if (slack) catalog.fields = [...catalog.fields, ...clone(fixtures.slackFacets).map(fieldOf)];
  if (views) tasksState.catalog = clone(fixtures.views);
}

export function useRows(rows: J[]): void {
  state.rows = clone(rows);
  state.loaded = true;
}

export function useSpecs(s: J): void {
  state.specs = { version: s.version, byId: Object.fromEntries(s.specs.map((x: J) => [x.id, x])) };
}

export interface Call {
  url: string;
  method: string;
  body: J;
}

// answer replaces the network with reply, by URL, and records each call.
export function answer(reply: (url: string, body: J) => J | Promise<J>, status = 200): Call[] {
  const calls: Call[] = [];
  net.fetch = async (url: string, init?: RequestInit) => {
    const body = init && init.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ url, method: (init && init.method) || "GET", body });
    const out = await reply(url, body);
    return new Response(JSON.stringify(out), { status, headers: { "Content-Type": "application/json" } });
  };
  return calls;
}

// settle lets the promises the page started run out.
export { settle } from "./settle";

// followHashes makes writeHash move nav as the page's hashchange would, and returns every hash written.
export function followHashes(): string[] {
  const written: string[] = [];
  const loc = {
    get hash() {
      return nav.hash;
    },
    set hash(h: string) {
      const v = h.startsWith("#") ? h : "#" + h;
      written.push(v);
      nav.hash = v;
    },
  } as unknown as Location;
  win.location = () => loc;
  win.replaceState = (h: string) => {
    written.push(h);
  };
  return written;
}

// query is the q a written hash carries.
export const queryOf = (hash: string): string => new URLSearchParams(hash.replace(/^#/, "")).get("q") || "";
