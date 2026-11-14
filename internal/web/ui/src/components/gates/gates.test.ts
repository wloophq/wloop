import { render } from "@testing-library/svelte";
import { beforeEach, describe, expect, test } from "vitest";
import { focusSlot } from "../../lib/actions";
import { state, tasksState } from "../../state/store.svelte";
import { quietClose, taskCatalog, useStrings } from "../../testing/gates";
import { resetStore } from "../../testing/reset.svelte";
import { planDoes, planReason, planTarget, planWhy } from "./gates";
import PlanDoes from "./PlanDoes.svelte";
import PlanTarget from "./PlanTarget.svelte";
import PlanWhy from "./PlanWhy.svelte";
import { verbBody } from "../../state/verbs";

beforeEach(() => {
  resetStore();
  tasksState.catalog = taskCatalog();
});

const texts = (c: Element, sel: string) => [...c.querySelectorAll(sel)].map((x) => x.textContent || "");

describe("plan_page_test.go, against the plan pieces the plan page draws", () => {
  test("the pieces say what applying does, to which task, and why, in en and pt-BR", () => {
    for (const [locale, does, posts] of [["en", "Resolve its open items as no longer relevant", "Posts nothing"], ["pt-BR", "Resolver os itens abertos como sem efeito", "Não publica nada"]] as const) {
      useStrings(locale);
      const it = quietClose();
      expect(planDoes(it)).toEqual({ steps: [{ text: does, use: "items.resolve", items: ["Ana asks for the retry numbers", "Bea asks when it ships"] }], postsNothing: true });
      const d = render(PlanDoes, { item: it }).container;
      expect(texts(d, ".plan-do-what")).toEqual([does]);
      expect(texts(d, ".plan-posts")).toEqual([posts]);
      const on = render(PlanTarget, { item: it }).container;
      const links = [...on.querySelectorAll("a")].map((a) => [a.getAttribute("href") || "", a.textContent]);
      expect(links[0][0]).toContain("task=t-06bec1c03a26fadf");
      expect(links[0][1]).toBe("Answer Ana about the retries");
      expect(links).toContainEqual(["https://chat.example/c1", "#general"]);
      expect(on.textContent).toContain("#ops");
      expect(on.textContent).not.toContain("task:t-06bec1c03a26fadf");
      const why = render(PlanWhy, { item: it }).container.querySelector("details.plan-why")!;
      expect(texts(why, "li")).toEqual(["task.items_open = 2"]);
    }
  });

  test("TestAStepThatPostsSaysSoAndAnUnknownStepReadsAsItIs", () => {
    useStrings("en");
    const it = { ...quietClose(), does: [{ use: "draft.propose", reach: "local", says: "Draft a message you approve before it posts", posts: true }, { use: "fake.step", reach: "local" }] };
    const d = planDoes(it)!;
    expect(d.steps.map((s) => s.text)).toEqual(["Draft a message you approve before it posts", "fake.step"]);
    expect(d.postsNothing).toBe(false);
    expect(render(PlanDoes, { item: it }).container.querySelectorAll(".plan-posts")).toHaveLength(0);
  });

  test("TestAStepsArgumentsReadAsAPersonReadsThem", () => {
    useStrings("pt-BR");
    tasksState.catalog = {
      ...taskCatalog(),
      verbs: [...taskCatalog().verbs, { verb: "next", label: "Next" }, { verb: "relate", label: "Relate" }],
      facets: [{ id: "size", label: "Size", fact: "task.prop.size", labels: { m: "Medium" } }],
    };
    const it = {
      ...quietClose(), does: [
        { use: "turn.start", says: "Start an agent turn: {intent}", posts: true, args: { intent: "act", intent_label: "Act on its items" } },
        { use: "task.verb", reach: "local", says: "Run {verb} on the task", args: { verb: "next" } },
        { use: "task.verb", reach: "local", says: "Run {verb} on the task, with “{task}”", args: { verb: "relate", task: "task.link_target_tasks", task_id: "t-2", task_title: "The other half" } },
        { use: "task.set", reach: "local", says: "Set {prop} to {value}", args: { prop: "size", value: "m" } },
      ],
    };
    expect(planDoes(it)!.steps.map((s) => s.text)).toEqual(["Iniciar um turno do agente: Act on its items", "Executar Next na tarefa", "Executar Relate na tarefa, com “The other half”", "Definir Size como Medium"]);
  });

  test("TestAPlanReasonReadsInTheCatalogsWords", () => {
    useStrings("pt-BR");
    const items = [
      { reason: "waits for you", reason_key: "waits for you" },
      { reason: "waits for you while task.items_open 2: the agent is busy", reason_key: "waits for you while {facts}: {refusal}",
        reason_params: { facts: "task.items_open 2", refusal: "the agent is busy" }, reason_facts: [{ name: "task.items_open", value: "2" }] },
      { reason: "failed 3 times: exit 1", reason_key: "failed {n} times: {error}", reason_params: { n: "3", error: "exit 1" } },
      { reason: "Open cockpit" },
      { reason: "a refusal no catalog words" },
      { reason: "the task holds no open item" },
      { reason: "waits for you while task.items_open 0: the task holds no open item", reason_key: "waits for you while {facts}: {refusal}",
        reason_params: { facts: "task.items_open 0", refusal: "the task holds no open item" }, reason_facts: [{ name: "task.items_open", value: "0" }] },
    ];
    expect(items.map(planReason)).toEqual(["espera você", "espera você enquanto task.items_open = 2: the agent is busy", "falhou 3 vezes: exit 1", "Abrir o cockpit",
      "a refusal no catalog words", "a tarefa não tem item aberto", "espera você enquanto task.items_open = 0: a tarefa não tem item aberto"]);
  });

  test("a plan item without a target acts on its unit, and one without facts has no why", () => {
    expect(planTarget({ label: "acme/api#8", link: "javascript:alert(1)" })).toEqual({ links: [{ label: "acme/api#8", href: undefined }] });
    expect(planTarget({})).toBeNull();
    expect(planWhy({ why: { matched: [] } })).toBeNull();
    expect(planDoes({ does: [] })).toBeNull();
  });
});

describe("roundchrome_test.go", () => {
  test("TestTheRoundChromeReadsTheServedGateIntents: a round's buttons keep their focus by the served specs", () => {
    state.specs.byId = {
      go_now: { id: "go_now", label: "Go now", group: "start", verb: "approve", intake: true },
      drop: { id: "drop", label: "Drop", group: "start", verb: "dismiss" },
      look: { id: "look", label: "Look", group: "start", intake: true },
      pause: { id: "pause", label: "Pause", group: "control" },
    };
    expect(["go_now", "look", "drop", "pause", "resume"].map(focusSlot)).toEqual(["start", "start", "open", "pause", "pause"]);
  });
});

test("a verb's body carries its args as strings and drops the unset ones", () => {
  expect(verbBody({ rev: 3, text: "hi", gone: undefined, none: null })).toEqual({ source: "cockpit", args: { rev: "3", text: "hi" } });
  expect(verbBody({})).toEqual({ source: "cockpit" });
});

describe("a card dims until the read after its answer", () => {
  test("an answered gate the daemon still serves open lets go at the next read", async () => {
    const { gateResolving, resolveGate } = await import("./gates");
    const { afterSync } = await import("../../state/sync.svelte");
    const { net } = await import("../../state/api");
    net.fetch = async () => new Response(JSON.stringify({}), { status: 200 });
    const g = { id: "g-1", kind: "question", status: "open", intents: [{ id: "approve", label: "Approve" }] };
    const sent = resolveGate({ id: "e-1", title: "Ship" }, g, "approve");
    expect(gateResolving(g)).toBe(true);
    await sent;
    expect(gateResolving(g)).toBe(true);
    for (const fn of afterSync) fn();
    expect(gateResolving(g)).toBe(false);
  });
});
