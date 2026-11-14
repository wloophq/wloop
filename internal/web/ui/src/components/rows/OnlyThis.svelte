<script lang="ts">
  import type { Group, Lens } from "../../lib/lens";
  import { onlyThis } from "./model";

  // A group's "only this" filter, its "show all" once it filters.
  let { g, lens }: { g: Group; lens: Lens } = $props();
  const o = $derived(onlyThis(g, lens));
</script>

{#if o}<button
    type="button"
    class="facet only"
    aria-pressed={o.on ? "true" : "false"}
    data-focus={o.slot}
    aria-label={o.label}
    onclick={(e) => {
      e.stopPropagation();
      o.pick();
    }}>{o.text}</button
  >{/if}
