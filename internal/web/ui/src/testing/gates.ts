import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { I18N } from "../lib/i18n.svelte";
import type { J } from "../lib/util";
import { net } from "../state/api";

// Fixtures of the gates and drafts the daemon serves, as the Go tests of the old cockpit built them, and a
// recorder of what the page posts.

export interface Post {
  path: string;
  body: J;
}

const realFetch = net.fetch;

// recordPosts makes every request a post the page records, answered by answer (an Error makes it fail).
export function recordPosts(answer: (path: string) => J = () => ({})): Post[] {
  const posts: Post[] = [];
  net.fetch = async (input: string, init?: RequestInit) => {
    posts.push({ path: input, body: init && init.body ? JSON.parse(String(init.body)) : undefined });
    const out = answer(input);
    if (out instanceof Error) return new Response(JSON.stringify({ error: out.message }), { status: 500 });
    return new Response(JSON.stringify(out), { status: 200 });
  };
  return posts;
}

export function restoreFetch(): void {
  net.fetch = realFetch;
}

// useStrings loads the profile's strings of locale into the page's catalog, from the repository's TOML.
export function useStrings(locale: "en" | "pt-BR"): void {
  const rel = locale === "en" ? "profiles/default/strings.toml" : `profiles/default/locales/${locale}/strings.toml`;
  const src = readFileSync(fileURLToPath(new URL("../../../../../" + rel, import.meta.url)), "utf8");
  const strings: Record<string, string> = {};
  for (const line of src.split("\n")) {
    const m = /^("(?:[^"\\]|\\.)*")\s*=\s*("(?:[^"\\]|\\.)*")\s*$/.exec(line);
    if (m) strings[JSON.parse(m[1])] = JSON.parse(m[2]);
  }
  Object.assign(I18N, { locale, strings });
}

export function taskCatalog(): J {
  return {
    views: [{ id: "triage", label: "Triage", group: "task.category" }, { id: "parked", label: "Parked", group: "task.state" }, { id: "walk", label: "Weekly walk" }],
    verbs: [{ verb: "finish", label: "Finish", summary: "Close it" }, { verb: "reply", handler: "reply", label: "Reply", summary: "Hold a reply for approval" }],
    states: [{ name: "triage", label: "To sort", category: "inbox" }, { name: "doing", label: "Doing", category: "active" }],
    props: [{ name: "size", type: "enum", values: ["s", "m", "l"] }, { name: "owner", type: "actor" }],
  };
}

export const prUnit = { provider: "github", key: "acme/api#8" };

// questionGate is open question gate 3 of task t-1 whose message is text, with options, and whose intents are
// answer, which takes text, and dismiss.
export function questionGate(text: string, recommend = "", ...options: J[]): J {
  return {
    id: 3, task: "t-1", kind: "question", name: "question", status: "open", text: "The agent asks",
    subject: { id: 3, unit: prUnit, kind: "question", text, recommend, options },
    intents: [{ id: "answer", label: "Answer", answer: true }, { id: "dismiss", label: "Dismiss" }],
  };
}

// stepGate is an open review-diff step of task t-1 about its change acme/api#9.
export function stepGate(): J {
  return {
    id: 5, task: "t-1", kind: "step", name: "review-diff", status: "open", text: "Review the diff of acme/api#9, 3 files changed",
    units: [{ label: "acme/api#9", title: "Batch export", link: "https://forge.example/acme/api/changes/9" }],
    intents: [{ id: "approve", label: "Looks good", summary: "Mark the diff reviewed; posts nothing." },
      { id: "dismiss", label: "Dismiss", summary: "Close this step without reviewing; the task stays as it is." }],
  };
}

// planGate is open plan gate 5 of task t-1, which asks to run plan, none when the plan no longer holds it.
export function planGate(plan: J = null): J {
  return {
    id: 5, task: "t-1", kind: "plan", name: "plan", status: "open", text: "Run this?", subject: "fake-close/task/t-1/ab12",
    intents: [{ id: "run_now", label: "Run now" }, { id: "skip", label: "Skip" }, { id: "guide", label: "Guide", answer: true }], plan,
  };
}

export function closingPlan(summary: string): J {
  return {
    id: "fake-close/task/t-1/ab12", workflow: "fake-close", summary, decision: "pending", label: "Retry flaky uploads", link: "https://chat.example/c1",
    steps: ["items.resolve"], does: [{ use: "items.resolve", reach: "local" }],
    why: { trigger: "sweep", matched: [{ name: "task.category", value: "active" }, { name: "task.items_open", value: "2" }] },
  };
}

// quietClose is a pending item that waits for you on task t-06bec1c03a26fadf, resolving its two open items as moot.
export function quietClose(): J {
  return {
    id: "fake-close/task/t-06bec1c03a26fadf/ab", workflow: "fake-close", summary: "Close a quiet task; posts nothing", decision: "pending", default: "wait",
    reason: "waits for you", unit: { provider: "task", key: "t-06bec1c03a26fadf" }, label: "task:t-06bec1c03a26fadf", steps: ["items.resolve"],
    does: [{ use: "items.resolve", reach: "local", says: "Resolve its open items as {resolution}", args: { resolution: "moot", resolution_says: "resolved as moot" },
      items: ["Ana asks for the retry numbers", "Bea asks when it ships"] }],
    target: { task: "t-06bec1c03a26fadf", title: "Answer Ana about the retries", units: [{ label: "#general", link: "https://chat.example/c1" }, { label: "#ops" }] },
    why: { trigger: "sweep", matched: [{ name: "task.items_open", value: "2" }] },
    actions: [],
  };
}

// pendingDraft is a held nudge on task t-1, from workflow fake-nudge, whose gate 12 approves or discards it.
export function pendingDraft(): J {
  return {
    id: 7, unit: "github/acme/api#8", task: "t-1", kind: "nudge", purpose: "nudge", target: "acme/api#8", where: "acme/api#8",
    text: "Any news on the retry change?", why: "No answer since Tuesday", language: "pt-BR", effects: ["task.nudged"], state: "held",
    permalink: "https://example.test/thread", origin: { kind: "workflow", workflow: "fake-nudge" }, gate: 12,
    intents: { approved: "approve", rejected: "discard" }, rev: 3, actions: [],
  };
}

const leaf = (pointer: string, label: string, value: string): J => ({ pointer, label, value });

// reviewDraft is a held review draft of acme/api#7: a verdict a person may change, and two inline comments.
export function reviewDraft(): J {
  return {
    ...pendingDraft(), unit: "github/acme/api#7", kind: "review", purpose: "", delivery: "review", where: "acme/api#7",
    text: "Two findings block the merge.", effects: [], basis: "4f9c2d1",
    payload: { event: "REQUEST_CHANGES", comments: [{ path: "api/handler.go", line: 42, side: "RIGHT", body: "Return the error." },
      { path: "api/retry.go", start_line: 50, line: 58, body: "Back off\nbetween tries." }] },
    fields: [
      { pointer: "/event", label: "Verdict", value: "REQUEST_CHANGES", shown: "Request changes", editable: true,
        options: [{ value: "APPROVE", label: "Approve" }, { value: "REQUEST_CHANGES", label: "Request changes" }, { value: "COMMENT", label: "Comment" }] },
      { pointer: "/comments", label: "Inline comments", fields: [
        { pointer: "/comments/1", label: "1", fields: [leaf("/comments/0/path", "File", "api/handler.go"), leaf("/comments/0/line", "Line", "42"),
          leaf("/comments/0/side", "Side", "RIGHT"), leaf("/comments/0/body", "Comment", "Return the error.")] },
        { pointer: "/comments/2", label: "2", fields: [leaf("/comments/1/path", "File", "api/retry.go"), leaf("/comments/1/start_line", "start_line", "50"),
          leaf("/comments/1/line", "Line", "58"), leaf("/comments/1/body", "Comment", "Back off\nbetween tries.")] },
      ] },
    ],
  };
}

// leaves are the scalar values of fields, each as its card must show it.
export function leaves(fields: J[]): string[] {
  return fields.flatMap((f: J) => ((f.fields || []).length ? leaves(f.fields) : [f.value]));
}

// draftGate is the gate the daemon serves a draft as on its own task.
export function draftGate(d: J): J {
  return { id: d.gate, kind: "draft", delivery: d.state, status: "open", intents: [], subject: d };
}

// ownDraftTask is the draft's own task draft:<id>, offering verbs.
export function ownDraftTask(d: J, verbs: J[]): J {
  return { id: "draft:" + d.id, title: "Nudge the reviewer?", category: "inbox", units: [prUnit], gates: [draftGate(d)], verbs };
}
