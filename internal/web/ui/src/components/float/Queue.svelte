<script lang="ts">
  import { tick } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { age } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import { clock } from "../../state/clock.svelte";
  import { itemKey, type FloatState } from "../../state/float.svelte";
  import Card from "./Card.svelte";
  import { itemRef, itemTitle, LATE_MS, sinceMS } from "./float";
  import ReviewCard from "./ReviewCard.svelte";

  // The queue in the daemon's order: the current item drawn whole in place, every other one a line. Moving keeps
  // the current card where it was on screen when it can, and always in view.
  let { f }: { f: FloatState } = $props();

  let list: HTMLElement | undefined = $state();
  let anchor = 0;

  const hand = () => (list ? (list.querySelector(".fl-hand") as HTMLElement | null) : null);

  $effect.pre(() => {
    void f.current;
    const h = hand();
    anchor = h && list ? h.getBoundingClientRect().top - list.getBoundingClientRect().top : 0;
  });

  $effect(() => {
    void f.current;
    void tick().then(() => {
      const h = hand();
      const scroller = list && (list.closest(".fl-body") as HTMLElement | null);
      if (!h || !scroller || !list) return;
      scroller.scrollTop += h.getBoundingClientRect().top - list.getBoundingClientRect().top - anchor;
      h.scrollIntoView({ block: "nearest" });
    });
  });

  const late = (it: J) => clock.now - sinceMS(it) > LATE_MS;
  const ageOf = (it: J) => (Number.isNaN(sinceMS(it)) ? "" : age(it.since, clock.now));
</script>

{#if !f.items.length}
  <div class="fl-empty">
    <p class="fl-clear">{t("All clear")}</p>
    <p>{f.empty || t("The loop brings the next thing here.")}</p>
  </div>
{:else}
  <ol class="fl-queue" bind:this={list}>
    {#each f.items as it, i (itemKey(it))}
      <li>
        {#if i === f.current}
          {#if (it.options || []).length}<ReviewCard {f} item={it} age={ageOf(it)} late={late(it)} />{:else}<Card {f} item={it} age={ageOf(it)} late={late(it)} />{/if}
        {:else}
          <button type="button" class="fl-line" onclick={() => f.select(i)}>
            <i class="fl-dot" aria-hidden="true"></i>
            <span class="fl-kind">{it.word || it.kind}</span>
            <span class="fl-title">{[itemRef(it), itemTitle(it)].filter(Boolean).join(" · ")}</span>
            <span class={["fl-age", late(it) && "late"]}>{ageOf(it)}</span>
          </button>
        {/if}
      </li>
    {/each}
  </ol>
{/if}
