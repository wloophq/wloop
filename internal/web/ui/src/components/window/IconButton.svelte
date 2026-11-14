<script lang="ts">
  import { keyHint } from "../../state/keycaps.svelte";

  // A button drawn as the icon app.css names by data-icon, titled with the key the served bindings give action.
  let { icon, label, slot, run, action = undefined, disabled = false }: { icon: string; label: string; slot: string; run: () => unknown; action?: string; disabled?: boolean } =
    $props();
</script>

<button type="button" class="icon-btn" data-icon={icon} title={keyHint(label, action)} aria-label={label} data-focus={slot} data-key-action={action} {disabled} onclick={() => run()}
></button>
