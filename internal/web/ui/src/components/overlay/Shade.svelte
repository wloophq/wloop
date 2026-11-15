<script lang="ts">
  import type { Snippet } from "svelte";

  // The backdrop an overlay shows over the page: a click on it, outside the box, dismisses the overlay; the
  // second press of a double click, which lands on the backdrop the first opened, does not.
  let { dismiss, children }: { dismiss: () => void; children: Snippet } = $props();

  function down(e: MouseEvent) {
    if (e.target === e.currentTarget && !(e.detail > 1)) dismiss();
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="pal-back" onmousedown={down}>{@render children()}</div>
