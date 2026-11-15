<script lang="ts">
  import type { Lens } from "../../lib/lens";
  import { t } from "../../lib/i18n.svelte";
  import { clock } from "../../state/clock.svelte";
  import ActionButtons from "../actions/ActionButtons.svelte";
  import Facet from "../common/Facet.svelte";
  import Marked from "../common/Marked.svelte";
  import DraftCard from "../gates/DraftCard.svelte";
  import Pieces from "./Pieces.svelte";
  import { left, rowClass, type RowModel } from "./model";
  import { roveKey, watchWrapped } from "./rove";

  // A row model as a list row, or with card as a board's card: the same slots either way. A row is the control
  // that opens it; a card's title is, stretched over the card.
  //   line 1: glyph, kind verbs, title, changed dot, draft marker | meta and flag pieces, actions
  //   line 2: ref, why                                            | aside, strip dot, who, the time rule's date
  let { model: m, lens, card = false, hidden = false }: { model: RowModel; lens: Lens; card?: boolean; hidden?: boolean } = $props();

  const changedLabel = $derived(t("Changed since you saw it"));
  const hasAfter = $derived(!!(m.project || m.draft));

  function onRowKey(e: KeyboardEvent) {
    if (roveKey(e) || !m.open) return;
    if ((e.key === "Enter" || e.key === " ") && e.target === e.currentTarget) {
      e.preventDefault();
      m.open();
    }
  }
</script>

{#snippet glyph()}
  {#if m.glyph}
    {#if m.glyph.field}
      <Facet {lens} field={m.glyph.field} raw={m.glyph.value || ""} slot={`facet:${m.scope}:${m.glyph.field}:${m.glyph.value}`} words={m.words} shown={m.glyph.label} rove
        ><span
          class="glyph"
          data-category={m.glyph.category || ""}
          data-field={m.glyph.field}
          data-value={m.glyph.value || undefined}
          title={m.glyph.label || undefined}
          aria-label={m.glyph.label || undefined}
          role="img"></span
        ></Facet
      >
    {:else}
      <span class="glyph" data-category={m.glyph.category || ""} title={m.glyph.label || undefined} aria-label={m.glyph.label || undefined} role="img"></span>
    {/if}
  {/if}
{/snippet}

{#snippet line1(title: import("svelte").Snippet)}
  <div class="l1">
    {#if m.star}<span class="star" title={m.words.starred}>★ </span>{/if}{@render title()}{#if m.changed}<span
        class="ch"
        role="img"
        title={changedLabel}
        aria-label={changedLabel}></span
      >{/if}{#if m.draftMark}<span class={m.draftMark.class} role="img" title={m.draftMark.title} aria-label={m.draftMark.title}>{m.draftMark.text}</span>{/if}
  </div>
{/snippet}

{#snippet bits(list: { class: string; text: string; title?: string }[])}
  {#each list as b, i (i)}<span class={b.class} title={b.title || undefined}>{#if b.class === "head"}<Marked text={b.text} ts={lens.ts} />{:else}{b.text}{/if}</span>{/each}
{/snippet}

{#snippet meta()}
  <div class="meta">
    <span class="pieces" use:watchWrapped={!card}
      ><Pieces {m} {lens} part="kind" /><Pieces {m} {lens} part="meta" />{@render bits(m.meta)}<Pieces {m} {lens} part="flag" />{@render bits(m.flags)}</span
    >{#if !card && m.actions}<span class="acts" use:watchWrapped><ActionButtons subject={m.actions} surface="row" scope={m.scope} rove /></span>{/if}
  </div>
{/snippet}

{#snippet line2()}
  <div class="l2">
    <span class="ref"
      ><Pieces {m} {lens} part="ref" />{#each m.ref as r, i (i)}{#if r.where}<Facet {lens} field="where" raw={r.where} slot={r.slot || ""} words={m.words} rove
            ><span class="unit"><Marked text={r.text} ts={lens.ts} /></span></Facet
          >{:else if m.kind === "task"}<span class="unit"><Marked text={r.text} ts={lens.ts} /></span>{:else}<Marked text={r.text} ts={lens.ts} />{/if}{/each}</span
    >{#if m.why}<span class="why" title={m.why}><Marked text={m.why} ts={lens.ts} /></span>{/if}
  </div>
{/snippet}

{#snippet right2()}
  <div class="r2">
    {#if !card && (m.asideLead || (m.parts.aside || []).length || m.agents || m.link)}<span class="aside"
        >{#if m.asideLead}<span class="ws">{m.asideLead}</span>{/if}<Pieces {m} {lens} part="aside" />{#if m.agents}<span class="agent">{m.agents}</span>{/if}{#if m.link}<a
            href={m.link.href}
            target="_blank"
            rel="noopener noreferrer"
            class="ext"
            data-focus={m.link.slot}
            tabindex="-1"
            data-rove=""
            onclick={(e) => e.stopPropagation()}>{m.link.label}</a
          >{/if}</span
      >{/if}<span class="when"
      >{#if m.strip}<Facet {lens} field={m.strip.spec.id} raw={m.strip.value} slot={`facet:${m.scope}:${m.strip.spec.id}`} words={m.words} rove
          ><span class="dot" aria-hidden="true" data-field={m.strip.spec.id} data-value={m.strip.value}>●</span></Facet
        >{/if}<span class="who"><Pieces {m} {lens} part="who" sep=", " /></span>{#if m.age}<span
          class="age countdown"
          data-deadline={m.age.deadline}
          data-provider={m.age.provider}
          title={m.age.title}>{left(m.age.deadline, m.age.words, clock.now)}</span
        >{:else}<Pieces {m} {lens} part="age" />{/if}</span
    >
  </div>
{/snippet}

{#snippet projectFacts()}
  {#if m.project}
    <div class="meta project-facts">
      {#if m.project.reviewDue}<span class="flag">{m.project.reviewDue}</span>{/if}{#if m.project.noNext}<span class="pill">{m.project.noNext}</span>{/if}<span class="pill"
        >{m.project.open}</span
      >{#if m.project.sequential}<span class="pill">{m.project.sequential}</span>{/if}{#if m.project.every}<span class="who">{m.project.every}</span>{/if}{#if m.project.reviewed}<span
          class="who">{m.project.reviewed}</span
        >{/if}
    </div>
  {/if}
  {#if m.draft}<DraftCard task={m.draft.task} draft={m.draft.draft} scope="outbox" />{/if}
{/snippet}

{#snippet rowTitle()}
  <span class="t" title={m.titleTip || m.title || undefined}
    >{#if m.href}<a href={m.href} data-focus={m.scope + ":title"} tabindex="-1" data-rove="" onclick={(e) => e.stopPropagation()}
        ><Marked text={m.title} ts={lens.ts} /></a
      >{:else}<Marked text={m.title} ts={lens.ts} />{/if}</span
  >
{/snippet}

{#snippet cardTitle()}
  {#if m.open}
    <button
      type="button"
      class="open"
      data-focus={m.scope}
      data-rove=""
      title={m.tip || undefined}
      aria-current={m.selected ? "true" : undefined}
      onclick={() => m.open && m.open()}><span class="t" title={m.titleTip || undefined}><Marked text={m.title} ts={lens.ts} /></span></button
    >
  {:else if m.href}
    <a class="open" data-focus={m.scope} data-rove="" title={m.tip || undefined} aria-current={m.selected ? "true" : undefined} href={m.href}
      ><span class="t" title={m.titleTip || undefined}><Marked text={m.title} ts={lens.ts} /></span></a
    >
  {:else}
    <span class="t" title={m.titleTip || undefined}><Marked text={m.title} ts={lens.ts} /></span>
  {/if}
{/snippet}

{#if card}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <li class={rowClass(m, true)} data-row={m.scope} {hidden} onkeydown={roveKey}>
    {@render line1(cardTitle)}{@render glyph()}{@render meta()}{@render line2()}{@render right2()}{@render projectFacts()}
  </li>
{:else}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div
    class={rowClass(m, false)}
    title={m.tip || undefined}
    tabindex="0"
    aria-current={m.selected ? "true" : undefined}
    data-focus={m.scope}
    data-row={m.scope}
    data-rove=""
    {hidden}
    onclick={() => m.open && m.open()}
    onkeydown={onRowKey}>
    {@render glyph()}{@render line1(rowTitle)}{@render meta()}{@render line2()}{@render right2()}{#if hasAfter}<div class="after">{@render projectFacts()}</div>{/if}
  </div>
{/if}
