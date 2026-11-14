import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { scheduler } from "../../state/actions.svelte";
import { editing, editPayloads, tasksState } from "../../state/store.svelte";
import { leaves, ownDraftTask, pendingDraft, recordPosts, restoreFetch, reviewDraft, taskCatalog } from "../../testing/gates";
import { resetStore } from "../../testing/reset.svelte";
import DraftCard from "./DraftCard.svelte";
import { approveEdited, DRAFT_CARD_HANDLERS, resolving } from "./gates";
import { settle } from "../../testing/settle";

const all = (c: Element, sel: string) => [...c.querySelectorAll(sel)];
const texts = (c: Element, sel: string) => all(c, sel).map((x) => x.textContent || "");
const byFocus = (c: Element, f: string) => c.querySelector(`[data-focus="${f}"]`) as HTMLElement;
const draft = (d: J, e: J = null, extra: J = {}) => render(DraftCard, { props: { task: e, draft: d, scope: "outbox", ...extra } }).container;
const labels = (c: Element) => texts(c, "button");

beforeEach(() => {
  resetStore();
  tasksState.catalog = taskCatalog();
  resolving.clear();
  scheduler.load = vi.fn();
});
afterEach(() => restoreFetch());

describe("gate_test.go, the draft card", () => {
  test("TestADraftCardSaysWhyAndWhatSendingDoes", () => {
    tasksState.catalog = { ...taskCatalog(), effects: { "task.nudged": "The task is marked as nudged." } };
    const c = draft(pendingDraft());
    for (const s of ["No answer since Tuesday", "The task is marked as nudged.", "Any news on the retry change?"]) expect(c.textContent).toContain(s);
    const links = all(c, "a").map((a) => a.getAttribute("href") || "");
    expect(links).toHaveLength(2);
    expect(links[0]).toContain("task=t-1");
    expect(links[1]).toBe("https://example.test/thread");
  });

  test("TestADraftCardShowsItsLanguage", () => {
    const none = { ...pendingDraft(), language: "" };
    expect([...texts(draft(pendingDraft()), ".lang"), ...texts(draft(none), ".lang")]).toEqual(["Brazilian Portuguese"]);
  });

  test("TestADraftCardOffersOnlyTheActionsServed", async () => {
    const posts = recordPosts();
    const offered = { ...pendingDraft(), actions: [{ id: "draft.close-silently", enabled: true }, { id: "draft.skip-workflow", enabled: true }, { id: "draft.edit", enabled: true }] };
    const refused = { ...pendingDraft(), actions: [{ id: "draft.close-silently", reason: "the conversation is open elsewhere" }] };
    const without = labels(draft(pendingDraft()));
    expect(without).not.toContain("Close without a message");
    expect(without).not.toContain("Don't nudge this conversation");
    const c = draft(offered);
    expect(labels(c)).toContain("Close without a message");
    expect(labels(c)).toContain("Don't nudge this conversation");
    const off = all(draft(refused), "button").filter((b) => b.textContent === "Close without a message") as HTMLButtonElement[];
    expect(off.map((b) => b.disabled)).toEqual([true]);
    expect(off[0].getAttribute("title")).toBe("the conversation is open elsewhere");
    await fireEvent.click(byFocus(c, "outbox:draft.close-silently:draft:7"));
    await settle();
    await fireEvent.click(byFocus(c, "outbox:draft.skip-workflow:draft:7"));
    await settle();
    expect(posts).toEqual([
      { path: "/api/drafts/7/close-silently", body: { rev: 3 } },
      { path: "/api/units/github/acme%2Fapi%238/skip/fake-nudge", body: { source: "cockpit" } },
    ]);
  });

  test("TestADraftCardTogglesAThreadReplysBroadcast", async () => {
    const posts = recordPosts();
    const box = (c: Element) => all(c, 'input[type="checkbox"]') as HTMLInputElement[];
    const c = draft({ ...pendingDraft(), broadcast: "on" });
    expect(box(c).map((b) => b.checked)).toEqual([true]);
    expect(box(draft(pendingDraft()))).toHaveLength(0);
    expect(box(draft({ ...pendingDraft(), broadcast: "off", state: "sent" })).map((b) => b.disabled)).toEqual([true]);
    const cb = byFocus(c, "outbox:broadcast:draft:7") as HTMLInputElement;
    cb.checked = false;
    await fireEvent.change(cb);
    await settle();
    expect(posts).toEqual([{ path: "/api/tasks/draft:7/edit", body: { source: "cockpit", args: { broadcast: "false", rev: "3" } } }]);
  });

  test("TestADraftCardNamesItsMentions", () => {
    const d = {
      ...pendingDraft(), text: "@{slack:UANA} and @{slack:UBRUNO}, any news? cc @{slack:UCAIO} @{group:channel participants}",
      names: { "slack:UANA": "Ana", "slack:UBRUNO": "Bruno", "group:channel participants": "channel participants" },
    };
    expect(all(draft(d), ".mention").map((m) => ({ text: m.textContent, title: m.getAttribute("title") }))).toEqual([
      { text: "@Ana", title: "slack:UANA" }, { text: "@Bruno", title: "slack:UBRUNO" }, { text: "@slack:UCAIO", title: "slack:UCAIO" },
      { text: "@channel participants", title: "group:channel participants" }]);
  });

  test("TestADraftCardNamesItsPurposeAndStateInWords", () => {
    tasksState.catalog = { ...taskCatalog(), draft_purposes: { nudge_once: "Nudge once" }, draft_states: { failed: "failed to send", held: "waiting for you" } };
    const failed = { ...pendingDraft(), purpose: "nudge_once", state: "failed" };
    const held = { ...pendingDraft(), purpose: "close_quietly" };
    expect([failed, held].map((d) => draft(d).querySelector(".d-sub")!.textContent)).toEqual(["Draft Nudge once (failed to send)", "Draft close_quietly"]);
  });
});

describe("draftcard_test.go", () => {
  test("TestCardShowsFullPayload", () => {
    const d = { ...reviewDraft(), by: "session", author: "Last written by an agent" };
    const c = draft(d);
    expect(all(c, ".pv").map((x) => x.getAttribute("data-value"))).toEqual(leaves(d.fields));
    for (const s of ["Last written by an agent", "Request changes", "Back off\nbetween tries.", "api/retry.go", "Two findings block the merge."]) expect(c.textContent).toContain(s);
    const dts = texts(c, "dt");
    for (const l of ["Verdict", "Inline comments", "start_line"]) expect(dts).toContain(l);
    expect(c.querySelector('.pv[data-value="REQUEST_CHANGES"]')!.getAttribute("title")).toBe("REQUEST_CHANGES");
  });

  test("TestCardEditableFromSchema", async () => {
    const posts = recordPosts();
    const c = draft(reviewDraft());
    expect(all(c, ".d-field")).toHaveLength(0);
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    const sel = all(c, ".d-field")[0] as HTMLSelectElement;
    const options = all(sel, "option") as HTMLOptionElement[];
    expect(options.map((o) => o.value + "=" + o.textContent)).toEqual(["APPROVE=Approve", "REQUEST_CHANGES=Request changes", "COMMENT=Comment"]);
    expect(options.filter((o) => o.selected).map((o) => o.value)).toEqual(["REQUEST_CHANGES"]);
    sel.value = "COMMENT";
    await fireEvent.change(sel);
    expect(all(c, ".d-field").map((x) => x.getAttribute("aria-label"))).toEqual(["Verdict"]);
    await fireEvent.click(byFocus(c, "outbox:approve:draft:7"));
    await settle();
    expect(posts.map((p) => p.path)).toEqual(["/api/gates/12/approve"]);
    const args = posts[0].body.args;
    const payload = JSON.parse(args.payload);
    expect(payload.event).toBe("COMMENT");
    expect(payload.comments).toHaveLength(2);
    expect(args.rev).toBe("3");
    expect(args.text).toBe("Two findings block the merge.");
    expect(editing.has("draft:7")).toBe(false);
  });

  test("TestCardStaleDisablesApprove", async () => {
    const posts = recordPosts();
    const d = { ...reviewDraft(), stale: true, notes: ["Written against an older head of the pull request."] };
    const c = draft(d);
    const buttons = all(c, "button") as HTMLButtonElement[];
    const approve = byFocus(c, "outbox:approve:draft:7") as HTMLButtonElement;
    await fireEvent.click(approve);
    await settle();
    expect(texts(c, ".d-note")).toEqual(d.notes);
    expect(buttons.filter((b) => b.disabled).map((b) => b.textContent)).toEqual(["approve"]);
    expect(approve.getAttribute("title")).toBe(d.notes[0]);
    expect(buttons.filter((b) => !b.disabled)).toHaveLength(2);
    expect(posts).toHaveLength(0);
  });

  test("TestCardRuleSentNotice", () => {
    const d = { ...reviewDraft(), state: "sent", gate: 0, intents: undefined, notes: ["Posted by your rule: gates.approval rule 0."] };
    const c = draft(d);
    expect(texts(c, ".d-note")).toEqual(d.notes);
    expect(all(c, "button")).toHaveLength(0);
    expect(all(c, ".pv")).toHaveLength(leaves(d.fields).length);
  });

  test("TestCardLocksTextItsKindLocks", async () => {
    const posts = recordPosts();
    const bare = { ...pendingDraft(), kind: "resolve", text: "", text_locked: true };
    expect(labels(draft(bare))).not.toContain("edit");
    const c = draft({ ...reviewDraft(), text_locked: true });
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    const sel = c.querySelector("select") as HTMLSelectElement;
    sel.value = "COMMENT";
    await fireEvent.change(sel);
    expect(all(c, "textarea")).toHaveLength(0);
    expect(all(c, "select")).toHaveLength(1);
    await fireEvent.click(byFocus(c, "outbox:approve:draft:7"));
    await settle();
    expect(posts).toHaveLength(1);
    const args = posts[0].body.args;
    expect("text" in args).toBe(false);
    expect(args.payload).toBeTruthy();
    expect(args.rev).toBe("3");
  });

  test("a broadcast toggle the daemon refuses shows the draft's own state again", async () => {
    recordPosts(() => new Error("draft changed"));
    const d = $state({ ...pendingDraft(), broadcast: "on" });
    const c = draft(d);
    const cb = byFocus(c, "outbox:broadcast:draft:7") as HTMLInputElement;
    cb.checked = false;
    await fireEvent.change(cb);
    await settle();
    expect(cb.checked).toBe(true);
  });

  test("TestCardShowsARevisionMadeWhileEditing", async () => {
    const posts = recordPosts();
    const d = $state(reviewDraft());
    const c = draft(d);
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    const sel = c.querySelector("select") as HTMLSelectElement;
    sel.value = "COMMENT";
    await fireEvent.change(sel);
    Object.assign(d, { rev: 4, text: "One finding left.", author: "Last written by an agent" });
    flushSync();
    const conflict = texts(c, ".d-conflict").join("");
    for (const s of ["changed since you began editing", "Last written by an agent", "One finding left."]) expect(conflict).toContain(s);
    expect((all(c, "option") as HTMLOptionElement[]).filter((o) => o.selected).map((o) => o.value)).toEqual(["REQUEST_CHANGES"]);
    await fireEvent.click(byFocus(c, "outbox:approve:draft:7"));
    await settle();
    expect(posts[0].body.args.rev).toBe("4");
    expect(posts[0].body.args.payload).toBeUndefined();
  });

  test("a field picked after the draft changed counts, and is what the approval sends", async () => {
    const posts = recordPosts();
    const d = $state(reviewDraft());
    const c = draft(d);
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    Object.assign(d, { rev: 4 });
    flushSync();
    const sel = c.querySelector("select") as HTMLSelectElement;
    sel.value = "APPROVE";
    await fireEvent.change(sel);
    flushSync();
    expect((all(c, "option") as HTMLOptionElement[]).filter((o) => o.selected).map((o) => o.value)).toEqual(["APPROVE"]);
    await fireEvent.click(byFocus(c, "outbox:approve:draft:7"));
    await settle();
    expect(posts[0].body.args.rev).toBe("4");
    expect(JSON.parse(posts[0].body.args.payload)).toMatchObject({ event: "APPROVE" });
  });

  test("TestCardOffersNoControlForAFieldWithoutOptions", async () => {
    const d = reviewDraft();
    d.fields[1].fields[0].fields[1].editable = true;
    const c = draft(d);
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    expect(all(c, "input, select")).toHaveLength(1);
  });

  test("TestCardShowsItsPreviewAboveTheFullPayload", () => {
    const d = { ...reviewDraft(), preview: "Request changes with **2** inline comments" };
    const c = draft(d);
    expect(texts(c, ".d-preview").map((x) => x.trim())).toEqual(["Request changes with 2 inline comments"]);
    expect(all(draft(reviewDraft()), ".d-preview")).toHaveLength(0);
    expect(all(c, ".pv")).toHaveLength(leaves(d.fields).length);
  });
});

describe("detail_test.go, a draft on its own task", () => {
  test("TestADraftOnATaskApprovesItsEditedText", async () => {
    const posts = recordPosts();
    const d = { id: 11, unit: "github/acme/api#8", kind: "nudge", target: "acme/api#8", text: "Could you take a look?", state: "held", rev: 2 };
    const e = ownDraftTask(d, [{ verb: "approve", enabled: true }, { verb: "dismiss", enabled: true }, { verb: "answer", enabled: true }]);
    const c = render(DraftCard, { task: e, draft: d }).container;
    expect(c.textContent).toContain("Could you take a look?");
    expect(c.textContent).toContain("acme/api#8");
    await fireEvent.click(byFocus(c, "detail:approve:draft:11"));
    await settle();
    expect(posts.splice(0).map((p) => p.path)).toEqual(["/api/tasks/draft:11/approve"]);

    await fireEvent.click(byFocus(c, "detail:open:approve:draft:11"));
    await settle();
    await fireEvent.input(Object.assign(byFocus(c, "detail:draft-text:draft:11"), { value: "Could you review the retry change today?" }));
    await fireEvent.click(byFocus(c, "detail:approve:draft:11"));
    await settle();
    expect(posts.splice(0)).toEqual([{ path: "/api/tasks/draft:11/approve", body: { source: "cockpit", args: { text: "Could you review the retry change today?", rev: "2" } } }]);
    expect(editing.has("draft:11")).toBe(false);

    // The approve key while the draft is edited goes through approveEdited, as the detail pane's act does.
    await fireEvent.click(byFocus(c, "detail:open:approve:draft:11"));
    await settle();
    await fireEvent.input(Object.assign(byFocus(c, "detail:draft-text:draft:11"), { value: "Ping on the retry change" }));
    await approveEdited(e);
    expect(posts).toEqual([{ path: "/api/tasks/draft:11/approve", body: { source: "cockpit", args: { text: "Ping on the retry change", rev: "2" } } }]);
  });

  test("TestADraftCardRunsTheVerbsOfItsHandlers", async () => {
    tasksState.catalog = { verbs: [{ verb: "approve", handler: "approve" }, { verb: "toss", handler: "drop" }, { verb: "edit", handler: "retry" }, { verb: "retry", handler: "edit" }, { verb: "look", handler: "seen" }] };
    const posts = recordPosts();
    const d = { id: 12, unit: "github/acme/api#8", kind: "nudge", target: "acme/api#8", text: "Could you take a look?", state: "failed", rev: 3 };
    const e = ownDraftTask(d, ["approve", "toss", "edit", "retry", "look"].map((verb) => ({ verb, enabled: true })));
    const c = render(DraftCard, { task: e, draft: d }).container;
    await fireEvent.click(byFocus(c, "detail:edit:draft:12"));
    await settle();
    expect(posts.splice(0)).toEqual([{ path: "/api/tasks/draft:12/edit", body: { source: "cockpit", args: { rev: "3" } } }]);
    await fireEvent.click(byFocus(c, "detail:toss:draft:12"));
    await settle();
    expect(posts.splice(0)).toEqual([{ path: "/api/tasks/draft:12/toss", body: { source: "cockpit" } }]);
    await fireEvent.click(byFocus(c, "detail:open:retry:draft:12"));
    await settle();
    expect(editing.has("draft:12")).toBe(true);
    expect(DRAFT_CARD_HANDLERS).toEqual(["approve", "drop", "edit", "retry"]);
  });
});

describe("qcard_test.go, a sent draft's card", () => {
  test("TestASentDraftsDetailShowsItsPost: no action, the post linked, why labeled, the unit by its label, Markdown fields", () => {
    const d = reviewDraft();
    Object.assign(d, { state: "sent", task: "t-1", gate: 0, intents: undefined, where: "", target: "", label: "acme/api#7",
      permalink: "https://example.test/acme/api/pull/7#review-1", why: "REQUEST_CHANGES held for the user", updated_at: "2026-11-07T10:00:00Z" });
    Object.assign(d.fields[1].fields[0].fields[3], { value: "Return the **error**.", markdown: true });
    const e = { id: "draft:7", title: "draft", gates: [{ kind: "draft", status: "approved", subject: d }], verbs: [{ verb: "approve", enabled: false, reason: "sent" }] };
    const c = render(DraftCard, { task: e, draft: d }).container;
    expect(all(c, "button")).toHaveLength(0);
    const posted = c.querySelector(".d-posted")!;
    expect(posted.textContent).not.toBe("");
    expect(posted.querySelector("a")!.getAttribute("href")).toBe(d.permalink);
    expect(texts(c, ".why").join("")).toBe("Why it was proposed: REQUEST_CHANGES held for the user");
    expect(c.querySelector(".ref")!.textContent).toBe("acme/api#7");
    expect(texts(c, "strong")).toEqual(["error"]);
  });
});

describe("what the migration is for", () => {
  test("a store update to the draft and its task keeps the edited textarea node, its value, size and focus", async () => {
    const d = $state(pendingDraft());
    const e = $state({ id: "t-1", title: "Retry flaky uploads", gates: [] as J[] });
    const c = render(DraftCard, { task: e, draft: d, scope: "outbox" }).container;
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    const box = byFocus(c, "outbox:draft-text:draft:7") as HTMLTextAreaElement;
    box.focus();
    box.style.height = "320px";
    box.setSelectionRange(3, 3);
    await fireEvent.input(Object.assign(box, { value: "Any news on the retry change, Ana?" }));

    e.title = "Retry the flaky uploads";
    d.why = "No answer since Monday";
    d.notes = ["Ana was away"];
    d.effects = ["task.nudged", "task.snoozed"];
    d.updated_at = "2026-11-07T11:30:00Z";
    flushSync();

    const after = byFocus(c, "outbox:draft-text:draft:7") as HTMLTextAreaElement;
    expect(after).toBe(box);
    expect(after.value).toBe("Any news on the retry change, Ana?");
    expect(after.style.height).toBe("320px");
    expect(document.activeElement).toBe(after);
    expect(editing.get("draft:7")).toBe("Any news on the retry change, Ana?");
    expect(c.textContent).toContain("No answer since Monday");

    d.rev = 4;
    d.text = "Any news?";
    flushSync();
    expect(byFocus(c, "outbox:draft-text:draft:7")).toBe(box);
    expect(box.value).toBe("Any news on the retry change, Ana?");
    expect(all(c, ".d-conflict")).toHaveLength(1);
  });

  test("an edit in progress outlives the card and comes back when it is drawn again", async () => {
    const c = draft(reviewDraft());
    await fireEvent.click(byFocus(c, "outbox:open:approve:draft:7"));
    await settle();
    await fireEvent.input(Object.assign(byFocus(c, "outbox:draft-text:draft:7"), { value: "Only one finding." }));
    const sel = c.querySelector("select") as HTMLSelectElement;
    sel.value = "APPROVE";
    await fireEvent.change(sel);
    expect(editPayloads.get("draft:7")).toEqual({ rev: 3, fields: { "/event": "APPROVE" } });
    const again = draft(reviewDraft());
    expect((byFocus(again, "outbox:draft-text:draft:7") as HTMLTextAreaElement).value).toBe("Only one finding.");
    expect((again.querySelector("select") as HTMLSelectElement).value).toBe("APPROVE");
  });

  test("a draft's context shows the asks it answers and the items it is about", () => {
    const c = draft(pendingDraft(), null, { context: { asks: [{ summary: "Ship it before Friday", asker: "Bo", quotes: ["Friday at the latest"] }], items: [{ id: "i1", state: "open", summary: "Retry numbers" }] } });
    expect(texts(c, ".d-context .ask-head")).toEqual(["Ship it before Friday · Bo"]);
    expect(texts(c, ".d-context .quote")).toEqual(["Friday at the latest"]);
    expect(texts(c, ".d-context .item")).toEqual(["open Retry numbers"]);
  });

  test("keyOf gives each action its queue key", () => {
    const keys: Record<string, string> = { "queue:primary": "Enter", "queue:edit": "e" };
    const c = draft(pendingDraft(), null, { keyOf: (k: string) => keys[k] || "" });
    const approve = byFocus(c, "outbox:approve:draft:7");
    expect(approve.getAttribute("aria-keyshortcuts")).toBe("Enter");
    expect(approve.textContent).toBe("↵ approve");
    expect(byFocus(c, "outbox:open:approve:draft:7").textContent).toBe("e edit");
  });
});
