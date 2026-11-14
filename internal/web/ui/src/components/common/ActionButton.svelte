<script lang="ts">
  import type { Snippet } from "svelte";
  import { pending, runAction } from "../../state/actions.svelte";

  // An action button: a click runs its action once, the button off and busy until it answers, and the notices it
  // raises meanwhile are its result. slot is its data-focus key, which names the action across draws.
  let {
    slot,
    run,
    class: cls = "act",
    title = undefined,
    disabled = false,
    rove = false,
    label = undefined,
    keyshortcuts = undefined,
    stop = false,
    children,
  }: {
    slot: string;
    run: (e: MouseEvent) => unknown;
    class?: string;
    title?: string;
    disabled?: boolean;
    rove?: boolean;
    label?: string;
    keyshortcuts?: string;
    stop?: boolean;
    children: Snippet;
  } = $props();

  const busy = $derived(pending.has(slot));
</script>

<button
  type="button"
  class={cls}
  {title}
  data-focus={slot}
  tabindex={rove ? -1 : undefined}
  data-rove={rove ? "" : undefined}
  aria-label={label}
  aria-keyshortcuts={keyshortcuts}
  aria-busy={busy ? "true" : undefined}
  disabled={disabled || busy}
  onclick={(e) => {
    if (stop) e.stopPropagation();
    return runAction(slot, () => run(e));
  }}>{@render children()}</button>
