<script lang="ts">
  import type { J } from "../../lib/util";
  import { keyOf } from "../../lib/util";
  import { readLens } from "../../lib/view";
  import { minute } from "../../state/clock.svelte";
  import { readHash } from "../../state/route.svelte";
  import RowView from "./RowView.svelte";
  import { rowModel } from "./model";

  // A plugin view's list of rows, drawn with the cockpit's row under the view's lens; empty says the view's word.
  let { rows, empty = "" }: { rows: J[]; empty?: string } = $props();

  const lens = $derived(readLens());
  const pr = $derived(readHash().pr);
  const now = $derived(minute());
</script>

{#if !rows.length}
  <p class="empty">{empty || (lens.words || {}).list_empty}</p>
{:else}
  <div class="rows plugin-list">
    {#each rows as r (keyOf(r))}<RowView model={rowModel(r, pr, lens, "", "row", now)} {lens} />{/each}
  </div>
{/if}
