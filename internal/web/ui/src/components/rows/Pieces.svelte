<script lang="ts">
  import type { Lens } from "../../lib/lens";
  import { agePart } from "../../lib/lens";
  import { clock } from "../../state/clock.svelte";
  import Facet from "../common/Facet.svelte";
  import Marked from "../common/Marked.svelte";
  import { partClass, pieceLabel, pieceTitle, restOf, type RowModel } from "./model";

  // A part's pieces, each a facet button whose label marks the search's hits; the piece that counts the rest past
  // show_max holds no value and stays text. sep goes between the pieces.
  let { m, lens, part, sep = "" }: { m: RowModel; lens: Lens; part: string; sep?: string } = $props();

  const pieces = $derived(m.parts[part] || []);
</script>

{#each pieces as p, i (p.field + ":" + (p.value || "+" + i))}{#if sep && i}{sep}{/if}{#if !p.value}<span
      class={partClass(part)}
      data-field={p.field}
      data-more=""
      title={restOf(m.item, lens, pieces, p.field).join(", ") || undefined}>{p.label}</span
    >{:else}<Facet
      {lens}
      field={p.field}
      raw={p.value}
      slot={`facet:${m.scope}:${p.field}:${p.value}`}
      words={m.words}
      noun={m.nouns[`${p.field}:${p.value}`]}
      rove><span
        class={partClass(part)}
        data-field={p.field}
        data-value={p.value}
        title={agePart(part) ? pieceTitle(p, m.words, clock.now) : undefined}><Marked text={pieceLabel(p)} ts={lens.ts} /></span
      ></Facet
    >{/if}{/each}
