<script lang="ts">
  import { onMount } from "svelte";
  import { appEnv } from "./appEnv";
  import { followFocus } from "./focus";
  import { installKeys } from "./install";

  // The page's keyboard: the dispatcher listens to the document while the cockpit is mounted, and the focus
  // follows a control that goes away.
  onMount(() => {
    const keys = installKeys(appEnv());
    const focus = followFocus();
    return () => {
      keys.stop();
      focus();
    };
  });
</script>
