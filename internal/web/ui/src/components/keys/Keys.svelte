<script lang="ts">
  import { onMount } from "svelte";
  import { appEnv } from "./appEnv";
  import { installKeys } from "./install";

  // The page's keyboard: the dispatcher listens to the document while the cockpit is mounted.
  onMount(() => installKeys(appEnv()).stop);
</script>
