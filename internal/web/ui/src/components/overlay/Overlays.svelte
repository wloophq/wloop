<script lang="ts">
  import { overlays } from "../../state/overlays.svelte";
  import ConfirmBox from "./ConfirmBox.svelte";
  import HelpBox from "./HelpBox.svelte";
  import InputBox from "./InputBox.svelte";
  import Omnibox from "./Omnibox.svelte";
  import { closeOmnibox, omni } from "../../state/overlays.svelte";
  import PaletteBox from "./PaletteBox.svelte";

  // The overlays over the page: the omnibox when open, then the stack the page asks through, newest on top, so
  // a question asked from the omnibox opens over it.
</script>

{#if omni.open}
  {#key omni.open.id}
    <Omnibox items={omni.open.items} text={omni.open.text} onclose={closeOmnibox} />
  {/key}
{/if}
{#each overlays.stack as o (o.id)}
  {#if o.kind === "confirm"}
    <ConfirmBox text={o.text} finish={o.finish} />
  {:else if o.kind === "input"}
    <InputBox label={o.label} placeholder={o.placeholder} value={o.value} finish={o.finish} />
  {:else if o.kind === "palette"}
    <PaletteBox palette={o.palette} finish={o.finish} />
  {:else}
    <HelpBox groups={o.groups} finish={o.finish} />
  {/if}
{/each}
