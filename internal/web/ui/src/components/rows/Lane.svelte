<script lang="ts">
  import { multiCopy, type Group, type Lens } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import OnlyThis from "./OnlyThis.svelte";
  import RowView from "./RowView.svelte";
  import { headClass, type RowModel } from "./model";

  // A board's column for group g of lens, its items drawn by model as cards; an ungrouped board's one lane is
  // titled title.
  let { g, lens, title, model }: { g: Group; lens: Lens; title: string; model: (it: J, copy: string) => RowModel } = $props();

  const label = $derived(g.label || title);
  const cards = $derived(g.rows.map((it) => model(it, multiCopy(g.field, lens.fields) ? g.key : "")));
</script>

<section class="lane" aria-label={label}>
  <h2 class={headClass("lane-head", g, lens)}><span>{label}</span><span class="lane-tools"><OnlyThis {g} {lens} /><span class="count">{g.rows.length}</span></span></h2>
  <ol class="cards">
    {#each cards as m (m.scope)}<RowView model={m} {lens} card />{/each}
  </ol>
</section>
