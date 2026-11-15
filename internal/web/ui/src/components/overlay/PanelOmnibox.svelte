<script lang="ts">
  import { onMount } from "svelte";
  import { followStrings } from "../../lib/i18n.svelte";
  import { getJSON } from "../../state/api";
  import { checkPanelBuild } from "../../state/build.svelte";
  import Omnibox from "./Omnibox.svelte";
  import { panelFollow } from "./omnibox";

  // The floating panel's page: the omnibox alone, modal and with no backdrop; Esc hides the panel. Each time
  // the panel shows again after it was left it starts empty and follows the daemon's strings and build.
  let { hide = () => window.close() }: { hide?: () => void } = $props();

  let shown = $state(0);

  onMount(() => {
    document.body.classList.add("panel-omnibox");
    const stop = panelFollow(window, document, {
      reset: () => shown++,
      health: () => getJSON("/api/health"),
      strings: (stamp) => followStrings(stamp, getJSON),
      build: checkPanelBuild,
    });
    return () => {
      stop();
      document.body.classList.remove("panel-omnibox");
    };
  });
</script>

{#key shown}
  <Omnibox panel onclose={() => hide()} />
{/key}
