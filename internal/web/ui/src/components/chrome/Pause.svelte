<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { setPaused } from "../../state/actions.svelte";
  import { state } from "../../state/store.svelte";

  const paused = $derived(!!(state.health && state.health.paused));
</script>

<button id="pause" type="button" class="act" data-focus="top:pause" hidden={!state.health} disabled={!!state.pausing} onclick={() => setPaused(!paused)}
  >{paused ? t("Resume automatic rounds") : t("Pause automatic rounds")}</button
>
