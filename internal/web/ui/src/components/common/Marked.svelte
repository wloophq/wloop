<script lang="ts">
  import { markedSpans } from "../../lib/lens";

  let { text, ts = [] }: { text: unknown; ts?: string[] } = $props();
  const spans = $derived(markedSpans(text, ts));
</script>

{#each spans as s, i (i)}{#if s.hit}<mark>{s.text}</mark>{:else}{s.text}{/if}{/each}
