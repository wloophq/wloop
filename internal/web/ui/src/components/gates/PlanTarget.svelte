<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { planTarget } from "./gates";

  // What a plan item acts on: its task linked to its detail, with the units it came from.
  let { item }: { item: J } = $props();
  const on = $derived(planTarget(item));
</script>

{#snippet out(l: { label: string; href?: string })}{#if l.href}<a href={l.href} target="_blank" rel="noopener noreferrer">{l.label}</a>{:else}<span>{l.label}</span>{/if}{/snippet}

{#if on}
  <p class="meta plan-on">{t("Acts on")} {#if on.task}<a href={on.task.href} data-focus={on.task.focus}>{on.task.title}</a>{#each on.links as l, i (i)} · {@render out(l)}{/each}{:else}{@render out(on.links[0])}{/if}</p>
{/if}
