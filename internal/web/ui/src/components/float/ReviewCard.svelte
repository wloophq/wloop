<script lang="ts">
  import { fmtNumber, t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { itemKey, optionOf, type FloatState } from "../../state/float.svelte";
  import { subjectOf } from "../gates/gates";
  import { draftSummary, hovered, itemLink, itemRef, itemTitle, toneClass } from "./float";

  // A question whose options pin held drafts, drawn as one card: a tab per option, the recommended one selected
  // until the person picks another, the question, then the selected option's draft: its verdict, its comments
  // counted by label, its text and its comments.
  let { f, item, age, late }: { f: FloatState; item: J; age: string; late: boolean } = $props();

  let more = $state(false);
  let opened = $state(-1);

  const options: J[] = $derived(item.options || []);
  const picked = $derived(optionOf(item));
  const option = $derived(options.find((o) => o.id === picked) || options[0]);
  const draft = $derived(option && option.draft ? subjectOf(option.draft) : null);
  const summary = $derived(draft ? draftSummary(draft) : null);
  const question = $derived(subjectOf(item.gate).text || item.gate.text || "");
  const p = $derived(item.primary || {});
  const ref = $derived(itemRef(item));
  const link = $derived(itemLink(item));
  const others: J[] = $derived((item.gate.intents || []).filter((x: J) => x.id !== p.id && !x.answer));

  function choose(id: string): void {
    f.pick(item, id);
    more = false;
    opened = -1;
  }
</script>

<article class={["fl-hand", "fl-review", toneClass(item.tone)]} aria-label={itemTitle(item)}>
  <header class="fl-card-head">
    <span class="fl-kind">{item.word || item.kind}</span>
    {#if link}
      <button type="button" class="fl-ref link" title={t("Open at its source")} aria-label={t("Open {ref} at its source", { ref })} use:hovered onclick={() => f.openUnit()}>{ref}</button>
    {:else}
      <span class="fl-ref">{ref}</span>
    {/if}
    <span class="fl-sp"></span>
    <span class={["fl-age", late && "late"]}>{age}</span>
  </header>
  <h3 class="fl-card-title">
    <button type="button" class="fl-card-open" title={t("Open in the cockpit")} use:hovered onclick={() => f.open()}>{itemTitle(item)}</button>
  </h3>
  <div class="fl-tabs" role="tablist">
    {#each options as o (o.id)}
      <button type="button" role="tab" class="fl-tab" aria-selected={o.id === picked ? "true" : "false"} onclick={() => choose(o.id)}
        >{o.label}{#if o.recommended}<span class="fl-rec">{t("recommended")}</span>{/if}</button
      >
    {/each}
  </div>
  {#if question}<blockquote class="fl-quote">{question}</blockquote>{/if}
  {#if draft && summary}
    {#if summary.verdict || summary.tally.length}
      <div class="fl-chips">
        {#if summary.verdict}<span class={["fl-chip", "fl-verdict", toneClass(summary.tone)]}>{summary.verdict}</span>{/if}
        {#each summary.tally as g (g.label + (g.blocking ? "!" : ""))}
          <span class={["fl-count-label", g.blocking && "blocking"]}>{fmtNumber(g.n)} {g.label}</span>
        {/each}
      </div>
    {/if}
    {#if draft.text}
      <p class={["fl-text", !more && "clamp"]}>{draft.text}</p>
      <button type="button" class="fl-link" onclick={() => (more = !more)}>{more ? t("Less") : t("More")}</button>
    {/if}
    {#if summary.comments.length}
      <ul class="fl-comments">
        {#each summary.comments as c, i (i)}
          <li>
            <button type="button" class="fl-comment" aria-expanded={opened === i ? "true" : "false"} onclick={() => (opened = opened === i ? -1 : i)}>
              {#if c.label}<span class={["fl-chip", c.blocking && "blocking"]}>{c.label}</span>{/if}
              <span class="fl-at">{c.at}</span>
              <span class={["fl-cbody", opened !== i && "clamp"]}>{c.body}</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
  <div class="fl-acts">
    <button type="button" class="act primary" disabled={f.busy || undefined} onclick={() => f.act(itemKey(item))}><kbd>↵</kbd> {(option && option.primary) || p.label || p.id}</button>
    <button type="button" class="act" onclick={() => f.open()}>{t("Edit")}</button>
    {#each others as x (x.id)}<button type="button" class="act quiet" disabled={f.busy || undefined} onclick={() => f.act(itemKey(item), x.id)}>{x.label}</button>{/each}
  </div>
</article>
