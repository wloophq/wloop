import type { Unit } from "./routes";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type J = any;

export const unitKey = (u: Unit): string => `${u.provider}:${u.key}`;
export const unitOf = (x: J): Unit => x.unit;
export const keyOf = (x: J): string => unitKey(unitOf(x));
export const labelOf = (x: J): string => (x && x.label) || "";
export const refLabel = (ref: { owner: string; repo: string; number: string | number }): string => `${ref.owner}/${ref.repo}#${ref.number}`;

export function isSet(iso: string | null | undefined): boolean {
  return !!iso && !iso.startsWith("0001-01-01");
}

export function safeURL(u: unknown): string | undefined {
  return typeof u === "string" && u.startsWith("https://") ? u : undefined;
}

// fold lowers s and drops its accents as api.Fold does, so the lens, the daemon's counts and the terminal
// compare text the same way.
export function fold(s: unknown): string {
  return String(s || "").toLowerCase().replace(/[áàâãäéèêëíìîïóòôõöúùûüçñ]/g, (c) => c.normalize("NFD")[0]).replace(/\p{Mn}/gu, "");
}

// tokens splits a query on spaces, keeping a "quoted value" whole.
export function tokens(q: unknown): string[] {
  return String(q || "").match(/[^\s"]*"[^"]*"?|[^\s"]+/g) || [];
}

export function unquote(s: string): string {
  return s.replace(/^"|"$/g, "");
}

export function cmp<T>(a: T, b: T): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

// goDuration reads a Go duration such as "72h", "-24h" or "1h30m" in ms.
export function goDuration(s: unknown): number {
  const m = /^([-+]?)((?:\d+(?:\.\d*)?(?:ns|us|µs|ms|s|m|h))+)$/.exec(String(s || ""));
  if (!m) return NaN;
  const unit: Record<string, number> = { ns: 1e-6, us: 1e-3, "µs": 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };
  let ms = 0;
  for (const [, n, u] of m[2].matchAll(/(\d+(?:\.\d*)?)(ns|us|µs|ms|s|m|h)/g)) ms += Number(n) * unit[u];
  return m[1] === "-" ? -ms : ms;
}

// rfc3339 says whether s is an RFC 3339 instant, as Go's lens parses them.
export function rfc3339(s: unknown): boolean {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(String(s));
}

// typing says whether el takes typed text, where single-letter keys belong to it.
export function typing(el: Element | null | undefined): boolean {
  if (el instanceof HTMLInputElement) return !["checkbox", "radio", "button", "submit", "reset"].includes(el.type);
  return el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement || !!(el && (el as HTMLElement).isContentEditable);
}

// deferred is a promise with its resolve and reject.
export interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
}

export function deferred<T>(): Deferred<T> {
  const d = {} as Deferred<T>;
  d.promise = new Promise<T>((resolve, reject) => Object.assign(d, { resolve, reject }));
  d.promise.catch(() => {});
  return d;
}

export function escapeHTML(s: unknown): string {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] as string);
}
