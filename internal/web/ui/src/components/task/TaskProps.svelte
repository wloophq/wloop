<script lang="ts">
  import type { J } from "../../lib/util";
  import { moreWord, pickTab, propRows } from "./task";

  // The task's facts as a label and value grid; the count of the brief's tags past those shown opens the Brief tab.
  let { e }: { e: J } = $props();

  const rows = $derived(propRows(e));
</script>

{#if rows.length}
  <dl class="props d-props">
    {#each rows as r (r.key)}
      {#if r.kind === "signal"}
        <dt>{r.words.label}</dt><dd class={"tone-" + r.tone}>{r.words.text}{#each r.words.notes as n, i (i)}{" · "}<span class={"tone-" + n.tone}>{n.text}</span>{/each}</dd>
      {:else if r.kind === "risk"}
        <dt>{r.label}</dt><dd data-prop="brief:risk"><span class={"risk-" + r.risk}>{r.text}</span>{#if r.why}{" · " + r.why}{/if}</dd>
      {:else if r.kind === "tags"}
        <dt>{r.label}</dt><dd>{#each r.tags as x, i (i)}<span class="pill">{x}</span>{/each}{#if r.more}<button type="button" class="more" data-focus="task:props:tags" onclick={() => pickTab("brief")}>{moreWord(r.more)}</button>{/if}</dd>
      {:else}
        <dt>{r.label}</dt><dd data-prop={r.prop}>{r.text}</dd>
      {/if}
    {/each}
  </dl>
{/if}
