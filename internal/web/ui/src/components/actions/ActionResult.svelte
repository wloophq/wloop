<script lang="ts">
  import { actionResult } from "../../state/actions.svelte";

  // The last answer of the first of the buttons under slots that has one, drawn beside them.
  let { slots }: { slots: string[] } = $props();
  const r = $derived(actionResult(slots));
</script>

{#if r}<p class={"act-result" + (r.err ? " err" : "")} aria-live="polite">{r.text}</p>{/if}
