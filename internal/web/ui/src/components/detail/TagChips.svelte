<script lang="ts">
  import type { Lens } from "../../lib/lens";
  import Facet from "../common/Facet.svelte";
  import { TAG_CHIPS } from "./unit";

  // A brief's key:value tags, the first TAG_CHIPS and a +N unless all; with a lens each is a facet.
  let { tags, lens = undefined, slot = "", all = false }: { tags: string[]; lens?: Lens; slot?: string; all?: boolean } = $props();

  const shown = $derived(all ? tags : tags.slice(0, TAG_CHIPS));
  const rest = $derived(tags.slice(shown.length));
</script>

{#each shown as tag, i (i + ":" + tag)}
  {#if lens}
    <Facet {lens} field="tag" raw={tag} slot={slot + "tag:" + tag}><span class="pill tag">{tag}</span></Facet>
  {:else}
    <span class="pill tag">{tag}</span>
  {/if}
{/each}
{#if rest.length}<span class="pill tag" title={rest.join(", ")}>{`+${rest.length}`}</span>{/if}
