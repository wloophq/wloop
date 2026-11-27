<script lang="ts">
  import Omnibox from "./Omnibox.svelte";
  import OverlayStack from "./OverlayStack.svelte";
  import { closeOmnibox, omni } from "../../state/overlays.svelte";

  // The overlays over the page: the omnibox when open, then the stack the page asks through, newest on top, so
  // a question asked from the omnibox opens over it.
</script>

{#if omni.open}
  {#key omni.open.id}
    <Omnibox items={omni.open.items} text={omni.open.text} onclose={closeOmnibox} />
  {/key}
{/if}
<OverlayStack />
