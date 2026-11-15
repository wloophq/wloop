import type { J } from "../lib/util";
import { net } from "../state/api";

export interface Call {
  url: string;
  init?: RequestInit;
  body?: J;
}

// FakeDaemon answers net.fetch from pages by URL, a function of the call, or a default for the reads every load
// makes; it records each call.
export class FakeDaemon {
  calls: Call[] = [];
  pages: Record<string, J | ((c: Call) => J)> = {};
  fallback: (c: Call) => J = () => {
    throw new Error("unexpected");
  };

  constructor(pages: Record<string, J | ((c: Call) => J)> = {}) {
    this.pages = pages;
    net.fetch = (url, init) => this.answer(url, init);
  }

  get urls(): string[] {
    return this.calls.map((c) => c.url);
  }

  clear(): void {
    this.calls.length = 0;
  }

  private async answer(url: string, init?: RequestInit): Promise<Response> {
    const call: Call = { url, init, body: init && typeof init.body === "string" ? JSON.parse(init.body) : undefined };
    this.calls.push(call);
    let page = Object.hasOwn(this.pages, url) ? this.pages[url] : undefined;
    let status = 200;
    try {
      if (page === undefined) page = this.fallback(call);
      else if (typeof page === "function") page = page(call);
    } catch (err) {
      page = { error: (err as Error).message + " " + url };
      status = 404;
    }
    if (page instanceof Reply) {
      status = page.status;
      page = page.body;
    }
    const body = page;
    return { ok: status >= 200 && status < 300, status, json: async () => body } as Response;
  }
}

// Reply is a page served with a status other than 200.
export class Reply {
  constructor(public status: number, public body: J) {}
}
