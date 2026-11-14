<script lang="ts">
  import { state } from "../../state/store.svelte";
  import { syncText } from "./sync";

  const open = $derived(state.conn === "open" && !!state.health);
  const cls = $derived("sync" + (open && state.health.slow_sync ? " slow" : "") + (open && state.health.paused ? " paused" : ""));
</script>

<span id="sync" class={cls}>{syncText(state.conn, state.health)}</span>
