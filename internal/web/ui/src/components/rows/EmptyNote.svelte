<script lang="ts">
  import type { NotePart } from "./model";

  let { parts }: { parts: NotePart[] } = $props();
</script>

{#each parts as p, i (i)}{#if p.kind === "text"}{p.text}{:else if p.kind === "button"}<button type="button" class="act" data-focus={p.slot} onclick={p.run}
      >{p.text}</button
    >{:else}<a href={p.href} target="_blank" rel="noopener noreferrer" data-focus={p.slot}>{p.text}</a>{/if}{/each}
