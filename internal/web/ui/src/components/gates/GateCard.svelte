<script lang="ts">
  import { fmtDate, t } from "../../lib/i18n.svelte";
  import { safeURL, type J } from "../../lib/util";
  import { gateState } from "../../state/store.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import Markdown from "../common/Markdown.svelte";
  import { gateOption, gateResolving, orderedOptions, pickOption, planReason, resolveGate, subjectOf } from "./gates";
  import PinnedDraft from "./PinnedDraft.svelte";
  import PlanDoes from "./PlanDoes.svelte";
  import PlanTarget from "./PlanTarget.svelte";
  import PlanWhy from "./PlanWhy.svelte";

  // An open gate of task: what it asks, the agent's message folded after its first blocks, what its plan does,
  // its option cards, the answer typed when an intent takes one, then its intents in the profile's order, the
  // first primary, each with the digit that picks it while the card has the focus.
  let { task, gate, scope = "detail" }: { task: J; gate: J; scope?: string } = $props();

  const id = $derived(String(gate.id));
  const intents = $derived((gate.intents || []) as J[]);
  const subject = $derived(subjectOf(gate));
  const deflt = $derived(intents.find((x) => x.id === gate.default));
  const answer = $derived(intents.find((x) => x.answer));
  const title = $derived(gate.text || gate.name || gate.kind);
  const said = $derived(intents.filter((x) => x.summary));
  const picked = $derived(gateOption(gate));
  const options = $derived(answer ? orderedOptions(gate) : []);
  const slots = $derived(intents.map((x) => `gate:${gate.id}:${x.id}`));
  const dim = $derived(gateResolving(gate));

  function keydown(ev: KeyboardEvent): void {
    if (ev.key !== "Enter" || !(ev.metaKey || ev.ctrlKey) || !answer) return;
    ev.preventDefault();
    resolveGate(task, gate, answer.id);
  }
</script>

<article class={["ask", "d-gate", dim && "resolving"]} data-gate={id} data-kind={gate.kind} aria-label={title} aria-busy={dim ? "true" : undefined}>
  <header class="hd">
    <i class="dot" aria-hidden="true"></i>
    <h3 class="d-sub">{title}</h3>
    {#if subject.created_at}<time datetime={subject.created_at}>{fmtDate(subject.created_at, "datetime")}</time>{/if}
  </header>
  <div class="bd">
    {#if subject.text}<Markdown text={subject.text} fold={4} key={`${scope}:${gate.id}`} />{/if}
    {#if gate.plan}
      <div class="gate-plan">
        {#if gate.plan.summary}<p class="plan-summary">{gate.plan.summary}</p>{/if}
        <PlanTarget item={gate.plan} />
        <PlanDoes item={gate.plan} />
        {#if gate.plan.reason}<p class="meta plan-reason">{planReason(gate.plan)}</p>{/if}
        <PlanWhy item={gate.plan} />
      </div>
    {/if}
    {#each gate.units || [] as u, i (i)}
      {@const label = u.title ? `${u.title} · ${u.label}` : u.label}
      <p class="meta gate-about">{#if safeURL(u.link)}<a href={safeURL(u.link)} target="_blank" rel="noopener noreferrer">{label}</a>{:else}<span>{label}</span>{/if}</p>
    {/each}
    {#if said.length}<ul class="meta gate-says">{#each said as x (x.id)}<li><b>{x.label}</b>: {x.summary}</li>{/each}</ul>{/if}
    {#if gate.delivery}<p class="meta">{gate.delivery}</p>{/if}
    {#if gate.deadline}
      <p class="meta">{deflt ? t("{action} at {time} unless you pick another", { action: deflt.label, time: fmtDate(gate.deadline, "datetime") }) : t("Until {time}", { time: fmtDate(gate.deadline, "datetime") })}</p>
    {/if}
    {#if options.length}
      <div class="opts">
        {#each options as o (o.id)}
          <button type="button" class={"opt" + (o.id === picked ? " on" : "")} aria-pressed={o.id === picked ? "true" : "false"} data-focus={`gate:${gate.id}:option:${o.id}`}
            data-pin={o.draft ? `${o.draft.id}@${o.draft.rev}` : undefined} onclick={() => pickOption(gate, o.id)}>
            <span class="r" aria-hidden="true"></span>
            <span class="lb">{o.label}</span>
            {#if o.id === subject.recommend}<span class="rec">{t("recommended")}</span>{/if}
            {#if o.consequence}<span class="cs">{o.consequence}</span>{/if}
          </button>
          {#if o.draft}<PinnedDraft {subject} ref={o.draft} scope={`gate:${gate.id}:option:${o.id}`} />{/if}
        {/each}
      </div>
    {/if}
    {#if subject.draft}<PinnedDraft {subject} ref={subject.draft} scope={`gate:${gate.id}:confirm`} />{/if}
    {#if answer}
      <textarea rows="2" aria-label={t("Answer")} aria-keyshortcuts="Meta+Enter" data-focus={`gate:${gate.id}:text`} onkeydown={keydown}
        bind:value={() => gateState.answers.get(id) || "", (v) => gateState.answers.set(id, v)}></textarea>
    {/if}
    <div class="d-actions">
      {#each intents as x, i (x.id)}
        <ActionButton slot={`gate:${gate.id}:${x.id}`} class={"act" + (i === 0 ? " primary" : "")} keyshortcuts={i < 9 ? String(i + 1) : undefined}
          run={() => resolveGate(task, gate, x.id)}>{#if i < 9}<kbd>{i + 1}</kbd>{/if} {x.label}</ActionButton>
      {/each}
      {#if answer}<span class="keyhint"><kbd>⌘↵</kbd> {answer.label}</span>{/if}
    </div>
    <ActionResult {slots} />
  </div>
</article>
