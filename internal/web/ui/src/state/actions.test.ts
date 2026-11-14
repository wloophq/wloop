import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { J } from "../lib/util";
import { resetStore } from "../testing/reset.svelte";
import { act, answerSent, claimCopy, clipboard, forgetAnswers, questionShown, scheduler } from "./actions.svelte";
import { apiError, net } from "./api";
import { FakeDaemon, Reply } from "../testing/fakedaemon";
import { testdata } from "../testing/fixtures";
import { overlays } from "./overlays.svelte";
import { answered, drafts, notice, state } from "./store.svelte";

// The actions, ported from board_test.go: what act posts, what an action that copies writes, and when a typed
// guidance goes along.

class StubItem {
  constructor(public parts: Record<string, Promise<Blob>>) {}
}

let daemon: FakeDaemon;
let writes: { text: string; html?: string }[];
let refuse: string;
let order: string[];

beforeEach(() => {
  resetStore();
  daemon = new FakeDaemon();
  daemon.fallback = () => ({ ok: true, output: [] });
  writes = [];
  refuse = "";
  order = [];
  vi.stubGlobal("ClipboardItem", StubItem);
  clipboard.canWriteItems = () => true;
  clipboard.write = async (items) => {
    order.push("claim");
    const parts = (items[0] as unknown as StubItem).parts;
    const text = await (await parts["text/plain"]).text();
    const html = await (await parts["text/html"]).text();
    if (refuse) throw new Error(refuse);
    writes.push({ text, html });
  };
  clipboard.writeText = async (text) => {
    writes.push({ text });
  };
  scheduler.load = () => {};
});

afterEach(() => vi.unstubAllGlobals());

// drive runs p, answering each overlay it asks with the next of answers.
async function drive<T>(p: Promise<T>, answers: unknown[] = []): Promise<T> {
  let done = false;
  const out = p.finally(() => (done = true));
  while (!done) {
    await new Promise((r) => setTimeout(r, 0));
    const o = overlays.stack[0];
    if (!o) continue;
    order.push(o.kind === "confirm" ? "confirm " + o.text : "input " + (o as J).label);
    (o.finish as (v: unknown) => void)(answers.shift());
  }
  return out;
}

const copyRow = (confirm = "") => ({ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" }, actions: [{ id: "copy_review_request", enabled: true, confirm }] });

describe("an action that copies", () => {
  test("puts its copy on the clipboard, and a refused write shows the text to copy by hand", async () => {
    state.specs.byId.copy_review_request = { id: "copy_review_request", copies: true };
    const notices: string[] = [];
    daemon.fallback = () => ({ ok: true, copy: { text: "review, pls? `x`", html: "review, pls? <code>x</code>" }, output: ["Copied the review request for acme/api#7; paste it in a chat."] });
    await act(copyRow(), "copy_review_request");
    notices.push(notice.text);
    daemon.fallback = () => ({ ok: true, copy: { text: "a & b" }, output: ["copied a & b"] });
    await act(copyRow(), "copy_review_request");
    notices.push(notice.text);
    refuse = "denied";
    await act(copyRow(), "copy_review_request");
    notices.push(notice.text);
    expect(writes).toEqual([{ text: "review, pls? `x`", html: "review, pls? <code>x</code>" }, { text: "a & b", html: "a &amp; b" }]);
    expect(notices).toEqual(["Copied the review request for acme/api#7; paste it in a chat.", "copied a & b", ""]);
    expect(state.actionError).toBe("Could not copy: denied");
    expect(state.actionDetail).toBe("a & b");
  });

  test("act resolves a claim handed in whatever its specs", async () => {
    daemon.fallback = () => ({ ok: true, copy: { text: "review, pls?" }, output: ["copied"] });
    const claim = claimCopy();
    let resolved = "";
    const resolve = claim.pending.resolve;
    claim.pending.resolve = (copy) => {
      resolved = copy.text;
      resolve(copy);
    };
    await act(copyRow(), "copy_review_request", { claim });
    expect(resolved).toBe("review, pls?");
    expect(writes).toHaveLength(1);
  });

  test("claims the clipboard before it asks its confirm; a declined confirm writes nothing", async () => {
    state.specs.byId.copy_review_request = { id: "copy_review_request", copies: true };
    daemon.fallback = () => ({ ok: true, copy: { text: "review, pls?" }, output: ["copied"] });
    await drive(act(copyRow("Copy it?"), "copy_review_request"), [true]);
    await drive(act(copyRow("Copy it?"), "copy_review_request"), [false]);
    expect(order).toEqual(["claim", "confirm Copy it?", "claim", "confirm Copy it?"]);
    expect(writes).toHaveLength(1);
  });

  test("a failed action writes nothing", async () => {
    state.specs.byId.copy_review_request = { id: "copy_review_request", copies: true };
    daemon.fallback = () => new Reply(409, { error: "copy_review_request is not offered" });
    await act(copyRow(), "copy_review_request");
    expect(writes).toEqual([]);
    expect(state.actionError).toContain("not offered");
  });
});

describe("act", () => {
  test("posts the basis, the source and only what the action takes", async () => {
    const specs = testdata("daemon/testdata/wire/specs.json");
    const rows = testdata("daemon/testdata/wire/actions.json");
    state.specs = { version: specs.version, byId: Object.fromEntries(specs.specs.map((x: J) => [x.id, x])) };
    const long = "x".repeat(501);
    const cases: { fixture: string; id: string; guidance?: string; prompt?: string; confirm?: boolean; url?: string; body?: J; error?: string }[] = [
      { fixture: "pending", id: "run_now", guidance: "go on", url: "/api/units/github/acme%2Fapi%237/run_now", body: { source: "cockpit", basis: "pending:2026-10-08T10:00:00Z", guidance: "go on" } },
      { fixture: "held", id: "run_now", confirm: true, url: "/api/units/github/acme%2Fapi%237/run_now", body: { source: "cockpit", basis: "held:automatic round held: daily cap" } },
      { fixture: "pending", id: "mute", prompt: "36h", url: "/api/units/github/acme%2Fapi%237/mute", body: { source: "cockpit", until: "36h" } },
      { fixture: "pending", id: "mute", prompt: "state", url: "/api/units/github/acme%2Fapi%237/mute", body: { source: "cockpit", until_state: true } },
      { fixture: "pending", id: "guide", error: "guide acme/api#7: Guide is required" },
      { fixture: "mine_no_reviewer", id: "nudge", guidance: long, confirm: true, error: "nudge acme/api#10: guidance is 501 characters, the limit is 500" },
      { fixture: "mine_no_reviewer", id: "nudge", guidance: "ask about CI", confirm: true, url: "/api/units/github/acme%2Fapi%2310/nudge", body: { source: "cockpit", guidance: "ask about CI" } },
      { fixture: "forceable_refusal", id: "remove_force", confirm: true, url: "/api/units/github/acme%2Fapi%237/remove_force", body: { source: "cockpit" } },
    ];
    for (const c of cases) {
      daemon.clear();
      state.actionError = "";
      const answers = [c.confirm, c.prompt ?? null];
      const row = rows[c.fixture];
      const st = (row.actions || []).find((a: J) => a.id === c.id);
      if (!(st && st.confirm)) answers.shift();
      await drive(act(row, c.id, { guidance: c.guidance }), answers);
      const where = `${c.fixture} ${c.id}`;
      if (c.error) {
        expect(daemon.calls, where).toHaveLength(0);
        expect(state.actionError, where).toContain(c.error);
      } else {
        expect(daemon.calls.map((x) => x.url), where).toEqual([c.url]);
        expect(daemon.calls[0].body, where).toEqual(c.body);
        expect(state.actionError, where).toBe("");
      }
    }
  });

  test("only a text input or an intake takes the guidance, and sending it clears the box", async () => {
    state.specs = { version: "v1", byId: Object.fromEntries([
      { id: "mute", input: { kind: "duration", default: "2d" } },
      { id: "guide", input: { kind: "text" } },
      { id: "review", intake: true },
      { id: "hold" },
    ].map((x) => [x.id, x])) };
    const row = { label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, actions: [] };
    for (const [name, sent] of [["mute", false], ["hold", false], ["guide", true], ["review", true]] as [string, boolean][]) {
      drafts.guidance["github:a/b#1"] = "look at the retry";
      daemon.clear();
      await drive(act(row, name, { guidance: "look at the retry" }), ["2d"]);
      expect(daemon.calls[0].body.guidance === "look at the retry", name).toBe(sent);
      expect(!drafts.guidance["github:a/b#1"], name).toBe(sent);
    }
  });

  test("an accepted answer keeps the question's controls off until the row shows another question", async () => {
    state.specs.byId.answer = { id: "answer", input: { kind: "answer" } };
    const row = { label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, actions: [{ id: "answer", enabled: true }], question: { record_id: "r1", state: "open", queued: false } };
    await act(row, "answer", { option_id: "b" });
    expect(daemon.calls[0].body).toEqual({ source: "cockpit", option_id: "b" });
    expect(answerSent(row)).toBe(true);
    expect(answered.get("github:a/b#1")).toBe(questionShown(row));
    const next = { ...row, question: { record_id: "r2", state: "open", queued: false } };
    expect(answerSent(next)).toBe(false);
    expect(answered.has("github:a/b#1")).toBe(true);
    forgetAnswers([next]);
    expect(answered.has("github:a/b#1")).toBe(false);
  });
});

describe("apiError", () => {
  test("an out-of-date page is told to reload", () => {
    const out = { error: "API version 3 is not this daemon's 4", fix: "launchctl kickstart -k gui/501/x" };
    const old = apiError(out, "HTTP 426", 426);
    expect(old).toContain("reload this page");
    expect(old).not.toContain("kickstart");
    expect(apiError(out, "HTTP 503", 503)).toContain("run: launchctl kickstart");
  });
});

test("an answer is kept against the question the person answered, though a sync moves the row while it is on its way", async () => {
  const { answered: kept } = await import("./store.svelte");
  const row: any = { unit: { provider: "acme", key: "web#7" }, label: "acme/web#7", actions: [{ id: "answer", enabled: true }], question: { record_id: "q1", state: "asked", queued: false } };
  let release!: () => void;
  net.fetch = () => new Promise<Response>((r) => (release = () => r(new Response("{}", { status: 200 }))));
  const sent = act(row, "answer", { option_id: "a" });
  await Promise.resolve();
  row.question = { record_id: "q1", state: "asked", queued: true };
  release();
  await sent;
  expect(kept.get("acme:web#7")).toBe("q1:asked:false");
});
