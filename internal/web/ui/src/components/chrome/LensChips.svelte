<script lang="ts">
  import type { Lens } from "../../lib/lens";
  import { lensChips } from "./toolbar";

  let { lens, applied = undefined }: { lens: Lens; applied?: string[] } = $props();
  const chips = $derived(lensChips(lens, applied));
</script>

{#each chips as c (c.slot)}
  <button type="button" class={["chip", "pill", "on", c.off ? "off" : ""].filter(Boolean).join(" ")} data-focus={c.slot} title={c.title} aria-label={c.label} onclick={c.remove}>{c.text}</button>
{/each}
