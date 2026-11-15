<script lang="ts">
  import { warnings } from "./warnings";

  const items = $derived(warnings());
</script>

<div id="warnings" class="warnings" role="alert" hidden={!items.length}>
  {#each items as w (w.key)}
    <div>{w.text}{#if w.detail}<details><summary>{w.summary}</summary>{w.detail}</details>{/if}</div>
  {/each}
</div>
