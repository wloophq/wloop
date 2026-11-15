import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { I18N } from "../../lib/i18n.svelte";
import type { J } from "../../lib/util";
import { scheduler } from "../../state/actions.svelte";
import { gateState, tasksState } from "../../state/store.svelte";
import {
  closingPlan, planGate, questionGate, quietClose, recordPosts, restoreFetch, reviewDraft, stepGate, taskCatalog, useStrings,
} from "../../testing/gates";
import { resetStore } from "../../testing/reset.svelte";
import GateCard from "./GateCard.svelte";
import { gateAt, resolveDraft, resolveGate, resolving } from "./gates";
import { settle } from "../../testing/settle";

const task = { id: "t-1", title: "Retry flaky uploads" };

const all = (c: Element, sel: string) => [...c.querySelectorAll(sel)];
const texts = (c: Element, sel: string) => all(c, sel).map((x) => x.textContent || "");
const byFocus = (c: Element, f: string) => c.querySelector(`[data-focus="${f}"]`) as HTMLElement;
const card = (gate: J, t: J = task) => render(GateCard, { task: t, gate }).container;

beforeEach(() => {
  resetStore();
  tasksState.catalog = taskCatalog();
  resolving.clear();
  scheduler.load = vi.fn();
});
afterEach(() => restoreFetch());

describe("gate_test.go", () => {
  test("TestAStepGatesCardSaysWhatToReviewAndWhatEachAnswerDoes", () => {
    const c = card(stepGate());
    expect(texts(c, ".d-sub").join("")).toBe("Review the diff of acme/api#9, 3 files changed");
    const about = texts(c, ".gate-about");
    expect(about).toHaveLength(1);
    expect(about[0]).toContain("Batch export");
    expect(about[0]).toContain("acme/api#9");
    expect(all(c, "a").map((a) => a.getAttribute("href"))).toContain("https://forge.example/acme/api/changes/9");
    expect(texts(c, ".gate-says li")).toEqual(["Looks good: Mark the diff reviewed; posts nothing.", "Dismiss: Close this step without reviewing; the task stays as it is."]);
    expect(all(c, "button")).toHaveLength(2);

    const plain = card(questionGate("Which one?"));
    expect(all(plain, ".gate-about")).toHaveLength(0);
    expect(all(plain, ".gate-says li")).toHaveLength(0);
  });

  test("TestAQuestionGateRendersMarkdownAndOptionCards", () => {
    const c = card(questionGate("Use `retry()`", "b", { id: "a", label: "Retry", consequence: "Runs it again." }, { id: "b", label: "Revert" }));
    const opts = all(c, ".opt");
    expect(opts.map((o) => o.querySelector(".lb")!.textContent)).toEqual(["Revert", "Retry"]);
    expect(opts.map((o) => texts(o, ".rec").join(""))).toEqual(["recommended", ""]);
    expect(opts.map((o) => o.getAttribute("aria-pressed"))).toEqual(["true", "false"]);
    expect(opts.map((o) => all(o, ".cs").length + ":" + texts(o, ".cs").join("|"))).toEqual(["0:", "1:Runs it again."]);
    expect(texts(c, "code")).toEqual(["retry()"]);
    expect(texts(c, ".primary")).toEqual(["1 Answer"]);
    expect(all(c, "textarea")).toHaveLength(1);
  });

  test("TestAGateRendersAgentProseAsText", () => {
    const lines = Array.from({ length: 5000 }, (_, i) => "line " + "x".repeat(i % 7));
    const text = "<img src=x onerror=alert(1)> <b>hi</b> [go](javascript:alert(1))\n\n" + lines.join("\n\n");
    const md = card(questionGate(text)).querySelector(".md")!;
    expect(all(md, "img, b, script")).toHaveLength(0);
    expect(all(md, "a")).toHaveLength(0);
    expect(all(md, "p").length).toBeLessThanOrEqual(4);
    expect(texts(md, "button")).toEqual(["Read all"]);
  });

  test("TestAGateWithoutOptionsShowsOnlyTheAnswerAndIntents", () => {
    const bare = card(questionGate("Which one?"));
    expect(all(bare, ".opt")).toHaveLength(0);
    expect(all(bare, "textarea")).toHaveLength(1);
    expect(texts(bare, ".primary")).toEqual(["1 Answer"]);
    const unmarked = card(questionGate("Which one?", "", { id: "a", label: "Retry" }, { id: "b", label: "Revert" }));
    expect(all(unmarked, ".opt")).toHaveLength(2);
    expect(all(unmarked, '.opt[aria-pressed="true"]')).toHaveLength(0);
    expect(texts(unmarked, ".primary")).toEqual(["1 Answer"]);
  });

  test("TestPickingAnOptionCardThenAnswerSendsIt", async () => {
    const posts = recordPosts();
    const g = questionGate("Retry or revert?", "b", { id: "a", label: "Retry" }, { id: "b", label: "Revert" });
    let c = card(g);
    await fireEvent.click(byFocus(c, "gate:3:option:a"));
    await fireEvent.input(Object.assign(byFocus(c, "gate:3:text"), { value: "typed" }));
    await fireEvent.click(byFocus(c, "gate:3:answer"));
    await settle();
    expect(posts.splice(0)).toEqual([{ path: "/api/gates/3/answer", body: { source: "cockpit", args: { answer: "a\ntyped" } } }]);
    expect(gateState.answers.has("3")).toBe(false);

    c = card(g);
    await fireEvent.input(Object.assign(byFocus(c, "gate:3:text"), { value: "ship it" }));
    await fireEvent.keyDown(byFocus(c, "gate:3:text"), { key: "Enter", metaKey: true });
    await settle();
    expect(posts).toEqual([{ path: "/api/gates/3/answer", body: { source: "cockpit", args: { answer: "b\nship it" } } }]);
    expect(scheduler.load).toHaveBeenCalledWith({ tasks: true });
  });

  test("TestAGateShowsItsProfileIntentsAndDigitKeys", async () => {
    const posts = recordPosts();
    const e = { id: "t-0123456789abcdef", title: "Ship the retry change" };
    const plan = { id: 7, task: e.id, kind: "plan", name: "plan", status: "open", text: "Run this?", intents: [{ id: "run", label: "Run now" }, { id: "skip", label: "Skip" }] };
    const q = { ...questionGate("The agent asks"), id: 9, task: e.id };
    const p = card(plan, e);
    const c = card(q, e);
    expect(texts(p, "button")).toEqual(["1 Run now", "2 Skip"]);
    expect(texts(c, "button")).toEqual(["1 Answer", "2 Dismiss"]);
    expect(all(p, "button").map((b) => b.getAttribute("aria-keyshortcuts"))).toEqual(["1", "2"]);
    expect([all(p, "textarea").length, all(c, "textarea").length]).toEqual([0, 1]);

    // A digit key resolves the gate whose card holds the focus with the intent at that place.
    const sources = [{ task: e, gates: [plan, q] }];
    const at = gateAt(p.querySelector("button"), sources)!;
    await resolveGate(at.task, at.gate, at.gate.intents[1].id);
    expect(posts.splice(0).map((x) => x.path)).toEqual(["/api/gates/7/skip"]);

    await fireEvent.click(byFocus(c, "gate:9:answer"));
    await settle();
    expect(posts).toHaveLength(0);
    expect(c.querySelector(".act-result.err")!.textContent).toBe("Answer: write the answer first");

    await fireEvent.input(Object.assign(byFocus(c, "gate:9:text"), { value: "Take the timeout case first" }));
    await fireEvent.click(byFocus(c, "gate:9:answer"));
    await settle();
    expect(posts).toEqual([{ path: "/api/gates/9/answer", body: { source: "cockpit", args: { answer: "Take the timeout case first" } } }]);
  });

  test("TestAPlanGateSaysWhatItsPlanDoesOnWhatAndWhy", () => {
    tasksState.catalog = { ...taskCatalog(), facets: [{ id: "category", label: "Category", fact: "task.category", labels: { active: "Active" } }] };
    const full = card(planGate(closingPlan("Close the task once its asker checked the answer; posts nothing")));
    expect(full.querySelector(".d-sub")!.textContent).toBe("Run this?");
    expect(texts(full, ".plan-summary").join("")).toBe("Close the task once its asker checked the answer; posts nothing");
    const on = full.querySelector(".plan-on")!;
    expect(on.textContent).toContain("Retry flaky uploads");
    expect(all(on, "a").map((a) => a.getAttribute("href"))).toEqual(["https://chat.example/c1"]);
    const why = full.querySelector(".plan-why")!;
    expect(why.tagName).toBe("DETAILS");
    expect(texts(why, "li")).toEqual(["Category: Active", "task.items_open = 2"]);
    expect(all(full, ".plan-do .plan-do-what").map((x) => x.getAttribute("title"))).toEqual(["items.resolve"]);

    const bare = card(planGate(closingPlan("")));
    expect(all(bare, ".plan-summary")).toHaveLength(0);
    expect(all(bare, ".plan-do .plan-do-what").map((x) => x.getAttribute("title"))).toEqual(["items.resolve"]);

    const gone = card(planGate(null));
    expect(gone.querySelector(".d-sub")!.textContent).toBe("Run this?");
    expect(all(gone, ".plan-summary, .plan-on, .plan-why")).toHaveLength(0);
    expect(texts(gone, "button")).toEqual(["1 Run now", "2 Skip", "3 Guide"]);
  });

  test("TestAPlanGateDropsAnUnsafeLinkAndTitlesEachFact", () => {
    const p = closingPlan("Close it");
    p.link = "javascript:alert(1)";
    const c = card(planGate(p));
    expect(all(c, "a")).toHaveLength(0);
    expect(c.querySelector(".plan-on")!.textContent).toContain("Retry flaky uploads");
    expect(all(c, ".plan-why li").map((x) => x.getAttribute("title"))).toEqual(["task.category = active", "task.items_open = 2"]);
  });
});

describe("plan_page_test.go, the plan gate's pieces", () => {
  test("TestTheQueuesPlanGateShowsTheSameTargetAndItems", () => {
    useStrings("pt-BR");
    const c = card(planGate(quietClose()), { id: "t-06bec1c03a26fadf" });
    const links = all(c, "a").map((a) => [a.getAttribute("href") || "", a.textContent]);
    expect(links.some(([h, l]) => h.includes("task=t-06bec1c03a26fadf") && l === "Answer Ana about the retries")).toBe(true);
    expect(links).toContainEqual(["https://chat.example/c1", "#general"]);
    expect(c.textContent).toContain("#ops");
    expect(c.textContent).not.toContain("task:t-06bec1c03a26fadf");
    expect(texts(c, ".plan-do .plan-do-what")).toEqual(["Resolver os itens abertos como sem efeito"]);
    expect(texts(c, ".plan-does li.plan-do-item")).toEqual(["Ana asks for the retry numbers", "Bea asks when it ships"]);
    expect(texts(c, ".plan-posts").join("")).toBe("Não publica nada");
    const why = c.querySelector(".plan-why")!;
    expect(why.tagName).toBe("DETAILS");
    expect(texts(why, "li")).toHaveLength(1);
    expect(texts(why, "li")[0]).toContain("task.items_open");
  });

  test("TestThePlanGateCardSaysWhyItsItemWaits", () => {
    useStrings("pt-BR");
    const p = { ...quietClose(), reason: "observe mode", reason_key: "observe mode" };
    expect(texts(card(planGate(p), { id: "t-06bec1c03a26fadf" }), ".plan-reason")).toEqual(["o workflow só observa: nada roda até você aplicar"]);
  });
});

describe("precision_test.go", () => {
  test("TestResolvingACardDimsItUntilTheAnswer", async () => {
    let release!: () => void;
    const posts = recordPosts();
    const real = (await import("../../state/api")).net.fetch;
    const g = questionGate("Retry?");
    const c = card(g);
    const article = c.querySelector(".d-gate")!;
    (await import("../../state/api")).net.fetch = async (input, init) => {
      await new Promise<void>((r) => (release = r));
      return real(input, init);
    };
    const resolved = resolveGate({ id: "t-1" }, g, "dismiss");
    flushSync();
    expect(article.classList.contains("resolving")).toBe(true);
    expect(article.getAttribute("aria-busy")).toBe("true");
    release();
    await resolved;
    flushSync();
    expect(posts.map((p) => p.path)).toEqual(["/api/gates/3/dismiss"]);
    expect(article.classList.contains("resolving")).toBe(true);

    recordPosts(() => new Error("gone"));
    await resolveDraft({ id: 7, gate: 12, rev: 1, state: "held" }, "approve", "Approve", {});
    expect(resolving.has("draft:7")).toBe(false);
  });

  test("a resolved gate's card comes back once the daemon serves it otherwise", async () => {
    recordPosts();
    const g = $state(questionGate("Retry?"));
    const c = card(g);
    await resolveGate(task, g, "dismiss");
    flushSync();
    expect(c.querySelector(".d-gate")!.classList.contains("resolving")).toBe(true);
    g.status = "resolved";
    flushSync();
    expect(c.querySelector(".d-gate")!.classList.contains("resolving")).toBe(false);
  });
});

describe("qcard_test.go, the question's card", () => {
  test("TestATasksQuestionStandsForItsPinnedDrafts: each option shows the draft it approves", () => {
    const approve = { ...reviewDraft(), id: 42, gate: 142, pinned_by: 3 };
    const comment = { ...reviewDraft(), id: 43, gate: 143, pinned_by: 3, rev: 4 };
    const g = {
      id: 3, task: "t-1", kind: "question", name: "question", status: "open", intents: [{ id: "answer", label: "Answer", answer: true }],
      subject: { id: 3, kind: "question", text: "Approve, or comment?", recommend: "approve", pinned: [approve, comment],
        options: [{ id: "approve", label: "Approve", draft: { id: 42, rev: 3 } }, { id: "comment", label: "Comment", draft: { id: 43, rev: 3 } }, { id: "gone", label: "Gone", draft: { id: 44, rev: 1 } }] },
    };
    const c = card(g);
    expect(all(c, ".d-pin[data-draft]").map((x) => x.getAttribute("data-draft"))).toEqual(["42", "43"]);
    expect(all(c, ".opt").map((o) => o.getAttribute("data-pin"))).toEqual(["42@3", "43@3", "44@1"]);
    expect(all(c, '.opt[aria-pressed="true"]')).toHaveLength(0);
    expect(all(c, ".d-pin button")).toHaveLength(0);
    const second = all(c, ".d-pin")[1];
    expect(texts(second, ".d-note.err")[0]).toContain("now at rev 4");
    expect(texts(all(c, ".d-pin")[2], ".d-note.err")).toEqual(["Approves draft #44, which is gone."]);
    expect(texts(c, ".d-pin .pv").length).toBeGreaterThan(0);
  });
});

describe("what the migration is for", () => {
  test("an update to the gate and its task keeps the answer box, its text, size and focus, and the pick", async () => {
    const t = $state({ id: "t-1", title: "Retry flaky uploads" });
    const g = $state(questionGate("Retry or revert?", "b", { id: "a", label: "Retry" }, { id: "b", label: "Revert" }));
    const { container: c } = render(GateCard, { task: t, gate: g });
    const box = byFocus(c, "gate:3:text") as HTMLTextAreaElement;
    box.focus();
    box.style.height = "240px";
    await fireEvent.input(Object.assign(box, { value: "Take the timeout case first" }));
    await fireEvent.click(byFocus(c, "gate:3:option:a"));

    t.title = "Retry the flaky uploads";
    g.subject.text = "Retry, or revert the change?";
    g.subject.created_at = "2026-11-07T11:00:00Z";
    g.intents.push({ id: "later", label: "Later" });
    flushSync();

    const after = byFocus(c, "gate:3:text") as HTMLTextAreaElement;
    expect(after).toBe(box);
    expect(after.value).toBe("Take the timeout case first");
    expect(after.style.height).toBe("240px");
    expect(document.activeElement).toBe(after);
    expect(all(c, ".opt").map((o) => o.getAttribute("aria-pressed"))).toEqual(["false", "true"]);
    expect(texts(c, "button.act")).toEqual(["1 Answer", "2 Dismiss", "3 Later"]);
  });

  test("a strings change rewords the card while it is open", () => {
    const c = card(questionGate("Which?", "a", { id: "a", label: "Retry" }));
    expect(texts(c, ".rec")).toEqual(["recommended"]);
    I18N.strings = { recommended: "recomendada" };
    flushSync();
    expect(texts(c, ".rec")).toEqual(["recomendada"]);
  });
});

describe("rf1_reattack5_test.go, a pinned recommendation", () => {
  const pinned = (): J => {
    const d = { ...reviewDraft(), rev: 3, preview: "Request changes with 2 inline comments" };
    return {
      id: 3, task: "t-1", kind: "question", name: "question", status: "open", text: "The agent asks",
      intents: [{ id: "answer", label: "Answer", answer: true }, { id: "dismiss", label: "Dismiss" }],
      subject: { id: 3, kind: "question", text: "Hold, post nothing?", pinned: [d], recommend: "post",
        options: [{ id: "post", label: "Hold, post nothing", draft: { id: 7, rev: 3 } }, { id: "wait", label: "Wait" }] },
    };
  };

  test("TestReattackFreeAnswerSendsThePinnedRecommendation: a typed answer does not carry the pinned option along", async () => {
    const posts = recordPosts();
    const c = card(pinned());
    await fireEvent.input(Object.assign(byFocus(c, "gate:3:text"), { value: "rewrite the second finding" }));
    await fireEvent.click(byFocus(c, "gate:3:answer"));
    await settle();
    expect(posts).toEqual([{ path: "/api/gates/3/answer", body: { source: "cockpit", args: { answer: "rewrite the second finding" } } }]);
  });

  test("TestAPickedPinnedOptionIsSent: picking the pinned option sends it with the text typed", async () => {
    const posts = recordPosts();
    const c = card(pinned());
    await fireEvent.click(byFocus(c, "gate:3:option:post"));
    await fireEvent.input(Object.assign(byFocus(c, "gate:3:text"), { value: "and thank them" }));
    await fireEvent.click(byFocus(c, "gate:3:answer"));
    await settle();
    expect(posts).toEqual([{ path: "/api/gates/3/answer", body: { source: "cockpit", args: { answer: "post\nand thank them" } } }]);
  });
});
