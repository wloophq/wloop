<script lang="ts">
  import { notice } from "../../state/store.svelte";
</script>

<span id="notice" class={"notice" + (notice.err ? " err" : "")} role="status" aria-live="polite" title={notice.title || undefined}>{notice.text}</span>
