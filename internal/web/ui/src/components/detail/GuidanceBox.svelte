<script lang="ts">
  import { drafts } from "../../state/store.svelte";

  // The unit's guidance or free answer; its text lives in drafts.guidance by unit key.
  let { key, placeholder }: { key: string; placeholder: string } = $props();
</script>

<textarea
  id="guidance"
  rows="3"
  {placeholder}
  data-key={key}
  data-focus="detail:guidance"
  bind:value={() => drafts.guidance[key] ?? "", (v) => (drafts.guidance[key] = v)}></textarea>
