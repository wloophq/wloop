<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import type { FloatState } from "../../state/float.svelte";
  import DraftCard from "../gates/DraftCard.svelte";
  import GateCard from "../gates/GateCard.svelte";
  import { queueDraft, queueDraftOf } from "../screens/queue";
  import { itemRef, itemTitle } from "./float";

  // The current item drawn whole: its kind, unit and age, its title, then its gate's own card, which offers the
  // gate's intents, or, for a unit's situation, what it is and the item's primary action.
  let { f, item, age, late }: { f: FloatState; item: J; age: string; late: boolean } = $props();

  const p = $derived(item.primary || {});
</script>

<article class="fl-hand" aria-label={itemTitle(item)}>
  <header class="fl-card-head">
    <span class="fl-kind">{item.word || item.kind}</span>
    <span class="fl-ref">{itemRef(item)}</span>
    <span class="fl-sp"></span>
    <span class={["fl-age", late && "late"]}>{age}</span>
  </header>
  <h3 class="fl-card-title">{itemTitle(item)}</h3>
  {#if item.gate && queueDraft(item.gate)}
    <DraftCard task={item.task} draft={queueDraftOf(item.gate)} scope="float" />
  {:else if item.gate}
    <GateCard task={item.task} gate={item.gate} scope="float" />
  {:else}
    {#if item.situation && item.situation.label}<p class="fl-text">{item.situation.label}</p>{/if}
    <div class="fl-acts">
      {#if p.kind}<button type="button" class="act primary" disabled={f.busy || undefined} onclick={() => f.act()}><kbd>↵</kbd> {p.label || p.id}</button>{/if}
      <button type="button" class="act" onclick={() => f.open()}>{t("Open in the cockpit")}</button>
    </div>
  {/if}
</article>
