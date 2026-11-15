import { brand } from "../brand";
import { t } from "../lib/i18n.svelte";
import type { J } from "../lib/util";

export const API_VERSION = "1";
export const REQUEST_TIMEOUT_MS = 15000;

// net is how the page reaches the daemon; tests replace fetch.
export const net = {
  fetch: (input: string, init?: RequestInit): Promise<Response> => fetch(input, init),
  // authLost is called when the daemon answers 401.
  authLost: () => {},
};

// apiError is the daemon's refusal with the command it names to get past it. A 426 means the daemon is newer
// than this page, which only a reload brings up to date.
export function apiError(out: J, fallback: string, status?: number): string {
  if (status === 426) return t("this page is older than {name}'s daemon; reload this page", { name: brand.name });
  const msg = (out && out.error) || fallback;
  return out && out.fix ? t("{error}; run: {fix}", { error: msg, fix: out.fix }) : msg;
}

export class APIError extends Error {
  status: number;
  detail: string;
  source: string;
  constructor(message: string, status: number, detail = "", source = "") {
    super(message);
    this.status = status;
    this.detail = detail;
    this.source = source;
  }
}

function timeout(): AbortSignal | undefined {
  return typeof AbortSignal !== "undefined" && "timeout" in AbortSignal ? AbortSignal.timeout(REQUEST_TIMEOUT_MS) : undefined;
}

export async function getJSON(url: string): Promise<J> {
  const res = await net.fetch(url, { headers: { Accept: "application/json", "X-Loop-API": API_VERSION }, signal: timeout() });
  if (res.status === 401) net.authLost();
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new APIError(apiError(body, `${url}: HTTP ${res.status}`, res.status), res.status);
  return body;
}

// postJSON posts body as JSON and resolves to the answer; a refusal, or an answer that carries an error,
// throws an APIError with the daemon's detail and source.
export async function postJSON(path: string, body: unknown): Promise<J> {
  const res = await net.fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
    body: JSON.stringify(body),
  });
  const out = await res.json().catch(() => ({}));
  if (!res.ok || out.error) throw new APIError(apiError(out, `HTTP ${res.status}`, res.status), res.status, out.detail || "", out.source || "");
  return out;
}
